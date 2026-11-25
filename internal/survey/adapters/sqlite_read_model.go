package adapters

import (
	"context"
	"database/sql"
	"errors"

	"github.com/zenstar13/survey-prototype/internal/platform/sqlitedb"
	"github.com/zenstar13/survey-prototype/internal/survey/app/query"
	"github.com/zenstar13/survey-prototype/internal/survey/domain"
)

// SQLiteReadModel implements query.ReadModel with plain SQL straight into view
// DTOs, bypassing the aggregate (the CQRS read side).
type SQLiteReadModel struct{ db *sql.DB }

func NewSQLiteReadModel(db *sql.DB) *SQLiteReadModel { return &SQLiteReadModel{db: db} }

var _ query.ReadModel = (*SQLiteReadModel)(nil)

func (m *SQLiteReadModel) SurveyByID(ctx context.Context, id string) (query.SurveyView, error) {
	var v query.SurveyView
	var created, updated string
	var published, closed sql.NullString
	err := m.db.QueryRowContext(ctx, `
		SELECT id, title, description, status, created_at, updated_at, published_at, closed_at
		FROM surveys WHERE id = ?`, id).
		Scan(&v.ID, &v.Title, &v.Description, &v.Status, &created, &updated, &published, &closed)
	if errors.Is(err, sql.ErrNoRows) {
		return query.SurveyView{}, domain.ErrNotFound
	}
	if err != nil {
		return query.SurveyView{}, err
	}
	if v.CreatedAt, err = sqlitedb.ParseTime(created); err != nil {
		return v, err
	}
	if v.UpdatedAt, err = sqlitedb.ParseTime(updated); err != nil {
		return v, err
	}
	if v.PublishedAt, err = sqlitedb.ParseNullTime(published); err != nil {
		return v, err
	}
	if v.ClosedAt, err = sqlitedb.ParseNullTime(closed); err != nil {
		return v, err
	}

	rows, err := m.db.QueryContext(ctx, `
		SELECT q.id, q.position, q.prompt, q.type, q.required, o.id, o.label
		FROM survey_questions q
		JOIN survey_question_options o ON o.question_id = q.id
		WHERE q.survey_id = ?
		ORDER BY q.position, o.position`, id)
	if err != nil {
		return v, err
	}
	defer rows.Close()
	v.Questions = []query.QuestionView{}
	for rows.Next() {
		var q query.QuestionView
		var o query.OptionView
		if err := rows.Scan(&q.ID, &q.Number, &q.Prompt, &q.Type, &q.Required, &o.ID, &o.Label); err != nil {
			return v, err
		}
		if n := len(v.Questions); n == 0 || v.Questions[n-1].ID != q.ID {
			v.Questions = append(v.Questions, q)
		}
		last := &v.Questions[len(v.Questions)-1]
		last.Options = append(last.Options, o)
	}
	return v, rows.Err()
}

func (m *SQLiteReadModel) ListSurveys(ctx context.Context, statuses []string) ([]query.SurveySummary, error) {
	q := `
		SELECT s.id, s.title, s.description, s.status, s.created_at, s.published_at,
		       (SELECT COUNT(*) FROM survey_questions q WHERE q.survey_id = s.id)
		FROM surveys s`
	args := make([]any, 0, len(statuses))
	if len(statuses) > 0 {
		q += ` WHERE s.status IN (` + sqlitedb.Placeholders(len(statuses)) + `)`
		for _, s := range statuses {
			args = append(args, s)
		}
	}
	q += ` ORDER BY s.created_at DESC`
	rows, err := m.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []query.SurveySummary{}
	for rows.Next() {
		var s query.SurveySummary
		var created string
		var published sql.NullString
		if err := rows.Scan(&s.ID, &s.Title, &s.Description, &s.Status, &created, &published, &s.QuestionCount); err != nil {
			return nil, err
		}
		if s.CreatedAt, err = sqlitedb.ParseTime(created); err != nil {
			return nil, err
		}
		if s.PublishedAt, err = sqlitedb.ParseNullTime(published); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
