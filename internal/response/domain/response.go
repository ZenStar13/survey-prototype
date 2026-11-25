// Package domain is the survey-answering model.
//
// Response is the aggregate root. It is created in one shot by Submit, which
// validates the answers against the Form (this context's view of a published
// survey) and is never modified afterwards.
package domain

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/zenstar13/survey-prototype/internal/kernel/errs"
)

var (
	ErrSurveyNotFound     = errs.NotFound("survey_not_found", "survey not found")
	ErrSurveyNotAccepting = errs.Conflict("survey_not_accepting_responses", "this survey is not accepting responses")
	ErrAlreadyResponded   = errs.Conflict("already_responded", "a response has already been submitted with this email for this survey")
)

// Form is what the response context needs to know about a survey. It is built
// by an anti-corruption adapter from the survey module's published query API,
// so this model does not depend on the survey module's types.
type Form struct {
	SurveyID           string
	AcceptingResponses bool
	Questions          []FormQuestion
}

type FormQuestion struct {
	ID             string
	AllowsMultiple bool
	Required       bool
	OptionIDs      []string
}

// AnswerInput is one answer as submitted. An empty OptionIDs (or omitting the
// question entirely) means the question was skipped, which only optional
// questions allow.
type AnswerInput struct {
	QuestionID string
	OptionIDs  []string
}

type Answer struct {
	questionID string
	optionIDs  []string
}

func (a Answer) QuestionID() string  { return a.questionID }
func (a Answer) OptionIDs() []string { return append([]string(nil), a.optionIDs...) }

type Response struct {
	id          string
	surveyID    string
	email       Email
	answers     []Answer
	submittedAt time.Time
	events      []Event
}

// Submit validates and creates a response. All problems are reported at once,
// keyed by field: "email", "answers.<question id>" or "answers[i].question_id".
func Submit(id string, form Form, rawEmail string, inputs []AnswerInput, now time.Time) (*Response, error) {
	if strings.TrimSpace(id) == "" {
		return nil, errs.Invalid("response_id_required", "response id is required")
	}
	if !form.AcceptingResponses {
		return nil, ErrSurveyNotAccepting
	}
	var v errs.Violations

	email, err := NewEmail(rawEmail)
	if err != nil {
		v.Add("email", err.Error())
	}

	byID := make(map[string]FormQuestion, len(form.Questions))
	for _, q := range form.Questions {
		byID[q.ID] = q
	}
	selected := map[string][]string{}
	seen := map[string]bool{}
	for i, in := range inputs {
		q, ok := byID[in.QuestionID]
		if !ok {
			v.Add(fmt.Sprintf("answers[%d].question_id", i), "is not a question of this survey")
			continue
		}
		field := "answers." + q.ID
		if seen[q.ID] {
			v.Add(field, "question answered more than once")
			continue
		}
		seen[q.ID] = true
		if len(in.OptionIDs) == 0 {
			continue // skipped; required-ness is checked below
		}
		if !q.AllowsMultiple && len(in.OptionIDs) > 1 {
			v.Add(field, "exactly one option must be selected")
			continue
		}
		valid := make(map[string]bool, len(q.OptionIDs))
		for _, o := range q.OptionIDs {
			valid[o] = true
		}
		picked := map[string]bool{}
		ok = true
		for _, o := range in.OptionIDs {
			switch {
			case !valid[o]:
				v.Add(field, "contains an option that does not belong to this question")
				ok = false
			case picked[o]:
				v.Add(field, "contains the same option twice")
				ok = false
			}
			picked[o] = true
		}
		if ok {
			selected[q.ID] = in.OptionIDs
		}
	}

	answers := make([]Answer, 0, len(selected))
	for _, q := range form.Questions { // keep the survey's question order
		opts, answered := selected[q.ID]
		if !answered {
			if q.Required {
				v.Add("answers."+q.ID, "this question is required")
			}
			continue
		}
		answers = append(answers, Answer{questionID: q.ID, optionIDs: append([]string(nil), opts...)})
	}

	if err := v.Err(); err != nil {
		return nil, err
	}
	r := &Response{id: id, surveyID: form.SurveyID, email: email, answers: answers, submittedAt: now}
	r.events = append(r.events, Submitted{ResponseID: id, SurveyID: form.SurveyID, Answers: r.Answers(), At: now})
	return r, nil
}

func (r *Response) ID() string             { return r.id }
func (r *Response) SurveyID() string       { return r.surveyID }
func (r *Response) Email() Email           { return r.email }
func (r *Response) Answers() []Answer      { return append([]Answer(nil), r.answers...) }
func (r *Response) SubmittedAt() time.Time { return r.submittedAt }

func (r *Response) PullEvents() []Event {
	e := r.events
	r.events = nil
	return e
}

// Event is a domain event raised by the Response aggregate.
type Event interface{ isResponseEvent() }

type Submitted struct {
	ResponseID string
	SurveyID   string
	Answers    []Answer
	At         time.Time
}

func (Submitted) isResponseEvent() {}

// Repository is the write-side port. Add must return ErrAlreadyResponded when
// the email already answered the survey (enforced atomically by the store, so
// concurrent duplicate submissions cannot both succeed).
type Repository interface {
	Add(ctx context.Context, r *Response) error
}
