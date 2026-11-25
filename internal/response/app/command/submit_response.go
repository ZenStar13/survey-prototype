// Package command holds the response module's write-side use cases.
package command

import (
	"context"

	"github.com/zenstar13/survey-prototype/internal/platform/clock"
	"github.com/zenstar13/survey-prototype/internal/response/domain"
)

// FormProvider is the port through which this module learns the shape of a
// published survey. Returns domain.ErrSurveyNotFound for unknown or draft surveys.
type FormProvider interface {
	FormFor(ctx context.Context, surveyID string) (domain.Form, error)
}

type Answer struct {
	QuestionID string
	OptionIDs  []string
}

type SubmitResponse struct {
	ResponseID string
	SurveyID   string
	Email      string
	Answers    []Answer
}

type SubmitResponseHandler struct {
	forms FormProvider
	repo  domain.Repository
	clock clock.Clock
}

func NewSubmitResponseHandler(forms FormProvider, repo domain.Repository, clk clock.Clock) SubmitResponseHandler {
	return SubmitResponseHandler{forms: forms, repo: repo, clock: clk}
}

func (h SubmitResponseHandler) Handle(ctx context.Context, cmd SubmitResponse) error {
	form, err := h.forms.FormFor(ctx, cmd.SurveyID)
	if err != nil {
		return err
	}
	inputs := make([]domain.AnswerInput, 0, len(cmd.Answers))
	for _, a := range cmd.Answers {
		inputs = append(inputs, domain.AnswerInput{QuestionID: a.QuestionID, OptionIDs: a.OptionIDs})
	}
	r, err := domain.Submit(cmd.ResponseID, form, cmd.Email, inputs, h.clock.Now())
	if err != nil {
		return err
	}
	return h.repo.Add(ctx, r)
}
