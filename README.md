# Survey platform — prototype

A Go + SQLite modular monolith with strict **DDD**, **hexagonal architecture** and
**CQRS**. It runs two HTTP servers (end-user and admin) plus a background worker
that computes statistics.

```
make run     # starts public :8080, admin 127.0.0.1:8081 and the worker (admin@example.com / change-me-please)
make demo    # in another terminal: seeds the example survey through the APIs and prints the report
make test    # unit + end-to-end tests (race detector on)
```

Then open <http://localhost:8080> to answer surveys and browse results.

Requires Go 1.24+. There is no cgo (it uses the pure-Go `modernc.org/sqlite`). The only
other dependency is `golang.org/x/crypto` for bcrypt.

---

## 1. Architecture at a glance

```
                 ┌──────────── public server :8080 (no auth) ────────────┐
 respondents ──▶ │ GET surveys · POST responses · GET results · web UI  │
                 └──────────────────────────────────────────────────────┘
                 ┌──────────── admin server 127.0.0.1:8081 (Bearer) ─────┐
 admins ───────▶ │ login · surveys CRUD/publish/close · relations · report│
                 └──────────────────────────────────────────────────────┘
                                │ commands / queries
     ┌──────────────┬───────────┴──────┬────────────────┐
     │   survey     │    response      │   analytics    │  identity
     │ (authoring)  │   (answering)    │ (relations +   │  (admin auth)
     │              │                  │  statistics)   │
     └──────┬───────┴────────┬─────────┴───────▲────────┘
            │  outbox (same transaction as the write)
            └────────────────┴──▶ worker: outbox relay ──▶ analytics subscribers
                                          stats refresher ──▶ projections
                          one SQLite file (WAL)
```

### Modules (bounded contexts)

| Module | Aggregate(s) | Owns tables | Talks to others via |
|---|---|---|---|
| `survey` | `Survey` (root) → `Question` → `Option` | `surveys`, `survey_questions`, `survey_question_options` | publishes `survey.published/closed` events; exposes the `GetSurvey` query |
| `response` | `Response` | `responses`, `response_answers` | reads surveys through an ACL over `GetSurvey`; publishes `response.submitted` |
| `analytics` | `Relation` | `analytics_*` | consumes events; reads survey labels through an ACL over `GetSurvey` |
| `identity` | `Admin`, `Session` | `identity_*` | provides the admin auth middleware |

Analytics **never reads another module's tables**. It builds its own answer
facts from `response.submitted` events, so it could become a separate service
without changes to its domain.

### Layout inside a module (same shape in every module)

```
internal/<module>/
  domain/        entities, value objects, aggregate rules, domain events, repository ports. No infra imports.
  app/command/   write use cases (one handler per command) + the ports they need
  app/query/     read use cases + read-model DTOs + read ports
  app/app.go     Application{Commands, Queries}
  adapters/      DRIVEN adapters: SQLite repositories/read models, ACLs onto other modules, bcrypt…
  ports/         DRIVING adapters: http (REST handlers), events (outbox subscribers / worker jobs)
  module.go      the module's composition root (wires adapters into app, applies decorators)
```

Shared code lives in `internal/platform/*` (sqlite, outbox, http plumbing, worker, cqrs
decorators, id/clock). `internal/kernel/errs` holds typed transport-agnostic errors,
`internal/contracts` holds the integration-event topics and payloads, and
`internal/bootstrap` is the app-wide composition root.

### CQRS rules applied

* **Commands return only `error`.** The caller chooses IDs, so the HTTP port generates
  the UUIDv7 and passes it in. That is why `Login` takes the session token as input.
* **Queries never touch aggregates.** They read SQL straight into DTOs (`SurveyView`) or
  read the analytics projections.
* Every handler is wrapped with a logging decorator that never logs payloads, because
  commands can carry emails.
* Writes go aggregate → repository → **outbox in the same transaction**, which gives
  atomic state plus event.

---

## 2. Decisions on the open questions

### REST, not GraphQL
* The use cases are a small, fixed set of *commands* (create, publish, close, submit,
  define relation). They map directly to REST actions like `POST …/publish`. GraphQL
  mutations would add a schema layer without adding anything.
* Public reads (`/results`) can be HTTP-cached (`Cache-Control: max-age=5`). GraphQL
  POST queries cannot be cached that way without extra infrastructure.
* Rate limiting, auth and body limits are per route, using only the stdlib (Go 1.22
  `ServeMux` method/path patterns). No framework is needed.
* Revisit if the admin back-office grows into a rich dashboard with many ad-hoc views.
  A GraphQL read API could sit on the same query handlers.

### Sync vs async
* **Synchronous:** everything that has to give the user a definite answer. That covers
  authoring, publish/close, and response submission (validation, one-per-email via a
  `UNIQUE` constraint, then `201`/`409`/`422`).
* **Asynchronous:** statistics. Submitting a response writes an outbox row in the same
  transaction. The worker (1 s tick) relays it to analytics, which stores facts and
  marks the survey *stale*. The stats refresher then rebuilds that survey's projections.
  A burst of N responses causes **one** rebuild per tick, not N.
* The report shows `computed_at` and `stale: true` while newer data is being processed,
  so the eventual consistency is visible to clients. Admins can force a rebuild with
  `POST /surveys/{id}/report/refresh`.
* Delivery is at-least-once. Every subscriber is idempotent (facts are keyed by
  `response_id`, and a rebuild is a full recompute).

### How relation analytics are stored and calculated
* A **Relation** is an ordered list of 2–4 questions. The first question is the
  *segment*: "of those who said Male, X% said Yes to cars". "Relate Q1 to Q2 and Q3
  together" is a single 3-question relation (Q1 × Q2 × Q3).
* Relations can only be defined on **published/closed** surveys, because question IDs
  are frozen from that point. They can be added at any time and are backfilled with
  existing responses on the next worker tick.
* Computation is a **full recompute** per stale survey, using set-based SQL. It does one
  self-join of the facts table per related question and runs `GROUP BY` on the option
  combination (`analytics/adapters/projector.go`). Results are written to projection
  tables (`analytics_question_stats`, `analytics_relation_{totals,segments,cells}`)
  inside one transaction, so readers never see a half-built report.
* Why recompute instead of incremental counters: it is trivially correct, idempotent and
  self-healing. It handles backfill and removal, and needs no exactly-once delivery. The
  cost is O(answers per survey) per rebuild, which is fine at prototype scale. If
  surveys reach millions of responses, switch the projector to incremental `+1` updates
  keyed by response (the ports would not change).
* Percentages are computed at read time (`query.BuildReport`, a pure function). Empty
  combinations are filled in as zeros.

### Required vs optional questions
* Each question has a `required` flag. In the API it **defaults to `true`**, so making a
  question optional is an explicit decision.
* Required questions must be answered. An optional question may be omitted or sent with
  `option_ids: []` (skipped). Single choice takes exactly one option, multiple choice
  takes one or more. All violations come back at once in a `422`, keyed
  `email` / `answers.<question_id>`.
* In statistics: per-question `answered` and `skipped` counts, with option percentages
  out of *answered*. Relations count only respondents who answered **all** related
  questions; the rest are reported as `excluded`. In multiple choice a respondent
  appears in every cell they picked, so a row can add up to more than 100%.

### Should the end-user service have (simple) auth?
Phase 1 ships **without accounts**, as requested, with these mitigations:
* Emails are normalised (trimmed, lower-cased), and `UNIQUE(survey_id, email)` makes
  the one-response rule atomic.
* Submissions are rate limited per IP (in-memory token bucket, `SURVEY_SUBMIT_RATE_PER_MINUTE`).
* Emails never leave the response module: integration events carry no PII (a test asserts this).

**Recommendation for phase 2:** don't add login. Add **email verification (double
opt-in)**. A submission is stored as `pending`, a signed one-time link is emailed, and
only confirmed responses are counted. Today anyone can submit with someone else's email
and block them, and verification is the minimal fix for that. Optional extras: a
CAPTCHA on submit, and small-cell suppression in public results (hide cells with fewer
than k respondents) so that a tiny segment can't be used to identify a person.

### Immutability of live surveys
This is enforced twice:
1. **Domain:** `Survey.ReviseDraft` returns `survey_not_editable` unless the survey is a
   draft. Status only moves `draft → published → closed`.
2. **Database:** triggers abort any insert, update or delete of questions or options, or
   any content change, once a survey is not a draft. They also block backwards status
   moves and deleting live surveys. Responses can only be inserted for `published`
   surveys. A composite FK guarantees that an answered option belongs to its question.

---

## 3. API

Errors always look like `{"error": {"code": "...", "message": "...", "fields": {...}}}`.
Status codes: `400` malformed, `401` auth, `404`, `409` state conflict, `422` validation,
`429` rate limited.

### Public server (`:8080`, no auth)
| Method & path | Purpose |
|---|---|
| `GET /api/v1/surveys` | published and closed surveys |
| `GET /api/v1/surveys/{id}` | survey form (drafts are 404) |
| `POST /api/v1/surveys/{id}/responses` | `{"email","answers":[{"question_id","option_ids":[…]}]}` → `201 {id}` |
| `GET /api/v1/surveys/{id}/results` | overall stats + relation cross tabs |
| `GET /` | minimal web UI |

### Admin server (`127.0.0.1:8081`, `Authorization: Bearer <token>`)
| Method & path | Purpose |
|---|---|
| `POST /api/v1/auth/login` | `{"email","password"}` → `{token, expires_at}` (rate limited) |
| `POST /api/v1/auth/logout` · `GET /api/v1/auth/me` | |
| `GET /api/v1/surveys?status=draft,published` | list |
| `POST /api/v1/surveys` | create draft: `{"title","description","questions":[{"prompt","type":"single_choice"\|"multiple_choice","required":true,"options":["…"]}]}` |
| `GET /PUT /api/v1/surveys/{id}` | read / replace draft (`409` once live) |
| `POST /api/v1/surveys/{id}/publish` · `…/close` | lifecycle |
| `GET /POST /api/v1/surveys/{id}/relations` | list / define `{"name","question_ids":[…]}` |
| `DELETE /api/v1/surveys/{id}/relations/{relationID}` | remove |
| `GET /api/v1/surveys/{id}/report` · `POST …/report/refresh` | report (drafts included) / force a rebuild |

Example relation result (from `GET …/results`):

```json
{ "name": "Gender × Cars", "respondents": 40, "excluded": 0, "computed": true,
  "segments": [ { "option": {"label": "Male"}, "respondents": 21,
      "breakdown": [ {"options": [{"label": "Yes"}], "count": 15, "percent_of_segment": 71.4},
                     {"options": [{"label": "No"}],  "count": 6,  "percent_of_segment": 28.6} ] } ] }
```

---

## 4. Configuration and deployment

| Env var | Default | |
|---|---|---|
| `SURVEY_DB_PATH` | `data/survey.db` | SQLite file (WAL mode) |
| `SURVEY_PUBLIC_ADDR` | `:8080` | |
| `SURVEY_ADMIN_ADDR` | `127.0.0.1:8081` | loopback by default. Expose it deliberately, behind TLS or a VPN |
| `SURVEY_RUN` | `public,admin,worker` | run any subset per process |
| `SURVEY_WORKER_INTERVAL` | `1s` | |
| `SURVEY_SESSION_TTL` | `12h` | admin bearer session lifetime |
| `SURVEY_ADMIN_EMAIL` / `SURVEY_ADMIN_PASSWORD` | – | creates the first admin if it is missing (password ≥ 12 chars) |
| `SURVEY_SUBMIT_RATE_PER_MINUTE` / `SURVEY_LOGIN_RATE_PER_MINUTE` | `60` / `10` | per client IP |
| `SURVEY_LOG_LEVEL` | `info` | `debug` logs every command and query |

* By default, one binary runs all three components. To split them, start the same binary
  three times with different `SURVEY_RUN` values against the same DB file. SQLite WAL
  plus `busy_timeout` plus `BEGIN IMMEDIATE` makes that safe on one host.
* Run **one** worker. Jobs are idempotent, so two workers would only duplicate work.
  Multi-host deployments would add a lease row or move to Postgres/a queue.
* Servers use read/write/idle timeouts, a 1 MiB body cap, strict JSON (unknown fields
  rejected), security headers, panic recovery, request IDs, structured JSON logs and
  graceful shutdown.

## 5. Known prototype limits / next steps
* Email verification for respondents (see above). Small-cell suppression in public results.
* Question types are single and multiple choice only. Free text and rating scales would
  extend `QuestionType`; free text would not be relatable.
* Rate limiter and worker coordination are single-instance.
* The admin API has no UI. Use curl or `cmd/demo`.
* Responses store the email in plain text, because it is needed for the uniqueness rule.
  Consider a keyed hash (HMAC) if contacting respondents is never required.
