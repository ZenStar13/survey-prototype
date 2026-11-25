// Package adapters contains the survey module's driven adapters (SQLite).
package adapters

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/zenstar13/survey-prototype/internal/contracts"
	"github.com/zenstar13/survey-prototype/internal/kernel/errs"
	"github.com/zenstar13/survey-prototype/internal/platform/outbox"
	"github.com/zenstar13/survey-prototype/internal/platform/sqlitedb"
	"github.com/zenstar13/survey-prototype/internal/survey/domain"
)

var errConcurrentModification = errs.Conflict("concurrent_modification", "the survey was modified concurrently, retry")

// SQLiteRepository implements domain.Repository.
type SQLiteRepository struct{ db *sql.DB }

func NewSQLiteRepository(db *sql.DB) *SQLiteRepository { return &SQLiteRepository{db: db} }

var _ domain.Repository = (*SQLiteRepository)(nil)

func (r *SQLiteRepository) Add(ctx context.Context, s *domain.Survey) error {
	return sqlitedb.WithTx(ctx, r.db, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO surveys (id, title, description, status, version, created_at, updated_at, published_at, closed_at)
			VALUES (?, ?, ?, ?, 1, ?, ?, ?, ?)`,
			s.ID(), s.Title(), s.Description(), string(s.Status()),
			sqlitedb.FormatTime(s.CreatedAt()), sqlitedb.FormatTime(s.UpdatedAt()),
			sqlitedb.FormatNullTime(s.PublishedAt()), sqlitedb.FormatNullTime(s.ClosedAt()))
		if sqlitedb.IsUniqueViolation(err) {
			return errs.Conflict("survey_already_exists", "a survey with this id already exists")
		}
		if err != nil {
			return err
		}
		if err := insertQuestions(ctx, tx, s); err != nil {
			return err
		}
		return publishEvents(ctx, tx, s.PullEvents())
	})
}

func (r *SQLiteRepository) Update(ctx context.Context, id string, mutate func(s *domain.Survey) error) error {
	return sqlitedb.WithTx(ctx, r.db, func(tx *sql.Tx) error {
		s, err := load(ctx, tx, id)
		if err != nil {
			return err
		}
		wasDraft := s.Status() == domain.StatusDraft
		if err := mutate(s); err != nil {
			return err
		}
		// Content can only change while the survey is (and stays) a draft; the
		// aggregate guarantees it and the DB triggers would reject it otherwise.
		if wasDraft && s.Status() == domain.StatusDraft {
			if _, err := tx.ExecContext(ctx, `DELETE FROM survey_questions WHERE survey_id = ?`, id); err != nil {
				return err
			}
			if err := insertQuestions(ctx, tx, s); err != nil {
				return err
			}
		}
		res, err := tx.ExecContext(ctx, `
			UPDATE surveys
			SET title = ?, description = ?, status = ?, version = version + 1,
			    updated_at = ?, published_at = ?, closed_at = ?
			WHERE id = ? AND version = ?`,
			s.Title(), s.Description(), string(s.Status()), sqlitedb.FormatTime(s.UpdatedAt()),
			sqlitedb.FormatNullTime(s.PublishedAt()), sqlitedb.FormatNullTime(s.ClosedAt()),
			id, s.Version())
		if sqlitedb.IsTriggerAbort(err) {
			return domain.ErrNotEditable
		}
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return errConcurrentModification
		}
		return publishEvents(ctx, tx, s.PullEvents())
	})
}

func insertQuestions(ctx context.Context, tx *sql.Tx, s *domain.Survey) error {
	for qi, q := range s.Questions() {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO survey_questions (id, survey_id, position, prompt, type, required) VALUES (?, ?, ?, ?, ?, ?)`,
			q.ID(), s.ID(), qi+1, q.Prompt(), string(q.Type()), q.Required()); err != nil {
			if sqlitedb.IsTriggerAbort(err) {
				return domain.ErrNotEditable
			}
			return err
		}
		for oi, o := range q.Options() {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO survey_question_options (id, question_id, position, label) VALUES (?, ?, ?, ?)`,
				o.ID(), q.ID(), oi+1, o.Label()); err != nil {
				return err
			}
		}
	}
	return nil
}

func load(ctx context.Context, tx *sql.Tx, id string) (*domain.Survey, error) {
	var sn domain.Snapshot
	var status, created, updated string
	var published, closed sql.NullString
	err := tx.QueryRowContext(ctx, `
		SELECT id, title, description, status, version, created_at, updated_at, published_at, closed_at
		FROM surveys WHERE id = ?`, id).
		Scan(&sn.ID, &sn.Title, &sn.Description, &status, &sn.Version, &created, &updated, &published, &closed)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	sn.Status = domain.Status(status)
	if sn.CreatedAt, err = sqlitedb.ParseTime(created); err != nil {
		return nil, err
	}
	if sn.UpdatedAt, err = sqlitedb.ParseTime(updated); err != nil {
		return nil, err
	}
	if sn.PublishedAt, err = sqlitedb.ParseNullTime(published); err != nil {
		return nil, err
	}
	if sn.ClosedAt, err = sqlitedb.ParseNullTime(closed); err != nil {
		return nil, err
	}

	rows, err := tx.QueryContext(ctx, `
		SELECT q.id, q.prompt, q.type, q.required, o.id, o.label
		FROM survey_questions q
		JOIN survey_question_options o ON o.question_id = q.id
		WHERE q.survey_id = ?
		ORDER BY q.position, o.position`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var qid, prompt, qtype, oid, label string
		var required bool
		if err := rows.Scan(&qid, &prompt, &qtype, &required, &oid, &label); err != nil {
			return nil, err
		}
		if n := len(sn.Questions); n == 0 || sn.Questions[n-1].ID != qid {
			sn.Questions = append(sn.Questions, domain.QuestionSpec{ID: qid, Prompt: prompt, Type: domain.QuestionType(qtype), Required: required})
		}
		last := &sn.Questions[len(sn.Questions)-1]
		last.Options = append(last.Options, domain.OptionSpec{ID: oid, Label: label})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return domain.Reconstitute(sn), nil
}

// publishEvents translates domain events into integration events (the
// published language in package contracts) and appends them to the outbox.
func publishEvents(ctx context.Context, tx *sql.Tx, events []domain.Event) error {
	for _, ev := range events {
		var err error
		switch e := ev.(type) {
		case domain.Published:
			err = outbox.Append(ctx, tx, contracts.TopicSurveyPublished, contracts.SurveyPublished{SurveyID: e.SurveyID, PublishedAt: e.At}, e.At)
		case domain.Closed:
			err = outbox.Append(ctx, tx, contracts.TopicSurveyClosed, contracts.SurveyClosed{SurveyID: e.SurveyID, ClosedAt: e.At}, e.At)
		default:
			err = fmt.Errorf("survey: unmapped domain event %T", ev)
		}
		if err != nil {
			return err
		}
	}
	return nil
}
