// Package http exposes relations (admin) and reports (admin + public).
package http

import (
	"log/slog"
	"net/http"

	"github.com/zenstar13/survey-prototype/internal/analytics/app"
	"github.com/zenstar13/survey-prototype/internal/analytics/app/command"
	"github.com/zenstar13/survey-prototype/internal/analytics/app/query"
	"github.com/zenstar13/survey-prototype/internal/platform/httpx"
	"github.com/zenstar13/survey-prototype/internal/platform/id"
)

type Handlers struct {
	app app.Application
	ids id.Generator
	log *slog.Logger
}

func NewHandlers(a app.Application, ids id.Generator, log *slog.Logger) *Handlers {
	return &Handlers{app: a, ids: ids, log: log}
}

func (h *Handlers) RegisterAdmin(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/surveys/{surveyID}/relations", h.listRelations)
	mux.HandleFunc("POST /api/v1/surveys/{surveyID}/relations", h.defineRelation)
	mux.HandleFunc("DELETE /api/v1/surveys/{surveyID}/relations/{relationID}", h.removeRelation)
	mux.HandleFunc("GET /api/v1/surveys/{surveyID}/report", h.report(false))
	mux.HandleFunc("POST /api/v1/surveys/{surveyID}/report/refresh", h.refresh)
}

func (h *Handlers) RegisterPublic(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/surveys/{surveyID}/results", h.report(true))
}

type defineRelationRequest struct {
	Name        string   `json:"name"`
	QuestionIDs []string `json:"question_ids"`
}

func (h *Handlers) defineRelation(w http.ResponseWriter, r *http.Request) {
	var req defineRelationRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		httpx.WriteError(w, r, h.log, err)
		return
	}
	cmd := command.DefineRelation{RelationID: h.ids.NewID(), SurveyID: r.PathValue("surveyID"), Name: req.Name, QuestionIDs: req.QuestionIDs}
	if err := h.app.Commands.DefineRelation.Handle(r.Context(), cmd); err != nil {
		httpx.WriteError(w, r, h.log, err)
		return
	}
	// 201: the relation exists now; its statistics are computed asynchronously.
	httpx.WriteJSON(w, http.StatusCreated, map[string]string{"id": cmd.RelationID})
}

func (h *Handlers) removeRelation(w http.ResponseWriter, r *http.Request) {
	err := h.app.Commands.RemoveRelation.Handle(r.Context(), command.RemoveRelation{
		SurveyID: r.PathValue("surveyID"), RelationID: r.PathValue("relationID"),
	})
	if err != nil {
		httpx.WriteError(w, r, h.log, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) listRelations(w http.ResponseWriter, r *http.Request) {
	out, err := h.app.Queries.ListRelations.Handle(r.Context(), query.ListRelations{SurveyID: r.PathValue("surveyID")})
	if err != nil {
		httpx.WriteError(w, r, h.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"relations": out})
}

func (h *Handlers) report(public bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rep, err := h.app.Queries.SurveyReport.Handle(r.Context(), query.GetSurveyReport{SurveyID: r.PathValue("surveyID"), PublicOnly: public})
		if err != nil {
			httpx.WriteError(w, r, h.log, err)
			return
		}
		if public {
			// Results are eventually consistent anyway; a short cache absorbs bursts.
			w.Header().Set("Cache-Control", "public, max-age=5")
		}
		httpx.WriteJSON(w, http.StatusOK, rep)
	}
}

func (h *Handlers) refresh(w http.ResponseWriter, r *http.Request) {
	surveyID := r.PathValue("surveyID")
	if _, err := h.app.Queries.ListRelations.Handle(r.Context(), query.ListRelations{SurveyID: surveyID}); err != nil {
		httpx.WriteError(w, r, h.log, err) // 404 for unknown surveys
		return
	}
	if err := h.app.Commands.MarkStatsStale.Handle(r.Context(), command.MarkStatsStale{SurveyID: surveyID}); err != nil {
		httpx.WriteError(w, r, h.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusAccepted, map[string]string{"status": "refresh scheduled"})
}
