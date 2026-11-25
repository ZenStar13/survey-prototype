// Package httpx holds HTTP plumbing shared by both servers: JSON encoding,
// error mapping, middleware and server lifecycle.
package httpx

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/zenstar13/survey-prototype/internal/kernel/errs"
)

const maxBodyBytes = 1 << 20 // 1 MiB

// WriteJSON writes v with the given status.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

// DecodeJSON strictly decodes the request body: unknown fields and trailing
// data are rejected, the body size is capped.
func DecodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return errs.BadRequest("body_too_large", "request body is too large")
		}
		return errs.BadRequest("malformed_json", "request body is not valid JSON for this endpoint: "+err.Error())
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errs.BadRequest("malformed_json", "request body must contain a single JSON object")
	}
	return nil
}

type errorBody struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Fields  map[string]string `json:"fields,omitempty"`
}

// WriteError maps typed errors to HTTP statuses. Untyped errors are logged and
// returned as an opaque 500 so internals never leak to clients.
func WriteError(w http.ResponseWriter, r *http.Request, log *slog.Logger, err error) {
	var e *errs.Error
	if !errors.As(err, &e) {
		log.ErrorContext(r.Context(), "unhandled error", "path", r.URL.Path, "error", err.Error())
		WriteJSON(w, http.StatusInternalServerError, errorBody{errorDetail{Code: "internal", Message: "internal server error"}})
		return
	}
	WriteJSON(w, StatusFor(e.Kind), errorBody{errorDetail{Code: e.Code, Message: e.Message, Fields: e.Fields}})
}

func StatusFor(k errs.Kind) int {
	switch k {
	case errs.KindBadRequest:
		return http.StatusBadRequest
	case errs.KindInvalid:
		return http.StatusUnprocessableEntity
	case errs.KindNotFound:
		return http.StatusNotFound
	case errs.KindConflict:
		return http.StatusConflict
	case errs.KindUnauthorized:
		return http.StatusUnauthorized
	case errs.KindTooMany:
		return http.StatusTooManyRequests
	default:
		return http.StatusInternalServerError
	}
}

// NotFoundHandler answers unknown routes with the standard error envelope.
func NotFoundHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		WriteJSON(w, http.StatusNotFound, errorBody{errorDetail{Code: "route_not_found", Message: "no such endpoint"}})
	})
}
