// Package response is the survey-answering module (bounded context).
package response

import (
	"database/sql"
	"log/slog"
	"net/http"

	"github.com/zenstar13/survey-prototype/internal/platform/clock"
	"github.com/zenstar13/survey-prototype/internal/platform/cqrs"
	"github.com/zenstar13/survey-prototype/internal/platform/httpx"
	"github.com/zenstar13/survey-prototype/internal/platform/id"
	"github.com/zenstar13/survey-prototype/internal/response/adapters"
	"github.com/zenstar13/survey-prototype/internal/response/app"
	"github.com/zenstar13/survey-prototype/internal/response/app/command"
	porthttp "github.com/zenstar13/survey-prototype/internal/response/ports/http"
	surveyquery "github.com/zenstar13/survey-prototype/internal/survey/app/query"
)

type Module struct {
	App  app.Application
	http *porthttp.Handlers
}

func New(
	db *sql.DB, log *slog.Logger, clk clock.Clock, ids id.Generator,
	getSurvey cqrs.QueryHandler[surveyquery.GetSurvey, surveyquery.SurveyView],
	submitLimiter *httpx.RateLimiter,
) *Module {
	log = log.With("module", "response")
	forms := adapters.NewSurveyForms(getSurvey)
	repo := adapters.NewSQLiteRepository(db)
	a := app.Application{Commands: app.Commands{
		Submit: cqrs.DecorateCommand[command.SubmitResponse](command.NewSubmitResponseHandler(forms, repo, clk), log),
	}}
	return &Module{App: a, http: porthttp.NewHandlers(a, ids, log, submitLimiter)}
}

func (m *Module) RegisterPublicRoutes(mux *http.ServeMux) { m.http.RegisterPublic(mux) }
