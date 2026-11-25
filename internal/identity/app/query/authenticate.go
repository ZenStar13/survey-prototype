// Package query holds the identity module's read side.
package query

import (
	"context"
	"time"

	"github.com/zenstar13/survey-prototype/internal/identity/domain"
	"github.com/zenstar13/survey-prototype/internal/platform/clock"
)

// Principal is the authenticated administrator.
type Principal struct {
	AdminID   string    `json:"admin_id"`
	Email     string    `json:"email"`
	ExpiresAt time.Time `json:"session_expires_at"`
}

type SessionReadModel interface {
	// PrincipalByTokenHash returns domain.ErrUnauthenticated if no session matches.
	PrincipalByTokenHash(ctx context.Context, tokenHash string) (Principal, error)
}

type Authenticate struct{ Token string }

type AuthenticateHandler struct {
	rm    SessionReadModel
	clock clock.Clock
}

func NewAuthenticateHandler(rm SessionReadModel, clk clock.Clock) AuthenticateHandler {
	return AuthenticateHandler{rm: rm, clock: clk}
}

func (h AuthenticateHandler) Handle(ctx context.Context, q Authenticate) (Principal, error) {
	if q.Token == "" {
		return Principal{}, domain.ErrUnauthenticated
	}
	p, err := h.rm.PrincipalByTokenHash(ctx, domain.HashToken(q.Token))
	if err != nil {
		return Principal{}, err
	}
	if !h.clock.Now().Before(p.ExpiresAt) {
		return Principal{}, domain.ErrUnauthenticated
	}
	return p, nil
}
