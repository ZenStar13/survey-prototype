// Package app wires the analytics module's commands and queries.
package app

import (
	"github.com/zenstar13/survey-prototype/internal/analytics/app/command"
	"github.com/zenstar13/survey-prototype/internal/analytics/app/query"
	"github.com/zenstar13/survey-prototype/internal/platform/cqrs"
)

type Application struct {
	Commands Commands
	Queries  Queries
}

type Commands struct {
	DefineRelation cqrs.CommandHandler[command.DefineRelation]
	RemoveRelation cqrs.CommandHandler[command.RemoveRelation]
	RecordResponse cqrs.CommandHandler[command.RecordResponse]
	MarkStatsStale cqrs.CommandHandler[command.MarkStatsStale]
	RefreshStats   cqrs.CommandHandler[command.RefreshStats]
}

type Queries struct {
	SurveyReport  cqrs.QueryHandler[query.GetSurveyReport, query.SurveyReport]
	ListRelations cqrs.QueryHandler[query.ListRelations, []query.RelationView]
	StaleSurveys  cqrs.QueryHandler[query.StaleSurveys, []string]
}
