// Package sqlitedb opens the SQLite database, applies migrations and offers
// small helpers shared by every module's persistence adapters.
package sqlitedb

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"time"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Open opens (creating if needed) the database at path with the pragmas the
// application relies on:
//   - foreign_keys: referential integrity is enforced by the database;
//   - WAL: readers never block the single writer, so the public, admin and
//     worker components (in one process or several) can share the file;
//   - busy_timeout: writers queue instead of failing with SQLITE_BUSY;
//   - _txlock=immediate: every transaction takes the write lock up front, which
//     removes deadlock-prone read->write lock upgrades.
func Open(path string) (*sql.DB, error) {
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create database directory: %w", err)
		}
	}
	q := url.Values{}
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "synchronous(NORMAL)")
	q.Set("_txlock", "immediate")
	db, err := sql.Open("sqlite", "file:"+path+"?"+q.Encode())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(8)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return db, nil
}

// Migrate applies the embedded migrations that have not run yet, in file-name
// order. File names are prefixed by the owning module, e.g. 0003_survey.sql.
func Migrate(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    TEXT PRIMARY KEY,
		applied_at TEXT NOT NULL
	)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)

	for _, name := range names {
		body, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			return err
		}
		err = WithTx(ctx, db, func(tx *sql.Tx) error {
			var n int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version = ?`, name).Scan(&n); err != nil {
				return err
			}
			if n > 0 {
				return nil
			}
			if _, err := tx.ExecContext(ctx, string(body)); err != nil {
				return err
			}
			_, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`, name, FormatTime(time.Now()))
			return err
		})
		if err != nil {
			return fmt.Errorf("migration %s: %w", name, err)
		}
	}
	return nil
}

// WithTx runs fn inside a transaction, committing on success and rolling back
// on error or panic.
func WithTx(ctx context.Context, db *sql.DB, fn func(tx *sql.Tx) error) (err error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
	}()
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// WithReadTx runs fn in a read-only (deferred) transaction, giving it a
// consistent snapshot across several SELECTs without taking the write lock.
func WithReadTx(ctx context.Context, db *sql.DB, fn func(tx *sql.Tx) error) error {
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return fmt.Errorf("begin read transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	return fn(tx)
}

// IsUniqueViolation reports whether err is a UNIQUE or PRIMARY KEY violation.
func IsUniqueViolation(err error) bool {
	var se *sqlite.Error
	if errors.As(err, &se) {
		c := se.Code()
		return c == sqlite3.SQLITE_CONSTRAINT_UNIQUE || c == sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY
	}
	return false
}

// IsTriggerAbort reports whether err was raised by a RAISE(ABORT, ...) trigger.
func IsTriggerAbort(err error) bool {
	var se *sqlite.Error
	return errors.As(err, &se) && se.Code() == sqlite3.SQLITE_CONSTRAINT_TRIGGER
}

const timeLayout = time.RFC3339Nano

// FormatTime stores times as sortable UTC RFC 3339 text.
func FormatTime(t time.Time) string { return t.UTC().Format(timeLayout) }

// FormatNullTime maps a nil time to SQL NULL.
func FormatNullTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return FormatTime(*t)
}

func ParseTime(s string) (time.Time, error) { return time.Parse(timeLayout, s) }

// ParseNullTime parses a nullable time column.
func ParseNullTime(s sql.NullString) (*time.Time, error) {
	if !s.Valid {
		return nil, nil
	}
	t, err := ParseTime(s.String)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// Placeholders returns "?, ?, ?" for n parameters.
func Placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	b := make([]byte, 0, n*3)
	for i := 0; i < n; i++ {
		if i > 0 {
			b = append(b, ", "...)
		}
		b = append(b, '?')
	}
	return string(b)
}
