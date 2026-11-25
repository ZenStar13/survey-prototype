// Package adapters contains the identity module's driven adapters.
package adapters

import (
	"context"
	"database/sql"
	"errors"

	"golang.org/x/crypto/bcrypt"

	"github.com/zenstar13/survey-prototype/internal/identity/app/query"
	"github.com/zenstar13/survey-prototype/internal/identity/domain"
	"github.com/zenstar13/survey-prototype/internal/platform/sqlitedb"
)

// BcryptHasher implements domain.PasswordHasher.
type BcryptHasher struct{ Cost int }

func (b BcryptHasher) Hash(plain string) (string, error) {
	cost := b.Cost
	if cost == 0 {
		cost = bcrypt.DefaultCost
	}
	h, err := bcrypt.GenerateFromPassword([]byte(plain), cost)
	return string(h), err
}

func (BcryptHasher) Matches(hash, plain string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain)) == nil
}

type SQLiteStore struct{ db *sql.DB }

func NewSQLiteStore(db *sql.DB) *SQLiteStore { return &SQLiteStore{db: db} }

var (
	_ domain.AdminRepository   = (*SQLiteStore)(nil)
	_ query.SessionReadModel   = (*SQLiteStore)(nil)
	_ domain.SessionRepository = (*SQLiteSessions)(nil)
)

func (s *SQLiteStore) Add(ctx context.Context, a *domain.Admin) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO identity_admins (id, email, password_hash, created_at) VALUES (?, ?, ?, ?)`,
		a.ID(), a.Email(), a.PasswordHash(), sqlitedb.FormatTime(a.CreatedAt()))
	if sqlitedb.IsUniqueViolation(err) {
		return domain.ErrAdminExists
	}
	return err
}

func (s *SQLiteStore) ByEmail(ctx context.Context, email string) (*domain.Admin, error) {
	var id, hash, created string
	err := s.db.QueryRowContext(ctx, `SELECT id, password_hash, created_at FROM identity_admins WHERE email = ?`, email).
		Scan(&id, &hash, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrInvalidCredentials
	}
	if err != nil {
		return nil, err
	}
	t, err := sqlitedb.ParseTime(created)
	if err != nil {
		return nil, err
	}
	return domain.ReconstituteAdmin(id, email, hash, t), nil
}

func (s *SQLiteStore) PrincipalByTokenHash(ctx context.Context, tokenHash string) (query.Principal, error) {
	var p query.Principal
	var expires string
	err := s.db.QueryRowContext(ctx, `
		SELECT a.id, a.email, s.expires_at
		FROM identity_sessions s JOIN identity_admins a ON a.id = s.admin_id
		WHERE s.token_hash = ?`, tokenHash).Scan(&p.AdminID, &p.Email, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return p, domain.ErrUnauthenticated
	}
	if err != nil {
		return p, err
	}
	p.ExpiresAt, err = sqlitedb.ParseTime(expires)
	return p, err
}

// SQLiteSessions implements domain.SessionRepository.
type SQLiteSessions struct{ db *sql.DB }

func NewSQLiteSessions(db *sql.DB) *SQLiteSessions { return &SQLiteSessions{db: db} }

func (s *SQLiteSessions) Add(ctx context.Context, sess domain.Session) error {
	return sqlitedb.WithTx(ctx, s.db, func(tx *sql.Tx) error {
		// Opportunistic cleanup keeps the table small without a separate job.
		if _, err := tx.ExecContext(ctx, `DELETE FROM identity_sessions WHERE expires_at < ?`, sqlitedb.FormatTime(sess.CreatedAt)); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO identity_sessions (token_hash, admin_id, created_at, expires_at) VALUES (?, ?, ?, ?)`,
			sess.TokenHash, sess.AdminID, sqlitedb.FormatTime(sess.CreatedAt), sqlitedb.FormatTime(sess.ExpiresAt))
		return err
	})
}

func (s *SQLiteSessions) Delete(ctx context.Context, tokenHash string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM identity_sessions WHERE token_hash = ?`, tokenHash)
	return err
}
