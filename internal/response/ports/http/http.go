// Package http exposes response submission on the public server.
package http

import (
	"log/slog"
	"net/http"

	"github.com/zenstar13/survey-prototype/internal/platform/httpx"
	"github.com/zenstar13/survey-prototype/internal/platform/id"
	"github.com/zenstar13/survey-prototype/internal/response/app"
	"github.com/zenstar13/survey-prototype/internal/response/app/command"
)

type Handlers struct {
	app     app.Application
	ids     id.Generator
	log     *slog.Logger
	limiter *httpx.RateLimiter
}

func NewHandlers(a app.Application, ids id.Generator, log *slog.Logger, limiter *httpx.RateLimiter) *Handlers {
	return &Handlers{app: a, ids: ids, log: log, limiter: limiter}
}

func (h *Handlers) RegisterPublic(mux *http.ServeMux) {
	submit := http.Handler(http.HandlerFunc(h.submit))
	if h.limiter != nil {
		submit = httpx.RateLimit(h.limiter, h.log)(submit)
	}
	mux.Handle("POST /api/v1/surveys/{surveyID}/responses", submit)
}

type answerRequest struct {
	QuestionID string   `json:"question_id"`
	OptionIDs  []string `json:"option_ids"`
}

type submitRequest struct {
	Email   string          `json:"email"`
	Answers []answerRequest `json:"answers"`
}

type submitResponse struct {
	ID string `json:"id"`
}

func (h *Handlers) submit(w http.ResponseWriter, r *http.Request) {
	var req submitRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		httpx.WriteError(w, r, h.log, err)
		return
	}
	cmd := command.SubmitResponse{ResponseID: h.ids.NewID(), SurveyID: r.PathValue("surveyID"), Email: req.Email}
	for _, a := range req.Answers {
		cmd.Answers = append(cmd.Answers, command.Answer{QuestionID: a.QuestionID, OptionIDs: a.OptionIDs})
	}
	if err := h.app.Commands.Submit.Handle(r.Context(), cmd); err != nil {
		httpx.WriteError(w, r, h.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, submitResponse{ID: cmd.ResponseID})
}
