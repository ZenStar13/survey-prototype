// Package http exposes login/logout and the admin authentication middleware.
package http

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/zenstar13/survey-prototype/internal/identity/app"
	"github.com/zenstar13/survey-prototype/internal/identity/app/command"
	"github.com/zenstar13/survey-prototype/internal/identity/app/query"
	"github.com/zenstar13/survey-prototype/internal/identity/domain"
	"github.com/zenstar13/survey-prototype/internal/platform/httpx"
)

type Handlers struct {
	app     app.Application
	log     *slog.Logger
	limiter *httpx.RateLimiter
}

func NewHandlers(a app.Application, log *slog.Logger, limiter *httpx.RateLimiter) *Handlers {
	return &Handlers{app: a, log: log, limiter: limiter}
}

// RegisterPublic mounts endpoints that do not require a session.
func (h *Handlers) RegisterPublic(mux *http.ServeMux) {
	login := http.Handler(http.HandlerFunc(h.login))
	if h.limiter != nil {
		login = httpx.RateLimit(h.limiter, h.log)(login)
	}
	mux.Handle("POST /api/v1/auth/login", login)
}

// RegisterProtected mounts endpoints that require a session.
func (h *Handlers) RegisterProtected(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/auth/logout", h.logout)
	mux.HandleFunc("GET /api/v1/auth/me", h.me)
}

type principalKey struct{}

func PrincipalFrom(ctx context.Context) (query.Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(query.Principal)
	return p, ok
}

// RequireAdmin rejects requests without a valid "Authorization: Bearer <token>".
func (h *Handlers) RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, err := h.app.Queries.Authenticate.Handle(r.Context(), query.Authenticate{Token: bearer(r)})
		if err != nil {
			w.Header().Set("WWW-Authenticate", `Bearer realm="survey-admin"`)
			httpx.WriteError(w, r, h.log, err)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalKey{}, p)))
	})
}

func bearer(r *http.Request) string {
	const prefix = "bearer "
	v := r.Header.Get("Authorization")
	if len(v) > len(prefix) && strings.EqualFold(v[:len(prefix)], prefix) {
		return strings.TrimSpace(v[len(prefix):])
	}
	return ""
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type loginResponse struct {
	Token     string    `json:"token"`
	TokenType string    `json:"token_type"`
	ExpiresAt time.Time `json:"expires_at"`
}

func (h *Handlers) login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		httpx.WriteError(w, r, h.log, err)
		return
	}
	token, err := domain.NewSessionToken()
	if err != nil {
		httpx.WriteError(w, r, h.log, err)
		return
	}
	if err := h.app.Commands.Login.Handle(r.Context(), command.Login{Email: req.Email, Password: req.Password, Token: token}); err != nil {
		httpx.WriteError(w, r, h.log, err)
		return
	}
	p, err := h.app.Queries.Authenticate.Handle(r.Context(), query.Authenticate{Token: token})
	if err != nil {
		httpx.WriteError(w, r, h.log, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, http.StatusOK, loginResponse{Token: token, TokenType: "Bearer", ExpiresAt: p.ExpiresAt})
}

func (h *Handlers) logout(w http.ResponseWriter, r *http.Request) {
	if err := h.app.Commands.Logout.Handle(r.Context(), command.Logout{Token: bearer(r)}); err != nil {
		httpx.WriteError(w, r, h.log, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) me(w http.ResponseWriter, r *http.Request) {
	p, _ := PrincipalFrom(r.Context())
	httpx.WriteJSON(w, http.StatusOK, p)
}
