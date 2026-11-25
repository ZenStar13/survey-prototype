package adapters

import (
	"context"
	"fmt"

	"github.com/zenstar13/survey-prototype/internal/analytics/app/command"
	"github.com/zenstar13/survey-prototype/internal/analytics/app/query"
	"github.com/zenstar13/survey-prototype/internal/analytics/domain"
	"github.com/zenstar13/survey-prototype/internal/kernel/errs"
	"github.com/zenstar13/survey-prototype/internal/platform/cqrs"
	surveyquery "github.com/zenstar13/survey-prototype/internal/survey/app/query"
)

// SurveyCatalog is the anti-corruption layer between analytics and the survey
// module's published query API.
type SurveyCatalog struct {
	getSurvey cqrs.QueryHandler[surveyquery.GetSurvey, surveyquery.SurveyView]
}

func NewSurveyCatalog(getSurvey cqrs.QueryHandler[surveyquery.GetSurvey, surveyquery.SurveyView]) *SurveyCatalog {
	return &SurveyCatalog{getSurvey: getSurvey}
}

var (
	_ command.SurveyCatalog = (*SurveyCatalog)(nil)
	_ query.Catalog         = (*SurveyCatalog)(nil)
)

func (c *SurveyCatalog) load(ctx context.Context, id string, publicOnly bool) (surveyquery.SurveyView, error) {
	aud := surveyquery.AudienceAdmin
	if publicOnly {
		aud = surveyquery.AudiencePublic
	}
	v, err := c.getSurvey.Handle(ctx, surveyquery.GetSurvey{SurveyID: id, Audience: aud})
	if errs.KindOf(err) == errs.KindNotFound {
		return v, domain.ErrSurveyNotFound
	}
	if err != nil {
		return v, fmt.Errorf("load survey: %w", err)
	}
	return v, nil
}

func (c *SurveyCatalog) Survey(ctx context.Context, id string) (domain.Survey, error) {
	v, err := c.load(ctx, id, false)
	if err != nil {
		return domain.Survey{}, err
	}
	s := domain.Survey{ID: v.ID, Live: v.Status == "published" || v.Status == "closed"}
	for _, q := range v.Questions {
		s.Questions = append(s.Questions, domain.SurveyQuestion{ID: q.ID, Prompt: q.Prompt})
	}
	return s, nil
}

func (c *SurveyCatalog) Describe(ctx context.Context, id string, publicOnly bool) (query.SurveyDescription, error) {
	v, err := c.load(ctx, id, publicOnly)
	if err != nil {
		return query.SurveyDescription{}, err
	}
	d := query.SurveyDescription{ID: v.ID, Title: v.Title, Status: v.Status}
	for _, q := range v.Questions {
		dq := query.DescribedQuestion{ID: q.ID, Number: q.Number, Prompt: q.Prompt, Type: q.Type, Required: q.Required}
		for _, o := range q.Options {
			dq.Options = append(dq.Options, query.DescribedOption{ID: o.ID, Label: o.Label})
		}
		d.Questions = append(d.Questions, dq)
	}
	return d, nil
}
