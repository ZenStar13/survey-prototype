package query

import (
	"context"
	"time"
)

type RelationView struct {
	ID          string    `json:"id"`
	SurveyID    string    `json:"survey_id"`
	Name        string    `json:"name"`
	QuestionIDs []string  `json:"question_ids"`
	CreatedAt   time.Time `json:"created_at"`
}

type RelationReadModel interface {
	RelationsOf(ctx context.Context, surveyID string) ([]RelationView, error)
}

type ListRelations struct{ SurveyID string }

type ListRelationsHandler struct {
	catalog Catalog
	rm      RelationReadModel
}

func NewListRelationsHandler(c Catalog, rm RelationReadModel) ListRelationsHandler {
	return ListRelationsHandler{catalog: c, rm: rm}
}

func (h ListRelationsHandler) Handle(ctx context.Context, q ListRelations) ([]RelationView, error) {
	if _, err := h.catalog.Describe(ctx, q.SurveyID, false); err != nil {
		return nil, err
	}
	return h.rm.RelationsOf(ctx, q.SurveyID)
}
