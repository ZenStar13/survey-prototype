// Package adapters contains the analytics module's driven adapters.
package adapters

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/zenstar13/survey-prototype/internal/analytics/app/query"
	"github.com/zenstar13/survey-prototype/internal/analytics/domain"
	"github.com/zenstar13/survey-prototype/internal/contracts"
	"github.com/zenstar13/survey-prototype/internal/platform/outbox"
	"github.com/zenstar13/survey-prototype/internal/platform/sqlitedb"
)

type RelationRepository struct{ db *sql.DB }

func NewRelationRepository(db *sql.DB) *RelationRepository { return &RelationRepository{db: db} }

var (
	_ domain.RelationRepository = (*RelationRepository)(nil)
	_ query.RelationReadModel   = (*RelationRepository)(nil)
)

func (r *RelationRepository) Add(ctx context.Context, rel *domain.Relation) error {
	return sqlitedb.WithTx(ctx, r.db, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO analytics_relations (id, survey_id, name, signature, created_at) VALUES (?, ?, ?, ?, ?)`,
			rel.ID(), rel.SurveyID(), rel.Name(), rel.Signature(), sqlitedb.FormatTime(rel.CreatedAt()))
		if sqlitedb.IsUniqueViolation(err) {
			return domain.ErrDuplicateRelation
		}
		if err != nil {
			return err
		}
		for i, q := range rel.QuestionIDs() {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO analytics_relation_questions (relation_id, position, question_id) VALUES (?, ?, ?)`,
				rel.ID(), i+1, q); err != nil {
				return err
			}
		}
		return publish(ctx, tx, rel.PullEvents())
	})
}

func (r *RelationRepository) Remove(ctx context.Context, surveyID, relationID string, prepare func(*domain.Relation) error) error {
	return sqlitedb.WithTx(ctx, r.db, func(tx *sql.Tx) error {
		rel, err := loadRelation(ctx, tx, surveyID, relationID)
		if err != nil {
			return err
		}
		if err := prepare(rel); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM analytics_relations WHERE id = ?`, relationID); err != nil {
			return err
		}
		return publish(ctx, tx, rel.PullEvents())
	})
}

func (r *RelationRepository) RelationsOf(ctx context.Context, surveyID string) ([]query.RelationView, error) {
	out := []query.RelationView{}
	err := sqlitedb.WithReadTx(ctx, r.db, func(tx *sql.Tx) error {
		rels, err := relationsOf(ctx, tx, surveyID)
		if err != nil {
			return err
		}
		for _, rel := range rels {
			out = append(out, query.RelationView{
				ID: rel.ID(), SurveyID: rel.SurveyID(), Name: rel.Name(), QuestionIDs: rel.QuestionIDs(), CreatedAt: rel.CreatedAt(),
			})
		}
		return nil
	})
	return out, err
}

type querier interface {
	QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row
}

func loadRelation(ctx context.Context, q querier, surveyID, relationID string) (*domain.Relation, error) {
	var name, created string
	err := q.QueryRowContext(ctx, `SELECT name, created_at FROM analytics_relations WHERE id = ? AND survey_id = ?`,
		relationID, surveyID).Scan(&name, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrRelationNotFound
	}
	if err != nil {
		return nil, err
	}
	createdAt, err := sqlitedb.ParseTime(created)
	if err != nil {
		return nil, err
	}
	rows, err := q.QueryContext(ctx, `SELECT question_id FROM analytics_relation_questions WHERE relation_id = ? ORDER BY position`, relationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var qids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		qids = append(qids, id)
	}
	return domain.ReconstituteRelation(relationID, surveyID, name, qids, createdAt), rows.Err()
}

// relationsOf loads every relation of a survey, oldest first.
func relationsOf(ctx context.Context, q querier, surveyID string) ([]*domain.Relation, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT r.id, r.name, r.created_at, rq.question_id
		FROM analytics_relations r
		JOIN analytics_relation_questions rq ON rq.relation_id = r.id
		WHERE r.survey_id = ?
		ORDER BY r.created_at, r.id, rq.position`, surveyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type acc struct {
		id, name string
		created  string
		qids     []string
	}
	var list []*acc
	for rows.Next() {
		var id, name, created, qid string
		if err := rows.Scan(&id, &name, &created, &qid); err != nil {
			return nil, err
		}
		if n := len(list); n == 0 || list[n-1].id != id {
			list = append(list, &acc{id: id, name: name, created: created})
		}
		list[len(list)-1].qids = append(list[len(list)-1].qids, qid)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]*domain.Relation, 0, len(list))
	for _, a := range list {
		t, err := sqlitedb.ParseTime(a.created)
		if err != nil {
			return nil, err
		}
		out = append(out, domain.ReconstituteRelation(a.id, surveyID, a.name, a.qids, t))
	}
	return out, nil
}

func publish(ctx context.Context, tx *sql.Tx, events []domain.Event) error {
	for _, ev := range events {
		var err error
		switch e := ev.(type) {
		case domain.RelationDefined:
			err = outbox.Append(ctx, tx, contracts.TopicRelationDefined, contracts.RelationChanged{RelationID: e.RelationID, SurveyID: e.SurveyID, At: e.At}, e.At)
		case domain.RelationRemoved:
			err = outbox.Append(ctx, tx, contracts.TopicRelationRemoved, contracts.RelationChanged{RelationID: e.RelationID, SurveyID: e.SurveyID, At: e.At}, e.At)
		default:
			err = fmt.Errorf("analytics: unmapped domain event %T", ev)
		}
		if err != nil {
			return err
		}
	}
	return nil
}
