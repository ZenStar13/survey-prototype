// Package app wires the survey module's commands and queries.
package app

import (
	"github.com/zenstar13/survey-prototype/internal/platform/cqrs"
	"github.com/zenstar13/survey-prototype/internal/survey/app/command"
	"github.com/zenstar13/survey-prototype/internal/survey/app/query"
)

type Application struct {
	Commands Commands
	Queries  Queries
}

type Commands struct {
	CreateDraft cqrs.CommandHandler[command.CreateDraftSurvey]
	ReviseDraft cqrs.CommandHandler[command.ReviseDraftSurvey]
	Publish     cqrs.CommandHandler[command.PublishSurvey]
	Close       cqrs.CommandHandler[command.CloseSurvey]
}

type Queries struct {
	GetSurvey   cqrs.QueryHandler[query.GetSurvey, query.SurveyView]
	ListSurveys cqrs.QueryHandler[query.ListSurveys, []query.SurveySummary]
}
