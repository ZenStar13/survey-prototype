package bootstrap_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zenstar13/survey-prototype/internal/analytics/app/query"
	"github.com/zenstar13/survey-prototype/internal/bootstrap"
	surveyquery "github.com/zenstar13/survey-prototype/internal/survey/app/query"
)

type harness struct {
	t      *testing.T
	app    *bootstrap.App
	public *httptest.Server
	admin  *httptest.Server
	token  string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	cfg := bootstrap.DefaultConfig()
	cfg.DBPath = filepath.Join(t.TempDir(), "test.db")
	cfg.AdminEmail, cfg.AdminPassword = "admin@example.com", "correct-horse-battery"
	cfg.BcryptCost = 4
	cfg.SubmitPerMinute, cfg.LoginPerMinute = 10_000, 10_000
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	a, err := bootstrap.New(context.Background(), cfg, log)
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, app: a, public: httptest.NewServer(a.PublicHandler()), admin: httptest.NewServer(a.AdminHandler())}
	t.Cleanup(func() { h.public.Close(); h.admin.Close(); a.Close() })
	return h
}

// call performs a request and decodes the JSON response into out (if non-nil).
func (h *harness) call(srv *httptest.Server, method, path string, body any, out any) (int, map[string]any) {
	h.t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, srv.URL+path, r)
	req.Header.Set("Content-Type", "application/json")
	if srv == h.admin && h.token != "" {
		req.Header.Set("Authorization", "Bearer "+h.token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	var generic map[string]any
	_ = json.Unmarshal(raw, &generic)
	if out != nil && res.StatusCode < 300 {
		if err := json.Unmarshal(raw, out); err != nil {
			h.t.Fatalf("decode %s %s: %v (%s)", method, path, err, raw)
		}
	}
	return res.StatusCode, generic
}

func (h *harness) expect(want, got int, ctx string, body map[string]any) {
	h.t.Helper()
	if want != got {
		h.t.Fatalf("%s: status %d, want %d (body %v)", ctx, got, want, body)
	}
}

func errCode(body map[string]any) string {
	e, _ := body["error"].(map[string]any)
	s, _ := e["code"].(string)
	return s
}

func optionID(q surveyquery.QuestionView, label string) string {
	for _, o := range q.Options {
		if o.Label == label {
			return o.ID
		}
	}
	panic("no option " + label)
}

func TestEndToEnd(t *testing.T) {
	h := newHarness(t)
	f := false

	// --- admin auth -----------------------------------------------------------
	st, body := h.call(h.admin, "GET", "/api/v1/surveys", nil, nil)
	h.expect(401, st, "admin without token", body)

	st, body = h.call(h.admin, "POST", "/api/v1/auth/login", map[string]string{"email": "admin@example.com", "password": "wrong-password-123"}, nil)
	h.expect(401, st, "bad password", body)

	var login struct{ Token string }
	st, body = h.call(h.admin, "POST", "/api/v1/auth/login", map[string]string{"email": "ADMIN@example.com", "password": "correct-horse-battery"}, &login)
	h.expect(200, st, "login", body)
	h.token = login.Token

	// --- authoring ------------------------------------------------------------
	surveyBody := map[string]any{
		"title": "Toys & you",
		"questions": []map[string]any{
			{"prompt": "What gender are you?", "type": "single_choice", "options": []string{"Male", "Female", "Other"}},
			{"prompt": "Do you like cars?", "type": "single_choice", "options": []string{"Yes", "No"}},
			{"prompt": "Do you like dolls?", "type": "single_choice", "options": []string{"Yes, a lot", "Sometimes", "No"}},
			{"prompt": "Select the amount of toys you usually have", "type": "single_choice", "required": &f, "options": []string{"0-1", "2-3", "4-6", "7+"}},
		},
	}
	st, body = h.call(h.admin, "POST", "/api/v1/surveys", map[string]any{"title": "", "questions": []any{}}, nil)
	h.expect(422, st, "invalid survey", body)

	var created struct{ ID string }
	st, body = h.call(h.admin, "POST", "/api/v1/surveys", surveyBody, &created)
	h.expect(201, st, "create survey", body)
	sid := created.ID

	st, body = h.call(h.public, "GET", "/api/v1/surveys/"+sid, nil, nil)
	h.expect(404, st, "public cannot see drafts", body)

	st, body = h.call(h.admin, "POST", "/api/v1/surveys/"+sid+"/relations", map[string]any{"question_ids": []string{"a", "b"}}, nil)
	h.expect(409, st, "relations need a live survey", body)

	st, body = h.call(h.admin, "PUT", "/api/v1/surveys/"+sid, surveyBody, nil)
	h.expect(200, st, "drafts are editable", body)

	st, body = h.call(h.admin, "POST", "/api/v1/surveys/"+sid+"/publish", nil, nil)
	h.expect(200, st, "publish", body)

	st, body = h.call(h.admin, "PUT", "/api/v1/surveys/"+sid, surveyBody, nil)
	h.expect(409, st, "live survey is immutable", body)
	if errCode(body) != "survey_not_editable" {
		t.Fatalf("want survey_not_editable, got %v", body)
	}

	// The database refuses it too (defence in depth).
	if _, err := h.app.DB.Exec(`UPDATE survey_questions SET prompt = 'hacked' WHERE survey_id = ?`, sid); err == nil {
		t.Fatal("trigger should block editing questions of a published survey")
	}

	var sv surveyquery.SurveyView
	st, body = h.call(h.public, "GET", "/api/v1/surveys/"+sid, nil, &sv)
	h.expect(200, st, "public survey", body)
	gender, cars, dolls, toys := sv.Questions[0], sv.Questions[1], sv.Questions[2], sv.Questions[3]

	// --- relations ------------------------------------------------------------
	rels := [][]string{
		{gender.ID, cars.ID},           // Q1 × Q2
		{gender.ID, dolls.ID},          // Q1 × Q3
		{gender.ID, cars.ID, dolls.ID}, // Q1 × (Q2 and Q3 together)
		{gender.ID, toys.ID},           // Q1 × Q4 (optional question)
	}
	for _, qs := range rels {
		st, body = h.call(h.admin, "POST", "/api/v1/surveys/"+sid+"/relations", map[string]any{"question_ids": qs}, nil)
		h.expect(201, st, "define relation", body)
	}
	st, body = h.call(h.admin, "POST", "/api/v1/surveys/"+sid+"/relations", map[string]any{"question_ids": rels[0]}, nil)
	h.expect(409, st, "duplicate relation", body)

	// --- answering ------------------------------------------------------------
	type resp struct{ email, g, c, d, toys string }
	data := []resp{
		{"m1@example.com", "Male", "Yes", "No", "2-3"},
		{"m2@example.com", "Male", "Yes", "Sometimes", "4-6"},
		{"m3@example.com", "Male", "No", "No", ""}, // skips the optional question
		{"f1@example.com", "Female", "Yes", "Yes, a lot", "7+"},
		{"f2@example.com", "Female", "No", "Yes, a lot", "2-3"},
		{"f3@example.com", "Female", "No", "Sometimes", ""},
		{"o1@example.com", "Other", "Yes", "Sometimes", "4-6"},
	}
	for _, d := range data {
		answers := []map[string]any{
			{"question_id": gender.ID, "option_ids": []string{optionID(gender, d.g)}},
			{"question_id": cars.ID, "option_ids": []string{optionID(cars, d.c)}},
			{"question_id": dolls.ID, "option_ids": []string{optionID(dolls, d.d)}},
		}
		if d.toys != "" {
			answers = append(answers, map[string]any{"question_id": toys.ID, "option_ids": []string{optionID(toys, d.toys)}})
		}
		st, body = h.call(h.public, "POST", "/api/v1/surveys/"+sid+"/responses", map[string]any{"email": d.email, "answers": answers}, nil)
		h.expect(201, st, "submit "+d.email, body)
	}

	st, body = h.call(h.public, "POST", "/api/v1/surveys/"+sid+"/responses", map[string]any{
		"email":   " M1@Example.com ",
		"answers": []map[string]any{{"question_id": gender.ID, "option_ids": []string{optionID(gender, "Male")}}, {"question_id": cars.ID, "option_ids": []string{optionID(cars, "No")}}, {"question_id": dolls.ID, "option_ids": []string{optionID(dolls, "No")}}},
	}, nil)
	h.expect(409, st, "one response per email", body)

	st, body = h.call(h.public, "POST", "/api/v1/surveys/"+sid+"/responses", map[string]any{
		"email":   "new@example.com",
		"answers": []map[string]any{{"question_id": gender.ID, "option_ids": []string{optionID(gender, "Male")}}},
	}, nil)
	h.expect(422, st, "required questions missing", body)
	fields := body["error"].(map[string]any)["fields"].(map[string]any)
	if _, ok := fields["answers."+cars.ID]; !ok {
		t.Fatalf("expected violation for the cars question, got %v", fields)
	}

	// --- async statistics -----------------------------------------------------
	var rep query.SurveyReport
	h.call(h.public, "GET", "/api/v1/surveys/"+sid+"/results", nil, &rep)
	if !rep.Stale || rep.TotalResponses != 0 {
		t.Fatalf("before the worker runs the report must be stale and empty, got stale=%v total=%d", rep.Stale, rep.TotalResponses)
	}

	h.app.Worker.RunOnce(context.Background())

	st, body = h.call(h.public, "GET", "/api/v1/surveys/"+sid+"/results", nil, &rep)
	h.expect(200, st, "results", body)
	if rep.Stale || rep.TotalResponses != 7 {
		t.Fatalf("want fresh report with 7 responses, got stale=%v total=%d", rep.Stale, rep.TotalResponses)
	}
	if q := rep.Questions[3]; q.Answered != 5 || q.Skipped != 2 {
		t.Errorf("optional question: answered=%d skipped=%d, want 5/2", q.Answered, q.Skipped)
	}

	findSeg := func(r query.RelationReport, label string) query.Segment {
		for _, s := range r.Segments {
			if s.Option.Label == label {
				return s
			}
		}
		t.Fatalf("segment %s not found", label)
		return query.Segment{}
	}
	findCell := func(s query.Segment, labels ...string) query.Cell {
		for _, c := range s.Breakdown {
			match := len(c.Options) == len(labels)
			for i := range labels {
				match = match && c.Options[i].Label == labels[i]
			}
			if match {
				return c
			}
		}
		t.Fatalf("cell %v not found", labels)
		return query.Cell{}
	}

	genderCars := rep.Relations[0]
	male := findSeg(genderCars, "Male")
	if c := findCell(male, "Yes"); male.Respondents != 3 || c.Count != 2 || c.PercentOfSegment != 66.7 {
		t.Errorf("male & likes cars: segment=%d count=%d pct=%v, want 3/2/66.7", male.Respondents, c.Count, c.PercentOfSegment)
	}
	if c := findCell(findSeg(genderCars, "Female"), "Yes"); c.Count != 1 {
		t.Errorf("female & likes cars = %d, want 1", c.Count)
	}

	threeWay := rep.Relations[2]
	if c := findCell(findSeg(threeWay, "Male"), "Yes", "No"); c.Count != 1 {
		t.Errorf("male & cars=Yes & dolls=No = %d, want 1", c.Count)
	}
	if n := len(findSeg(threeWay, "Male").Breakdown); n != 6 {
		t.Errorf("3-way breakdown must contain every combination (2×3=6), got %d", n)
	}

	genderToys := rep.Relations[3]
	if genderToys.Respondents != 5 || genderToys.Excluded != 2 {
		t.Errorf("relation with optional question: respondents=%d excluded=%d, want 5/2", genderToys.Respondents, genderToys.Excluded)
	}

	// A relation added after the fact is backfilled by the next worker run.
	st, body = h.call(h.admin, "POST", "/api/v1/surveys/"+sid+"/relations", map[string]any{"name": "Cars vs dolls", "question_ids": []string{cars.ID, dolls.ID}}, nil)
	h.expect(201, st, "late relation", body)
	h.call(h.admin, "GET", "/api/v1/surveys/"+sid+"/report", nil, &rep)
	if late := rep.Relations[4]; late.Computed {
		t.Fatal("new relation must be pending until the worker runs")
	}
	h.app.Worker.RunOnce(context.Background())
	h.call(h.admin, "GET", "/api/v1/surveys/"+sid+"/report", nil, &rep)
	if late := rep.Relations[4]; !late.Computed || late.Respondents != 7 || findCell(findSeg(late, "Yes"), "Sometimes").Count != 2 {
		t.Errorf("late relation not backfilled correctly: %+v", late)
	}

	// --- privacy: no email leaves the response module ------------------------------
	var leaked int
	if err := h.app.DB.QueryRow(`SELECT COUNT(*) FROM outbox_messages WHERE payload LIKE '%@%'`).Scan(&leaked); err != nil || leaked != 0 {
		t.Errorf("integration events must not carry emails (leaked=%d, err=%v)", leaked, err)
	}

	// --- closing ----------------------------------------------------------------
	st, body = h.call(h.admin, "POST", "/api/v1/surveys/"+sid+"/close", nil, nil)
	h.expect(200, st, "close", body)
	st, body = h.call(h.public, "POST", "/api/v1/surveys/"+sid+"/responses", map[string]any{"email": "late@example.com", "answers": []any{}}, nil)
	h.expect(409, st, "closed survey rejects responses", body)
	st, body = h.call(h.public, "GET", "/api/v1/surveys/"+sid+"/results", nil, nil)
	h.expect(200, st, "closed survey keeps its results", body)

	// --- logout ------------------------------------------------------------------
	st, body = h.call(h.admin, "POST", "/api/v1/auth/logout", nil, nil)
	h.expect(204, st, "logout", body)
	st, body = h.call(h.admin, "GET", "/api/v1/surveys", nil, nil)
	h.expect(401, st, "token revoked", body)
}

func TestPublicUIServed(t *testing.T) {
	h := newHarness(t)
	res, err := http.Get(h.public.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 || !strings.Contains(string(b), "<title>Surveys</title>") {
		t.Fatalf("UI not served: %d", res.StatusCode)
	}
}
