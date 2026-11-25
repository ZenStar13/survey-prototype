// Package survey is the survey-authoring module (bounded context). This file
// is the module's composition root: it is the only place that knows about
// both the application core and the concrete adapters.
package survey

import (
	"database/sql"
	"log/slog"
	"net/http"

	"github.com/zenstar13/survey-prototype/internal/platform/clock"
	"github.com/zenstar13/survey-prototype/internal/platform/cqrs"
	"github.com/zenstar13/survey-prototype/internal/platform/id"
	"github.com/zenstar13/survey-prototype/internal/survey/adapters"
	"github.com/zenstar13/survey-prototype/internal/survey/app"
	"github.com/zenstar13/survey-prototype/internal/survey/app/command"
	"github.com/zenstar13/survey-prototype/internal/survey/app/query"
	porthttp "github.com/zenstar13/survey-prototype/internal/survey/ports/http"
)

type Module struct {
	App  app.Application
	http *porthttp.Handlers
}

func New(db *sql.DB, log *slog.Logger, clk clock.Clock, ids id.Generator) *Module {
	log = log.With("module", "survey")
	repo := adapters.NewSQLiteRepository(db)
	rm := adapters.NewSQLiteReadModel(db)
	a := app.Application{
		Commands: app.Commands{
			CreateDraft: cqrs.DecorateCommand[command.CreateDraftSurvey](command.NewCreateDraftSurveyHandler(repo, ids, clk), log),
			ReviseDraft: cqrs.DecorateCommand[command.ReviseDraftSurvey](command.NewReviseDraftSurveyHandler(repo, ids, clk), log),
			Publish:     cqrs.DecorateCommand[command.PublishSurvey](command.NewPublishSurveyHandler(repo, clk), log),
			Close:       cqrs.DecorateCommand[command.CloseSurvey](command.NewCloseSurveyHandler(repo, clk), log),
		},
		Queries: app.Queries{
			GetSurvey:   cqrs.DecorateQuery[query.GetSurvey, query.SurveyView](query.NewGetSurveyHandler(rm), log),
			ListSurveys: cqrs.DecorateQuery[query.ListSurveys, []query.SurveySummary](query.NewListSurveysHandler(rm), log),
		},
	}
	return &Module{App: a, http: porthttp.NewHandlers(a, ids, log)}
}

func (m *Module) RegisterAdminRoutes(mux *http.ServeMux)  { m.http.RegisterAdmin(mux) }
func (m *Module) RegisterPublicRoutes(mux *http.ServeMux) { m.http.RegisterPublic(mux) }
