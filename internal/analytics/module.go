// Package analytics is the relations & statistics module (bounded context).
package analytics

import (
	"database/sql"
	"log/slog"
	"net/http"

	"github.com/zenstar13/survey-prototype/internal/analytics/adapters"
	"github.com/zenstar13/survey-prototype/internal/analytics/app"
	"github.com/zenstar13/survey-prototype/internal/analytics/app/command"
	"github.com/zenstar13/survey-prototype/internal/analytics/app/query"
	"github.com/zenstar13/survey-prototype/internal/analytics/ports/events"
	porthttp "github.com/zenstar13/survey-prototype/internal/analytics/ports/http"
	"github.com/zenstar13/survey-prototype/internal/platform/clock"
	"github.com/zenstar13/survey-prototype/internal/platform/cqrs"
	"github.com/zenstar13/survey-prototype/internal/platform/id"
	"github.com/zenstar13/survey-prototype/internal/platform/outbox"
	"github.com/zenstar13/survey-prototype/internal/platform/worker"
	surveyquery "github.com/zenstar13/survey-prototype/internal/survey/app/query"
)

type Module struct {
	App  app.Application
	http *porthttp.Handlers
}

func New(
	db *sql.DB, log *slog.Logger, clk clock.Clock, ids id.Generator,
	getSurvey cqrs.QueryHandler[surveyquery.GetSurvey, surveyquery.SurveyView],
) *Module {
	log = log.With("module", "analytics")
	catalog := adapters.NewSurveyCatalog(getSurvey)
	relations := adapters.NewRelationRepository(db)
	facts := adapters.NewFactStore(db)
	projector := adapters.NewProjector(db)
	stats := adapters.NewStatsReadModel(db)

	a := app.Application{
		Commands: app.Commands{
			DefineRelation: cqrs.DecorateCommand[command.DefineRelation](command.NewDefineRelationHandler(catalog, relations, clk), log),
			RemoveRelation: cqrs.DecorateCommand[command.RemoveRelation](command.NewRemoveRelationHandler(relations, clk), log),
			RecordResponse: cqrs.DecorateCommand[command.RecordResponse](command.NewRecordResponseHandler(facts), log),
			MarkStatsStale: cqrs.DecorateCommand[command.MarkStatsStale](command.NewMarkStatsStaleHandler(facts), log),
			RefreshStats:   cqrs.DecorateCommand[command.RefreshStats](command.NewRefreshStatsHandler(projector, clk), log),
		},
		Queries: app.Queries{
			SurveyReport:  cqrs.DecorateQuery[query.GetSurveyReport, query.SurveyReport](query.NewGetSurveyReportHandler(catalog, stats), log),
			ListRelations: cqrs.DecorateQuery[query.ListRelations, []query.RelationView](query.NewListRelationsHandler(catalog, relations), log),
			StaleSurveys:  cqrs.DecorateQuery[query.StaleSurveys, []string](query.NewStaleSurveysHandler(stats), log),
		},
	}
	return &Module{App: a, http: porthttp.NewHandlers(a, ids, log)}
}

func (m *Module) RegisterAdminRoutes(mux *http.ServeMux)  { m.http.RegisterAdmin(mux) }
func (m *Module) RegisterPublicRoutes(mux *http.ServeMux) { m.http.RegisterPublic(mux) }

// Subscribe wires the module's outbox subscribers.
func (m *Module) Subscribe(relay *outbox.Relay) { events.Subscribe(relay, m.App) }

// Jobs returns the module's background jobs.
func (m *Module) Jobs() []worker.Job { return []worker.Job{events.NewStatsRefresher(m.App)} }
