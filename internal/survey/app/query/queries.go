// Package query holds the survey module's read-side use cases and read models.
//
// The read models are also the module's published query API: other modules
// (response, analytics) consume GetSurvey through their own anti-corruption
// adapters rather than touching survey tables or domain types.
package query

import (
	"context"
	"time"

	"github.com/zenstar13/survey-prototype/internal/survey/domain"
)

type OptionView struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type QuestionView struct {
	ID       string       `json:"id"`
	Number   int          `json:"number"` // 1-based position
	Prompt   string       `json:"prompt"`
	Type     string       `json:"type"`
	Required bool         `json:"required"`
	Options  []OptionView `json:"options"`
}

type SurveyView struct {
	ID          string         `json:"id"`
	Title       string         `json:"title"`
	Description string         `json:"description"`
	Status      string         `json:"status"`
	Questions   []QuestionView `json:"questions"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	PublishedAt *time.Time     `json:"published_at,omitempty"`
	ClosedAt    *time.Time     `json:"closed_at,omitempty"`
}

type SurveySummary struct {
	ID            string     `json:"id"`
	Title         string     `json:"title"`
	Description   string     `json:"description"`
	Status        string     `json:"status"`
	QuestionCount int        `json:"question_count"`
	CreatedAt     time.Time  `json:"created_at"`
	PublishedAt   *time.Time `json:"published_at,omitempty"`
}

// ReadModel is the port the query handlers read from.
type ReadModel interface {
	SurveyByID(ctx context.Context, id string) (SurveyView, error) // domain.ErrNotFound if absent
	ListSurveys(ctx context.Context, statuses []string) ([]SurveySummary, error)
}

// Audience controls visibility: the public never sees drafts.
type Audience int

const (
	AudienceAdmin Audience = iota
	AudiencePublic
)

type GetSurvey struct {
	SurveyID string
	Audience Audience
}

type GetSurveyHandler struct{ rm ReadModel }

func NewGetSurveyHandler(rm ReadModel) GetSurveyHandler { return GetSurveyHandler{rm: rm} }

func (h GetSurveyHandler) Handle(ctx context.Context, q GetSurvey) (SurveyView, error) {
	v, err := h.rm.SurveyByID(ctx, q.SurveyID)
	if err != nil {
		return SurveyView{}, err
	}
	if q.Audience == AudiencePublic && v.Status == string(domain.StatusDraft) {
		return SurveyView{}, domain.ErrNotFound
	}
	return v, nil
}

type ListSurveys struct {
	Audience Audience
	Statuses []string // optional filter (admin only)
}

type ListSurveysHandler struct{ rm ReadModel }

func NewListSurveysHandler(rm ReadModel) ListSurveysHandler { return ListSurveysHandler{rm: rm} }

func (h ListSurveysHandler) Handle(ctx context.Context, q ListSurveys) ([]SurveySummary, error) {
	statuses := q.Statuses
	if q.Audience == AudiencePublic {
		statuses = []string{string(domain.StatusPublished), string(domain.StatusClosed)}
	}
	return h.rm.ListSurveys(ctx, statuses)
}
