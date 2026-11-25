// Package domain is the analytics model.
//
// Relation is the aggregate root: an ordered set of 2..4 questions of one
// survey whose answers are cross-tabulated. The first question is the segment:
// "of the respondents who answered <option of Q1>, how many answered <option of
// Q2> (and <option of Q3> ...)?". Relating Q1 to Q2 and Q3 "together" is a
// single three-question relation (Q1 × Q2 × Q3).
package domain

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/zenstar13/survey-prototype/internal/kernel/errs"
)

const (
	MinRelationQuestions = 2
	MaxRelationQuestions = 4 // the number of cells grows multiplicatively
	MaxNameLength        = 120
)

var (
	ErrSurveyNotFound    = errs.NotFound("survey_not_found", "survey not found")
	ErrSurveyNotLive     = errs.Conflict("survey_not_live", "relations can only be defined on published (or closed) surveys, whose questions can no longer change")
	ErrRelationNotFound  = errs.NotFound("relation_not_found", "relation not found")
	ErrDuplicateRelation = errs.Conflict("relation_already_exists", "a relation with the same questions in the same order already exists")
)

// Survey is analytics' view of a survey (built by an anti-corruption adapter).
type Survey struct {
	ID        string
	Live      bool // published or closed: content is frozen
	Questions []SurveyQuestion
}

type SurveyQuestion struct {
	ID     string
	Prompt string
}

type Relation struct {
	id          string
	surveyID    string
	name        string
	questionIDs []string
	createdAt   time.Time
	events      []Event
}

// DefineRelation validates and creates a relation.
func DefineRelation(id string, survey Survey, name string, questionIDs []string, now time.Time) (*Relation, error) {
	if !survey.Live {
		return nil, ErrSurveyNotLive
	}
	var v errs.Violations
	if n := len(questionIDs); n < MinRelationQuestions || n > MaxRelationQuestions {
		v.Add("question_ids", fmt.Sprintf("must contain between %d and %d questions", MinRelationQuestions, MaxRelationQuestions))
	}
	prompts := make(map[string]string, len(survey.Questions))
	for _, q := range survey.Questions {
		prompts[q.ID] = q.Prompt
	}
	seen := map[string]bool{}
	for i, q := range questionIDs {
		field := fmt.Sprintf("question_ids[%d]", i)
		switch {
		case prompts[q] == "":
			v.Add(field, "is not a question of this survey")
		case seen[q]:
			v.Add(field, "is repeated")
		}
		seen[q] = true
	}
	name = strings.TrimSpace(name)
	if utf8.RuneCountInString(name) > MaxNameLength {
		v.Add("name", fmt.Sprintf("must be at most %d characters", MaxNameLength))
	}
	if err := v.Err(); err != nil {
		return nil, err
	}
	if name == "" {
		name = defaultName(questionIDs, prompts)
	}
	r := &Relation{id: id, surveyID: survey.ID, name: name, questionIDs: append([]string(nil), questionIDs...), createdAt: now}
	r.events = append(r.events, RelationDefined{RelationID: id, SurveyID: survey.ID, At: now})
	return r, nil
}

func defaultName(ids []string, prompts map[string]string) string {
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		parts = append(parts, prompts[id])
	}
	n := strings.Join(parts, " × ")
	if utf8.RuneCountInString(n) > MaxNameLength {
		n = string([]rune(n)[:MaxNameLength-1]) + "…"
	}
	return n
}

// Remove marks the relation for deletion.
func (r *Relation) Remove(now time.Time) {
	r.events = append(r.events, RelationRemoved{RelationID: r.id, SurveyID: r.surveyID, At: now})
}

func (r *Relation) ID() string            { return r.id }
func (r *Relation) SurveyID() string      { return r.surveyID }
func (r *Relation) Name() string          { return r.name }
func (r *Relation) QuestionIDs() []string { return append([]string(nil), r.questionIDs...) }
func (r *Relation) CreatedAt() time.Time  { return r.createdAt }

// Signature identifies the ordered question set; order matters because the
// first question is the segment.
func (r *Relation) Signature() string { return strings.Join(r.questionIDs, ",") }

func (r *Relation) PullEvents() []Event {
	e := r.events
	r.events = nil
	return e
}

func ReconstituteRelation(id, surveyID, name string, questionIDs []string, createdAt time.Time) *Relation {
	return &Relation{id: id, surveyID: surveyID, name: name, questionIDs: questionIDs, createdAt: createdAt}
}

type Event interface{ isAnalyticsEvent() }

type RelationDefined struct {
	RelationID, SurveyID string
	At                   time.Time
}

type RelationRemoved struct {
	RelationID, SurveyID string
	At                   time.Time
}

func (RelationDefined) isAnalyticsEvent() {}
func (RelationRemoved) isAnalyticsEvent() {}

// RelationRepository is the write-side port for relations.
type RelationRepository interface {
	// Add returns ErrDuplicateRelation if the signature already exists for the survey.
	Add(ctx context.Context, r *Relation) error
	// Remove loads the relation, lets prepare record its removal, then deletes it.
	Remove(ctx context.Context, surveyID, relationID string, prepare func(r *Relation) error) error
}
