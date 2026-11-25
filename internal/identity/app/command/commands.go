// Package command holds the identity module's write-side use cases.
package command

import (
	"context"
	"errors"
	"time"

	"github.com/zenstar13/survey-prototype/internal/identity/domain"
	"github.com/zenstar13/survey-prototype/internal/platform/clock"
)

// EnsureAdmin creates the administrator unless one with that email exists
// (used to bootstrap the first admin from configuration).
type EnsureAdmin struct {
	AdminID  string
	Email    string
	Password string
}

// Login verifies credentials and opens a session for the caller-chosen token
// (the caller generates it so the command returns nothing, per CQRS).
type Login struct {
	Email    string
	Password string
	Token    string
}

type Logout struct{ Token string }

type EnsureAdminHandler struct {
	admins domain.AdminRepository
	hasher domain.PasswordHasher
	clock  clock.Clock
}

func NewEnsureAdminHandler(a domain.AdminRepository, h domain.PasswordHasher, clk clock.Clock) EnsureAdminHandler {
	return EnsureAdminHandler{admins: a, hasher: h, clock: clk}
}

func (h EnsureAdminHandler) Handle(ctx context.Context, cmd EnsureAdmin) error {
	admin, err := domain.NewAdmin(cmd.AdminID, cmd.Email, cmd.Password, h.hasher, h.clock.Now())
	if err != nil {
		return err
	}
	if err := h.admins.Add(ctx, admin); err != nil && !errors.Is(err, domain.ErrAdminExists) {
		return err
	}
	return nil
}

type LoginHandler struct {
	admins    domain.AdminRepository
	sessions  domain.SessionRepository
	hasher    domain.PasswordHasher
	clock     clock.Clock
	ttl       time.Duration
	dummyHash string
}

func NewLoginHandler(a domain.AdminRepository, s domain.SessionRepository, h domain.PasswordHasher, clk clock.Clock, ttl time.Duration) (LoginHandler, error) {
	dummy, err := h.Hash("timing-equaliser-not-a-password")
	if err != nil {
		return LoginHandler{}, err
	}
	return LoginHandler{admins: a, sessions: s, hasher: h, clock: clk, ttl: ttl, dummyHash: dummy}, nil
}

func (h LoginHandler) Handle(ctx context.Context, cmd Login) error {
	admin, err := h.admins.ByEmail(ctx, domain.NormalizeEmail(cmd.Email))
	if errors.Is(err, domain.ErrInvalidCredentials) {
		// Spend the same time as a real check so response timing does not
		// reveal which emails are administrators.
		h.hasher.Matches(h.dummyHash, cmd.Password)
		return domain.ErrInvalidCredentials
	}
	if err != nil {
		return err
	}
	if err := admin.Authenticate(cmd.Password, h.hasher); err != nil {
		return err
	}
	return h.sessions.Add(ctx, domain.NewSession(cmd.Token, admin.ID(), h.clock.Now(), h.ttl))
}

type LogoutHandler struct{ sessions domain.SessionRepository }

func NewLogoutHandler(s domain.SessionRepository) LogoutHandler { return LogoutHandler{sessions: s} }

func (h LogoutHandler) Handle(ctx context.Context, cmd Logout) error {
	return h.sessions.Delete(ctx, domain.HashToken(cmd.Token))
}
