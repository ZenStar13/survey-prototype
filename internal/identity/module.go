// Package identity is the admin authentication module (bounded context).
package identity

import (
	"database/sql"
	"log/slog"
	"net/http"
	"time"

	"github.com/zenstar13/survey-prototype/internal/identity/adapters"
	"github.com/zenstar13/survey-prototype/internal/identity/app"
	"github.com/zenstar13/survey-prototype/internal/identity/app/command"
	"github.com/zenstar13/survey-prototype/internal/identity/app/query"
	porthttp "github.com/zenstar13/survey-prototype/internal/identity/ports/http"
	"github.com/zenstar13/survey-prototype/internal/platform/clock"
	"github.com/zenstar13/survey-prototype/internal/platform/cqrs"
	"github.com/zenstar13/survey-prototype/internal/platform/httpx"
)

type Module struct {
	App  app.Application
	http *porthttp.Handlers
}

type Config struct {
	SessionTTL   time.Duration
	BcryptCost   int // 0 = bcrypt default; tests use the minimum
	LoginLimiter *httpx.RateLimiter
}

func New(db *sql.DB, log *slog.Logger, clk clock.Clock, cfg Config) (*Module, error) {
	log = log.With("module", "identity")
	store := adapters.NewSQLiteStore(db)
	sessions := adapters.NewSQLiteSessions(db)
	hasher := adapters.BcryptHasher{Cost: cfg.BcryptCost}
	login, err := command.NewLoginHandler(store, sessions, hasher, clk, cfg.SessionTTL)
	if err != nil {
		return nil, err
	}
	a := app.Application{
		Commands: app.Commands{
			EnsureAdmin: cqrs.DecorateCommand[command.EnsureAdmin](command.NewEnsureAdminHandler(store, hasher, clk), log),
			Login:       cqrs.DecorateCommand[command.Login](login, log),
			Logout:      cqrs.DecorateCommand[command.Logout](command.NewLogoutHandler(sessions), log),
		},
		Queries: app.Queries{
			Authenticate: cqrs.DecorateQuery[query.Authenticate, query.Principal](query.NewAuthenticateHandler(store, clk), log),
		},
	}
	return &Module{App: a, http: porthttp.NewHandlers(a, log, cfg.LoginLimiter)}, nil
}

func (m *Module) RegisterPublicRoutes(mux *http.ServeMux)     { m.http.RegisterPublic(mux) }
func (m *Module) RegisterProtectedRoutes(mux *http.ServeMux)  { m.http.RegisterProtected(mux) }
func (m *Module) RequireAdmin(next http.Handler) http.Handler { return m.http.RequireAdmin(next) }
