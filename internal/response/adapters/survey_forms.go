package adapters

import (
	"context"
	"errors"

	"github.com/zenstar13/survey-prototype/internal/kernel/errs"
	"github.com/zenstar13/survey-prototype/internal/platform/cqrs"
	"github.com/zenstar13/survey-prototype/internal/response/domain"
	surveyquery "github.com/zenstar13/survey-prototype/internal/survey/app/query"
)

// SurveyForms is an anti-corruption layer: it calls the survey module's
// published query (in-process today, an HTTP/gRPC client tomorrow) and
// translates the result into this module's Form model.
type SurveyForms struct {
	getSurvey cqrs.QueryHandler[surveyquery.GetSurvey, surveyquery.SurveyView]
}

func NewSurveyForms(getSurvey cqrs.QueryHandler[surveyquery.GetSurvey, surveyquery.SurveyView]) *SurveyForms {
	return &SurveyForms{getSurvey: getSurvey}
}

func (s *SurveyForms) FormFor(ctx context.Context, surveyID string) (domain.Form, error) {
	v, err := s.getSurvey.Handle(ctx, surveyquery.GetSurvey{SurveyID: surveyID, Audience: surveyquery.AudiencePublic})
	if errs.KindOf(err) == errs.KindNotFound {
		return domain.Form{}, domain.ErrSurveyNotFound
	}
	if err != nil {
		return domain.Form{}, errors.Join(errors.New("load survey form"), err)
	}
	f := domain.Form{SurveyID: v.ID, AcceptingResponses: v.Status == "published"}
	for _, q := range v.Questions {
		fq := domain.FormQuestion{ID: q.ID, Required: q.Required, AllowsMultiple: q.Type == "multiple_choice"}
		for _, o := range q.Options {
			fq.OptionIDs = append(fq.OptionIDs, o.ID)
		}
		f.Questions = append(f.Questions, fq)
	}
	return f, nil
}
