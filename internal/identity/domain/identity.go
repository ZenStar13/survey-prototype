// Package domain is the admin identity model: administrators and their
// bearer sessions.
package domain

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/zenstar13/survey-prototype/internal/kernel/errs"
)

const (
	MinPasswordLength = 12
	MaxPasswordBytes  = 72 // bcrypt limit
)

var (
	ErrInvalidCredentials = errs.Unauthorized("invalid_credentials", "invalid email or password")
	ErrUnauthenticated    = errs.Unauthorized("unauthenticated", "a valid bearer token is required")
	ErrAdminExists        = errs.Conflict("admin_already_exists", "an administrator with this email already exists")
)

type Admin struct {
	id           string
	email        string
	passwordHash string
	createdAt    time.Time
}

// PasswordHasher is the port for a slow password hash (bcrypt in production).
type PasswordHasher interface {
	Hash(plain string) (string, error)
	Matches(hash, plain string) bool
}

func NewAdmin(id, email, password string, hasher PasswordHasher, now time.Time) (*Admin, error) {
	var v errs.Violations
	email = NormalizeEmail(email)
	if email == "" || !strings.Contains(email, "@") {
		v.Add("email", "is not a valid email address")
	}
	if len(password) < MinPasswordLength {
		v.Add("password", "must be at least 12 characters")
	} else if len(password) > MaxPasswordBytes {
		v.Add("password", "must be at most 72 bytes")
	}
	if err := v.Err(); err != nil {
		return nil, err
	}
	hash, err := hasher.Hash(password)
	if err != nil {
		return nil, err
	}
	return &Admin{id: id, email: email, passwordHash: hash, createdAt: now}, nil
}

func ReconstituteAdmin(id, email, hash string, createdAt time.Time) *Admin {
	return &Admin{id: id, email: email, passwordHash: hash, createdAt: createdAt}
}

func (a *Admin) ID() string           { return a.id }
func (a *Admin) Email() string        { return a.email }
func (a *Admin) PasswordHash() string { return a.passwordHash }
func (a *Admin) CreatedAt() time.Time { return a.createdAt }

func (a *Admin) Authenticate(password string, hasher PasswordHasher) error {
	if !hasher.Matches(a.passwordHash, password) {
		return ErrInvalidCredentials
	}
	return nil
}

func NormalizeEmail(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// Session is an opaque bearer token session. Only the token's hash is kept.
type Session struct {
	TokenHash string
	AdminID   string
	CreatedAt time.Time
	ExpiresAt time.Time
}

func NewSession(token, adminID string, now time.Time, ttl time.Duration) Session {
	return Session{TokenHash: HashToken(token), AdminID: adminID, CreatedAt: now, ExpiresAt: now.Add(ttl)}
}

// NewSessionToken returns 256 bits of randomness, URL-safe encoded.
func NewSessionToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", errors.Join(errors.New("generate session token"), err)
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

// HashToken is SHA-256: tokens are high-entropy, so a fast hash suffices.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

type AdminRepository interface {
	Add(ctx context.Context, a *Admin) error                   // ErrAdminExists on duplicate email
	ByEmail(ctx context.Context, email string) (*Admin, error) // ErrInvalidCredentials if absent
}

type SessionRepository interface {
	Add(ctx context.Context, s Session) error
	Delete(ctx context.Context, tokenHash string) error
}
