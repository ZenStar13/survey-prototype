package adapters

import (
	"context"
	"database/sql"
	"errors"

	"github.com/zenstar13/survey-prototype/internal/analytics/app/query"
	"github.com/zenstar13/survey-prototype/internal/platform/sqlitedb"
)

// StatsReadModel reads the projections built by the Projector.
type StatsReadModel struct{ db *sql.DB }

func NewStatsReadModel(db *sql.DB) *StatsReadModel { return &StatsReadModel{db: db} }

var _ query.ReadModel = (*StatsReadModel)(nil)

func (m *StatsReadModel) Stats(ctx context.Context, surveyID string) (query.RawStats, error) {
	raw := query.RawStats{Selections: map[string]map[string]int{}, Answered: map[string]int{}}
	err := sqlitedb.WithReadTx(ctx, m.db, func(tx *sql.Tx) error {
		var dataV, computedV int
		var computedAt sql.NullString
		err := tx.QueryRowContext(ctx, `
			SELECT data_version, computed_version, computed_at, total_responses
			FROM analytics_survey_state WHERE survey_id = ?`, surveyID).
			Scan(&dataV, &computedV, &computedAt, &raw.TotalResponses)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			return nil // never seen by the worker: empty report
		case err != nil:
			return err
		}
		raw.Known = true
		raw.Stale = dataV > computedV
		if raw.ComputedAt, err = sqlitedb.ParseNullTime(computedAt); err != nil {
			return err
		}

		if err := scanRows(ctx, tx, `SELECT question_id, option_id, selections FROM analytics_question_stats WHERE survey_id = ?`,
			[]any{surveyID}, func(r *sql.Rows) error {
				var q, o string
				var n int
				if err := r.Scan(&q, &o, &n); err != nil {
					return err
				}
				if raw.Selections[q] == nil {
					raw.Selections[q] = map[string]int{}
				}
				raw.Selections[q][o] = n
				return nil
			}); err != nil {
			return err
		}
		if err := scanRows(ctx, tx, `SELECT question_id, answered FROM analytics_question_totals WHERE survey_id = ?`,
			[]any{surveyID}, func(r *sql.Rows) error {
				var q string
				var n int
				if err := r.Scan(&q, &n); err != nil {
					return err
				}
				raw.Answered[q] = n
				return nil
			}); err != nil {
			return err
		}

		rels, err := relationsOf(ctx, tx, surveyID)
		if err != nil {
			return err
		}
		index := map[string]int{}
		for _, rel := range rels {
			index[rel.ID()] = len(raw.Relations)
			raw.Relations = append(raw.Relations, query.RawRelation{
				ID: rel.ID(), Name: rel.Name(), QuestionIDs: rel.QuestionIDs(), CreatedAt: rel.CreatedAt(),
				Segments: map[string]int{}, Cells: map[string]int{},
			})
		}
		if err := scanRows(ctx, tx, `
			SELECT t.relation_id, t.respondents FROM analytics_relation_totals t
			JOIN analytics_relations r ON r.id = t.relation_id WHERE r.survey_id = ?`,
			[]any{surveyID}, func(r *sql.Rows) error {
				var id string
				var n int
				if err := r.Scan(&id, &n); err != nil {
					return err
				}
				if i, ok := index[id]; ok {
					raw.Relations[i].Computed = true
					raw.Relations[i].Respondents = n
				}
				return nil
			}); err != nil {
			return err
		}
		if err := scanRows(ctx, tx, `
			SELECT s.relation_id, s.option_id, s.respondents FROM analytics_relation_segments s
			JOIN analytics_relations r ON r.id = s.relation_id WHERE r.survey_id = ?`,
			[]any{surveyID}, func(r *sql.Rows) error {
				var id, o string
				var n int
				if err := r.Scan(&id, &o, &n); err != nil {
					return err
				}
				if i, ok := index[id]; ok {
					raw.Relations[i].Segments[o] = n
				}
				return nil
			}); err != nil {
			return err
		}
		return scanRows(ctx, tx, `
			SELECT c.relation_id, c.combination, c.respondents FROM analytics_relation_cells c
			JOIN analytics_relations r ON r.id = c.relation_id WHERE r.survey_id = ?`,
			[]any{surveyID}, func(r *sql.Rows) error {
				var id, combo string
				var n int
				if err := r.Scan(&id, &combo, &n); err != nil {
					return err
				}
				if i, ok := index[id]; ok {
					raw.Relations[i].Cells[combo] = n
				}
				return nil
			})
	})
	return raw, err
}

func (m *StatsReadModel) StaleSurveys(ctx context.Context, limit int) ([]string, error) {
	rows, err := m.db.QueryContext(ctx, `
		SELECT survey_id FROM analytics_survey_state WHERE data_version > computed_version ORDER BY survey_id LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func scanRows(ctx context.Context, tx *sql.Tx, q string, args []any, each func(*sql.Rows) error) error {
	rows, err := tx.QueryContext(ctx, q, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if err := each(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}
