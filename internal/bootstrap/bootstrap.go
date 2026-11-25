// Package bootstrap is the application's composition root: it opens the
// database, builds every module, and assembles the two HTTP servers and the
// background worker. Nothing else in the codebase knows about all modules.
package bootstrap

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/zenstar13/survey-prototype/internal/analytics"
	"github.com/zenstar13/survey-prototype/internal/identity"
	"github.com/zenstar13/survey-prototype/internal/identity/app/command"
	"github.com/zenstar13/survey-prototype/internal/platform/clock"
	"github.com/zenstar13/survey-prototype/internal/platform/httpx"
	"github.com/zenstar13/survey-prototype/internal/platform/id"
	"github.com/zenstar13/survey-prototype/internal/platform/outbox"
	"github.com/zenstar13/survey-prototype/internal/platform/sqlitedb"
	"github.com/zenstar13/survey-prototype/internal/platform/worker"
	"github.com/zenstar13/survey-prototype/internal/response"
	"github.com/zenstar13/survey-prototype/internal/survey"
	"github.com/zenstar13/survey-prototype/internal/webui"
)

// Config is read from the environment (12-factor style); see ConfigFromEnv.
type Config struct {
	DBPath     string
	PublicAddr string
	AdminAddr  string

	RunPublic bool
	RunAdmin  bool
	RunWorker bool

	WorkerInterval time.Duration
	SessionTTL     time.Duration
	BcryptCost     int

	AdminEmail    string // optional bootstrap administrator
	AdminPassword string

	SubmitPerMinute int // per client IP, public submission endpoint
	LoginPerMinute  int // per client IP, admin login endpoint
}

func DefaultConfig() Config {
	return Config{
		DBPath:          "data/survey.db",
		PublicAddr:      ":8080",
		AdminAddr:       "127.0.0.1:8081", // admin is loopback-only unless explicitly exposed
		RunPublic:       true,
		RunAdmin:        true,
		RunWorker:       true,
		WorkerInterval:  time.Second,
		SessionTTL:      12 * time.Hour,
		SubmitPerMinute: 60,
		LoginPerMinute:  10,
	}
}

// ConfigFromEnv overlays environment variables on the defaults:
//
//	SURVEY_DB_PATH, SURVEY_PUBLIC_ADDR, SURVEY_ADMIN_ADDR,
//	SURVEY_RUN (comma list of public,admin,worker; default all),
//	SURVEY_WORKER_INTERVAL (e.g. 1s), SURVEY_SESSION_TTL (e.g. 12h),
//	SURVEY_ADMIN_EMAIL, SURVEY_ADMIN_PASSWORD,
//	SURVEY_SUBMIT_RATE_PER_MINUTE, SURVEY_LOGIN_RATE_PER_MINUTE
func ConfigFromEnv() (Config, error) {
	c := DefaultConfig()
	str := func(key string, dst *string) {
		if v, ok := os.LookupEnv(key); ok {
			*dst = v
		}
	}
	str("SURVEY_DB_PATH", &c.DBPath)
	str("SURVEY_PUBLIC_ADDR", &c.PublicAddr)
	str("SURVEY_ADMIN_ADDR", &c.AdminAddr)
	str("SURVEY_ADMIN_EMAIL", &c.AdminEmail)
	str("SURVEY_ADMIN_PASSWORD", &c.AdminPassword)

	var errs []error
	dur := func(key string, dst *time.Duration) {
		if v, ok := os.LookupEnv(key); ok {
			d, err := time.ParseDuration(v)
			if err != nil || d <= 0 {
				errs = append(errs, fmt.Errorf("%s: invalid duration %q", key, v))
				return
			}
			*dst = d
		}
	}
	num := func(key string, dst *int) {
		if v, ok := os.LookupEnv(key); ok {
			n, err := strconv.Atoi(v)
			if err != nil || n <= 0 {
				errs = append(errs, fmt.Errorf("%s: invalid positive integer %q", key, v))
				return
			}
			*dst = n
		}
	}
	dur("SURVEY_WORKER_INTERVAL", &c.WorkerInterval)
	dur("SURVEY_SESSION_TTL", &c.SessionTTL)
	num("SURVEY_SUBMIT_RATE_PER_MINUTE", &c.SubmitPerMinute)
	num("SURVEY_LOGIN_RATE_PER_MINUTE", &c.LoginPerMinute)

	if v, ok := os.LookupEnv("SURVEY_RUN"); ok {
		c.RunPublic, c.RunAdmin, c.RunWorker = false, false, false
		for _, part := range strings.Split(v, ",") {
			switch strings.TrimSpace(part) {
			case "public":
				c.RunPublic = true
			case "admin":
				c.RunAdmin = true
			case "worker":
				c.RunWorker = true
			case "all":
				c.RunPublic, c.RunAdmin, c.RunWorker = true, true, true
			default:
				errs = append(errs, fmt.Errorf("SURVEY_RUN: unknown component %q", part))
			}
		}
	}
	if (c.AdminEmail == "") != (c.AdminPassword == "") {
		errs = append(errs, errors.New("SURVEY_ADMIN_EMAIL and SURVEY_ADMIN_PASSWORD must be set together"))
	}
	return c, errors.Join(errs...)
}

// App holds the built modules and the assembled runtime components.
type App struct {
	cfg Config
	log *slog.Logger
	DB  *sql.DB

	Survey    *survey.Module
	Response  *response.Module
	Analytics *analytics.Module
	Identity  *identity.Module

	Relay  *outbox.Relay
	Worker *worker.Runner
}

func New(ctx context.Context, cfg Config, log *slog.Logger) (*App, error) {
	db, err := sqlitedb.Open(cfg.DBPath)
	if err != nil {
		return nil, err
	}
	if err := sqlitedb.Migrate(ctx, db); err != nil {
		db.Close()
		return nil, err
	}

	clk := clock.System{}
	ids := id.UUIDv7{}

	surveyMod := survey.New(db, log, clk, ids)
	responseMod := response.New(db, log, clk, ids, surveyMod.App.Queries.GetSurvey,
		httpx.NewRateLimiter(cfg.SubmitPerMinute, cfg.SubmitPerMinute))
	analyticsMod := analytics.New(db, log, clk, ids, surveyMod.App.Queries.GetSurvey)
	identityMod, err := identity.New(db, log, clk, identity.Config{
		SessionTTL: cfg.SessionTTL, BcryptCost: cfg.BcryptCost,
		LoginLimiter: httpx.NewRateLimiter(cfg.LoginPerMinute, cfg.LoginPerMinute),
	})
	if err != nil {
		db.Close()
		return nil, err
	}

	relay := outbox.NewRelay(db, log.With("component", "outbox"), clk)
	analyticsMod.Subscribe(relay)
	jobs := append([]worker.Job{relay}, analyticsMod.Jobs()...)

	a := &App{
		cfg: cfg, log: log, DB: db,
		Survey: surveyMod, Response: responseMod, Analytics: analyticsMod, Identity: identityMod,
		Relay: relay, Worker: worker.NewRunner(cfg.WorkerInterval, log.With("component", "worker"), jobs...),
	}

	if cfg.AdminEmail != "" {
		if err := identityMod.App.Commands.EnsureAdmin.Handle(ctx, command.EnsureAdmin{
			AdminID: ids.NewID(), Email: cfg.AdminEmail, Password: cfg.AdminPassword,
		}); err != nil {
			db.Close()
			return nil, fmt.Errorf("bootstrap admin: %w", err)
		}
	}
	return a, nil
}

func (a *App) Close() error { return a.DB.Close() }

func baseMiddleware(log *slog.Logger, name string) []httpx.Middleware {
	return []httpx.Middleware{httpx.Recoverer(log), httpx.RequestID(), httpx.AccessLog(log, name), httpx.SecurityHeaders()}
}

// PublicHandler is the end-user server: browse surveys, answer, see results.
// No authentication (phase 1); submissions are rate limited per IP.
func (a *App) PublicHandler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /healthz", httpx.Health())
	mux.Handle("GET /{$}", webui.Handler())
	a.Survey.RegisterPublicRoutes(mux)
	a.Response.RegisterPublicRoutes(mux)
	a.Analytics.RegisterPublicRoutes(mux)
	mux.Handle("/", httpx.NotFoundHandler())
	return httpx.Chain(mux, baseMiddleware(a.log, "public")...)
}

// AdminHandler is the back-office server. Everything except login and health
// requires an admin bearer token.
func (a *App) AdminHandler() http.Handler {
	protected := http.NewServeMux()
	a.Survey.RegisterAdminRoutes(protected)
	a.Analytics.RegisterAdminRoutes(protected)
	a.Identity.RegisterProtectedRoutes(protected)
	protected.Handle("/", httpx.NotFoundHandler())

	root := http.NewServeMux()
	root.Handle("GET /healthz", httpx.Health())
	a.Identity.RegisterPublicRoutes(root)
	root.Handle("/", a.Identity.RequireAdmin(protected))
	return httpx.Chain(root, baseMiddleware(a.log, "admin")...)
}

// Run starts the configured components and blocks until ctx is cancelled or
// one of them fails.
func (a *App) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	type result struct {
		name string
		err  error
	}
	results := make(chan result, 3)
	running := 0
	start := func(name string, fn func() error) {
		running++
		go func() { results <- result{name, fn()} }()
	}
	if a.cfg.RunPublic {
		srv := httpx.NewServer(a.cfg.PublicAddr, a.PublicHandler())
		start("public", func() error { return httpx.Serve(ctx, srv, "public", a.log) })
	}
	if a.cfg.RunAdmin {
		srv := httpx.NewServer(a.cfg.AdminAddr, a.AdminHandler())
		start("admin", func() error { return httpx.Serve(ctx, srv, "admin", a.log) })
	}
	if a.cfg.RunWorker {
		start("worker", func() error { a.Worker.Start(ctx); return nil })
	}
	if running == 0 {
		return errors.New("nothing to run: SURVEY_RUN selected no components")
	}

	var firstErr error
	for i := 0; i < running; i++ {
		r := <-results
		if r.err != nil && firstErr == nil {
			firstErr = fmt.Errorf("%s: %w", r.name, r.err)
		}
		cancel() // one component stopping stops the others
	}
	return firstErr
}
