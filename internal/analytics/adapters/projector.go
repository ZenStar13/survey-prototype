package adapters

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/zenstar13/survey-prototype/internal/analytics/app/command"
	"github.com/zenstar13/survey-prototype/internal/platform/sqlitedb"
)

// Projector recomputes a survey's statistics from the answer facts with set-
// based SQL and replaces the projection rows, all in one transaction (readers
// always see a complete snapshot).
//
// Full recomputation was chosen over incremental counters because it is
// trivially correct and self-healing: it is idempotent, covers relations that
// are added after responses already exist (backfill), and needs no exactly-once
// delivery. Cost is O(answers of the survey) per rebuild, and rebuilds are
// coalesced: a burst of N responses triggers one rebuild per worker tick, not N.
type Projector struct{ db *sql.DB }

func NewProjector(db *sql.DB) *Projector { return &Projector{db: db} }

var _ command.Projector = (*Projector)(nil)

func (p *Projector) Rebuild(ctx context.Context, surveyID string, at time.Time) error {
	return sqlitedb.WithTx(ctx, p.db, func(tx *sql.Tx) error {
		// The write lock is held from BEGIN (IMMEDIATE), so the version read
		// here is exactly the data this rebuild sees.
		var version int
		err := tx.QueryRowContext(ctx, `SELECT data_version FROM analytics_survey_state WHERE survey_id = ?`, surveyID).Scan(&version)
		if errors.Is(err, sql.ErrNoRows) {
			if _, err := tx.ExecContext(ctx, `INSERT INTO analytics_survey_state (survey_id) VALUES (?)`, surveyID); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}

		var total int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM analytics_respondents WHERE survey_id = ?`, surveyID).Scan(&total); err != nil {
			return err
		}

		stmts := []struct {
			sql  string
			args []any
		}{
			{`DELETE FROM analytics_question_stats WHERE survey_id = ?`, []any{surveyID}},
			{`INSERT INTO analytics_question_stats (survey_id, question_id, option_id, selections)
			  SELECT ?, question_id, option_id, COUNT(*)
			  FROM analytics_answer_facts WHERE survey_id = ?
			  GROUP BY question_id, option_id`, []any{surveyID, surveyID}},
			{`DELETE FROM analytics_question_totals WHERE survey_id = ?`, []any{surveyID}},
			{`INSERT INTO analytics_question_totals (survey_id, question_id, answered)
			  SELECT ?, question_id, COUNT(DISTINCT response_id)
			  FROM analytics_answer_facts WHERE survey_id = ?
			  GROUP BY question_id`, []any{surveyID, surveyID}},
		}
		for _, s := range stmts {
			if _, err := tx.ExecContext(ctx, s.sql, s.args...); err != nil {
				return err
			}
		}

		rels, err := relationsOf(ctx, tx, surveyID)
		if err != nil {
			return err
		}
		for _, rel := range rels {
			if err := rebuildRelation(ctx, tx, surveyID, rel.ID(), rel.QuestionIDs()); err != nil {
				return fmt.Errorf("relation %s: %w", rel.ID(), err)
			}
		}

		_, err = tx.ExecContext(ctx, `
			UPDATE analytics_survey_state SET computed_version = ?, computed_at = ?, total_responses = ? WHERE survey_id = ?`,
			version, sqlitedb.FormatTime(at), total, surveyID)
		return err
	})
}

// rebuildRelation computes the n-way contingency table of a relation. For
// questions q0..qn-1 it joins the facts once per question:
//
//	FROM analytics_respondents r
//	JOIN analytics_answer_facts f0 ON f0.response_id = r.response_id AND f0.question_id = ?
//	JOIN analytics_answer_facts f1 ON f1.response_id = r.response_id AND f1.question_id = ?
//
// Inner joins drop respondents who skipped any related question. A respondent
// who picked several options of a multiple-choice question lands in several
// cells, which is the standard cross-tab semantics.
func rebuildRelation(ctx context.Context, tx *sql.Tx, surveyID, relationID string, questionIDs []string) error {
	var from strings.Builder
	from.WriteString(" FROM analytics_respondents r")
	joinArgs := make([]any, 0, len(questionIDs)+1)
	combo := make([]string, 0, len(questionIDs))
	for i, q := range questionIDs {
		// Only the loop index is interpolated; every value is a bound parameter.
		fmt.Fprintf(&from, " JOIN analytics_answer_facts f%d ON f%d.response_id = r.response_id AND f%d.question_id = ?", i, i, i)
		joinArgs = append(joinArgs, q)
		combo = append(combo, fmt.Sprintf("f%d.option_id", i))
	}
	from.WriteString(" WHERE r.survey_id = ?")
	joinArgs = append(joinArgs, surveyID)
	comboExpr := strings.Join(combo, " || '|' || ")
	withRel := func() []any { return append([]any{relationID}, joinArgs...) }

	for _, table := range []string{"analytics_relation_cells", "analytics_relation_segments", "analytics_relation_totals"} {
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE relation_id = ?`, relationID); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO analytics_relation_totals (relation_id, respondents) SELECT ?, COUNT(DISTINCT r.response_id)`+from.String(),
		withRel()...); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO analytics_relation_segments (relation_id, option_id, respondents) SELECT ?, f0.option_id, COUNT(DISTINCT r.response_id)`+
			from.String()+` GROUP BY f0.option_id`,
		withRel()...); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx,
		`INSERT INTO analytics_relation_cells (relation_id, combination, respondents) SELECT ?, `+comboExpr+`, COUNT(*)`+
			from.String()+` GROUP BY `+comboExpr,
		withRel()...)
	return err
}
