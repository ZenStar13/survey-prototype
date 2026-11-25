// Package command holds the analytics module's write-side use cases: managing
// relations, ingesting answers, and (re)computing statistics.
package command

import (
	"context"
	"time"

	"github.com/zenstar13/survey-prototype/internal/analytics/domain"
	"github.com/zenstar13/survey-prototype/internal/platform/clock"
)

// --- ports -------------------------------------------------------------------

// SurveyCatalog gives analytics its view of a survey (anti-corruption port).
type SurveyCatalog interface {
	Survey(ctx context.Context, surveyID string) (domain.Survey, error) // domain.ErrSurveyNotFound
}

// FactStore holds analytics' own copy of submitted answers.
type FactStore interface {
	// RecordResponse must be idempotent: the outbox delivers at least once.
	// It also marks the survey's statistics as stale.
	RecordResponse(ctx context.Context, f ResponseFacts) error
	MarkStale(ctx context.Context, surveyID string) error
}

// Projector rebuilds a survey's statistics projections from the facts.
type Projector interface {
	Rebuild(ctx context.Context, surveyID string, at time.Time) error
}

type ResponseFacts struct {
	ResponseID  string
	SurveyID    string
	SubmittedAt time.Time
	Answers     []FactAnswer
}

type FactAnswer struct {
	QuestionID string
	OptionIDs  []string
}

// --- commands ----------------------------------------------------------------

type DefineRelation struct {
	RelationID  string
	SurveyID    string
	Name        string
	QuestionIDs []string
}

type RemoveRelation struct {
	SurveyID   string
	RelationID string
}

// RecordResponse ingests a response.submitted integration event.
type RecordResponse struct{ Facts ResponseFacts }

// MarkStatsStale requests a recomputation (e.g. after a relation changed, a
// survey was published, or an admin asked for a refresh).
type MarkStatsStale struct{ SurveyID string }

// RefreshStats recomputes all statistics of a survey. Run by the worker.
type RefreshStats struct{ SurveyID string }

// --- handlers ----------------------------------------------------------------

type DefineRelationHandler struct {
	catalog SurveyCatalog
	repo    domain.RelationRepository
	clock   clock.Clock
}

func NewDefineRelationHandler(c SurveyCatalog, repo domain.RelationRepository, clk clock.Clock) DefineRelationHandler {
	return DefineRelationHandler{catalog: c, repo: repo, clock: clk}
}

func (h DefineRelationHandler) Handle(ctx context.Context, cmd DefineRelation) error {
	s, err := h.catalog.Survey(ctx, cmd.SurveyID)
	if err != nil {
		return err
	}
	r, err := domain.DefineRelation(cmd.RelationID, s, cmd.Name, cmd.QuestionIDs, h.clock.Now())
	if err != nil {
		return err
	}
	return h.repo.Add(ctx, r)
}

type RemoveRelationHandler struct {
	repo  domain.RelationRepository
	clock clock.Clock
}

func NewRemoveRelationHandler(repo domain.RelationRepository, clk clock.Clock) RemoveRelationHandler {
	return RemoveRelationHandler{repo: repo, clock: clk}
}

func (h RemoveRelationHandler) Handle(ctx context.Context, cmd RemoveRelation) error {
	return h.repo.Remove(ctx, cmd.SurveyID, cmd.RelationID, func(r *domain.Relation) error {
		r.Remove(h.clock.Now())
		return nil
	})
}

type RecordResponseHandler struct{ facts FactStore }

func NewRecordResponseHandler(f FactStore) RecordResponseHandler {
	return RecordResponseHandler{facts: f}
}

func (h RecordResponseHandler) Handle(ctx context.Context, cmd RecordResponse) error {
	return h.facts.RecordResponse(ctx, cmd.Facts)
}

type MarkStatsStaleHandler struct{ facts FactStore }

func NewMarkStatsStaleHandler(f FactStore) MarkStatsStaleHandler {
	return MarkStatsStaleHandler{facts: f}
}

func (h MarkStatsStaleHandler) Handle(ctx context.Context, cmd MarkStatsStale) error {
	return h.facts.MarkStale(ctx, cmd.SurveyID)
}

type RefreshStatsHandler struct {
	projector Projector
	clock     clock.Clock
}

func NewRefreshStatsHandler(p Projector, clk clock.Clock) RefreshStatsHandler {
	return RefreshStatsHandler{projector: p, clock: clk}
}

func (h RefreshStatsHandler) Handle(ctx context.Context, cmd RefreshStats) error {
	return h.projector.Rebuild(ctx, cmd.SurveyID, h.clock.Now())
}
