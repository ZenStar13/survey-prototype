// Package adapters contains the response module's driven adapters.
package adapters

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/zenstar13/survey-prototype/internal/contracts"
	"github.com/zenstar13/survey-prototype/internal/platform/outbox"
	"github.com/zenstar13/survey-prototype/internal/platform/sqlitedb"
	"github.com/zenstar13/survey-prototype/internal/response/domain"
)

type SQLiteRepository struct{ db *sql.DB }

func NewSQLiteRepository(db *sql.DB) *SQLiteRepository { return &SQLiteRepository{db: db} }

var _ domain.Repository = (*SQLiteRepository)(nil)

func (r *SQLiteRepository) Add(ctx context.Context, resp *domain.Response) error {
	return sqlitedb.WithTx(ctx, r.db, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO responses (id, survey_id, email, submitted_at) VALUES (?, ?, ?, ?)`,
			resp.ID(), resp.SurveyID(), resp.Email().String(), sqlitedb.FormatTime(resp.SubmittedAt()))
		switch {
		case sqlitedb.IsUniqueViolation(err):
			return domain.ErrAlreadyResponded
		case sqlitedb.IsTriggerAbort(err): // survey closed between read and write
			return domain.ErrSurveyNotAccepting
		case err != nil:
			return err
		}
		for _, a := range resp.Answers() {
			for _, opt := range a.OptionIDs() {
				if _, err := tx.ExecContext(ctx, `
					INSERT INTO response_answers (response_id, question_id, option_id) VALUES (?, ?, ?)`,
					resp.ID(), a.QuestionID(), opt); err != nil {
					return err
				}
			}
		}
		for _, ev := range resp.PullEvents() {
			switch e := ev.(type) {
			case domain.Submitted:
				if err := outbox.Append(ctx, tx, contracts.TopicResponseSubmitted, toContract(e), e.At); err != nil {
					return err
				}
			default:
				return fmt.Errorf("response: unmapped domain event %T", ev)
			}
		}
		return nil
	})
}

// toContract maps the domain event to the integration event. The email is
// deliberately not part of it.
func toContract(e domain.Submitted) contracts.ResponseSubmitted {
	answers := make([]contracts.AnswerSelected, 0, len(e.Answers))
	for _, a := range e.Answers {
		answers = append(answers, contracts.AnswerSelected{QuestionID: a.QuestionID(), OptionIDs: a.OptionIDs()})
	}
	return contracts.ResponseSubmitted{ResponseID: e.ResponseID, SurveyID: e.SurveyID, SubmittedAt: e.At, Answers: answers}
}
