package adapters

import (
	"context"
	"database/sql"

	"github.com/zenstar13/survey-prototype/internal/analytics/app/command"
	"github.com/zenstar13/survey-prototype/internal/platform/sqlitedb"
)

// FactStore persists analytics' own copy of answers.
type FactStore struct{ db *sql.DB }

func NewFactStore(db *sql.DB) *FactStore { return &FactStore{db: db} }

var _ command.FactStore = (*FactStore)(nil)

const bumpVersionSQL = `
	INSERT INTO analytics_survey_state (survey_id, data_version) VALUES (?, 1)
	ON CONFLICT (survey_id) DO UPDATE SET data_version = data_version + 1`

func (s *FactStore) RecordResponse(ctx context.Context, f command.ResponseFacts) error {
	return sqlitedb.WithTx(ctx, s.db, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `
			INSERT INTO analytics_respondents (response_id, survey_id, submitted_at) VALUES (?, ?, ?)
			ON CONFLICT (response_id) DO NOTHING`,
			f.ResponseID, f.SurveyID, sqlitedb.FormatTime(f.SubmittedAt))
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil // redelivery: already recorded
		}
		for _, a := range f.Answers {
			for _, o := range a.OptionIDs {
				if _, err := tx.ExecContext(ctx, `
					INSERT INTO analytics_answer_facts (response_id, survey_id, question_id, option_id) VALUES (?, ?, ?, ?)`,
					f.ResponseID, f.SurveyID, a.QuestionID, o); err != nil {
					return err
				}
			}
		}
		_, err = tx.ExecContext(ctx, bumpVersionSQL, f.SurveyID)
		return err
	})
}

func (s *FactStore) MarkStale(ctx context.Context, surveyID string) error {
	_, err := s.db.ExecContext(ctx, bumpVersionSQL, surveyID)
	return err
}
