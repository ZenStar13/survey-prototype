// Package http is the survey module's driving adapter: it translates HTTP
// requests into commands/queries and results into JSON.
package http

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/zenstar13/survey-prototype/internal/platform/httpx"
	"github.com/zenstar13/survey-prototype/internal/platform/id"
	"github.com/zenstar13/survey-prototype/internal/survey/app"
	"github.com/zenstar13/survey-prototype/internal/survey/app/command"
	"github.com/zenstar13/survey-prototype/internal/survey/app/query"
)

type Handlers struct {
	app app.Application
	ids id.Generator
	log *slog.Logger
}

func NewHandlers(a app.Application, ids id.Generator, log *slog.Logger) *Handlers {
	return &Handlers{app: a, ids: ids, log: log}
}

// RegisterAdmin mounts the authoring endpoints (the caller wraps them in auth).
func (h *Handlers) RegisterAdmin(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/surveys", h.adminList)
	mux.HandleFunc("POST /api/v1/surveys", h.create)
	mux.HandleFunc("GET /api/v1/surveys/{surveyID}", h.get(query.AudienceAdmin))
	mux.HandleFunc("PUT /api/v1/surveys/{surveyID}", h.revise)
	mux.HandleFunc("POST /api/v1/surveys/{surveyID}/publish", h.publish)
	mux.HandleFunc("POST /api/v1/surveys/{surveyID}/close", h.close)
}

// RegisterPublic mounts the read-only endpoints respondents use.
func (h *Handlers) RegisterPublic(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/surveys", h.publicList)
	mux.HandleFunc("GET /api/v1/surveys/{surveyID}", h.get(query.AudiencePublic))
}

type questionRequest struct {
	Prompt   string   `json:"prompt"`
	Type     string   `json:"type"`
	Required *bool    `json:"required"` // defaults to true: optional must be explicit
	Options  []string `json:"options"`
}

type surveyRequest struct {
	Title       string            `json:"title"`
	Description string            `json:"description"`
	Questions   []questionRequest `json:"questions"`
}

func (r surveyRequest) questions() []command.QuestionInput {
	out := make([]command.QuestionInput, 0, len(r.Questions))
	for _, q := range r.Questions {
		required := true
		if q.Required != nil {
			required = *q.Required
		}
		out = append(out, command.QuestionInput{Prompt: q.Prompt, Type: q.Type, Required: required, Options: q.Options})
	}
	return out
}

type createdResponse struct {
	ID string `json:"id"`
}

func (h *Handlers) create(w http.ResponseWriter, r *http.Request) {
	var req surveyRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		httpx.WriteError(w, r, h.log, err)
		return
	}
	surveyID := h.ids.NewID()
	err := h.app.Commands.CreateDraft.Handle(r.Context(), command.CreateDraftSurvey{
		SurveyID: surveyID, Title: req.Title, Description: req.Description, Questions: req.questions(),
	})
	if err != nil {
		httpx.WriteError(w, r, h.log, err)
		return
	}
	w.Header().Set("Location", "/api/v1/surveys/"+surveyID)
	httpx.WriteJSON(w, http.StatusCreated, createdResponse{ID: surveyID})
}

func (h *Handlers) revise(w http.ResponseWriter, r *http.Request) {
	var req surveyRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		httpx.WriteError(w, r, h.log, err)
		return
	}
	err := h.app.Commands.ReviseDraft.Handle(r.Context(), command.ReviseDraftSurvey{
		SurveyID: r.PathValue("surveyID"), Title: req.Title, Description: req.Description, Questions: req.questions(),
	})
	if err != nil {
		httpx.WriteError(w, r, h.log, err)
		return
	}
	h.writeSurvey(w, r, r.PathValue("surveyID"), query.AudienceAdmin)
}

func (h *Handlers) publish(w http.ResponseWriter, r *http.Request) {
	if err := h.app.Commands.Publish.Handle(r.Context(), command.PublishSurvey{SurveyID: r.PathValue("surveyID")}); err != nil {
		httpx.WriteError(w, r, h.log, err)
		return
	}
	h.writeSurvey(w, r, r.PathValue("surveyID"), query.AudienceAdmin)
}

func (h *Handlers) close(w http.ResponseWriter, r *http.Request) {
	if err := h.app.Commands.Close.Handle(r.Context(), command.CloseSurvey{SurveyID: r.PathValue("surveyID")}); err != nil {
		httpx.WriteError(w, r, h.log, err)
		return
	}
	h.writeSurvey(w, r, r.PathValue("surveyID"), query.AudienceAdmin)
}

func (h *Handlers) get(aud query.Audience) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		h.writeSurvey(w, r, r.PathValue("surveyID"), aud)
	}
}

func (h *Handlers) writeSurvey(w http.ResponseWriter, r *http.Request, id string, aud query.Audience) {
	v, err := h.app.Queries.GetSurvey.Handle(r.Context(), query.GetSurvey{SurveyID: id, Audience: aud})
	if err != nil {
		httpx.WriteError(w, r, h.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, v)
}

type listResponse struct {
	Surveys []query.SurveySummary `json:"surveys"`
}

func (h *Handlers) adminList(w http.ResponseWriter, r *http.Request) {
	var statuses []string
	if s := r.URL.Query().Get("status"); s != "" {
		statuses = strings.Split(s, ",")
	}
	h.list(w, r, query.ListSurveys{Audience: query.AudienceAdmin, Statuses: statuses})
}

func (h *Handlers) publicList(w http.ResponseWriter, r *http.Request) {
	h.list(w, r, query.ListSurveys{Audience: query.AudiencePublic})
}

func (h *Handlers) list(w http.ResponseWriter, r *http.Request, q query.ListSurveys) {
	out, err := h.app.Queries.ListSurveys.Handle(r.Context(), q)
	if err != nil {
		httpx.WriteError(w, r, h.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, listResponse{Surveys: out})
}
