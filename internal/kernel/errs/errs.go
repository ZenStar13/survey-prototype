// Package errs is the shared kernel for typed, transport-agnostic errors.
//
// Domain and application layers return *errs.Error values that describe WHAT went
// wrong (a Kind plus a stable machine-readable Code). Driving adapters (HTTP, the
// worker) decide HOW to surface them, e.g. mapping KindConflict to 409.
// The package has no dependencies so every layer may import it.
package errs

import (
	"errors"
	"sort"
	"strings"
)

// Kind classifies an error independently of any transport.
type Kind string

const (
	KindBadRequest   Kind = "bad_request"  // the request could not be understood (malformed input)
	KindInvalid      Kind = "invalid"      // the request was understood but violates business rules
	KindNotFound     Kind = "not_found"    // the addressed resource does not exist (or is not visible)
	KindConflict     Kind = "conflict"     // the request conflicts with the current state
	KindUnauthorized Kind = "unauthorized" // missing or invalid credentials
	KindTooMany      Kind = "too_many"     // rate limited
)

// Error is a typed error with a stable code.
type Error struct {
	Kind    Kind
	Code    string
	Message string
	Fields  map[string]string // per-field violations, only for KindInvalid
}

func (e *Error) Error() string {
	if len(e.Fields) == 0 {
		return e.Code + ": " + e.Message
	}
	keys := make([]string, 0, len(e.Fields))
	for k := range e.Fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+" "+e.Fields[k])
	}
	return e.Code + ": " + strings.Join(parts, "; ")
}

// Is makes errors.Is compare by Kind and Code, so sentinel errors keep matching
// even when a copy with extra detail is returned.
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && t.Kind == e.Kind && t.Code == e.Code
}

func New(kind Kind, code, message string) *Error {
	return &Error{Kind: kind, Code: code, Message: message}
}

func BadRequest(code, message string) *Error   { return New(KindBadRequest, code, message) }
func Invalid(code, message string) *Error      { return New(KindInvalid, code, message) }
func NotFound(code, message string) *Error     { return New(KindNotFound, code, message) }
func Conflict(code, message string) *Error     { return New(KindConflict, code, message) }
func Unauthorized(code, message string) *Error { return New(KindUnauthorized, code, message) }
func TooMany(code, message string) *Error      { return New(KindTooMany, code, message) }

// ErrValidation matches any error produced by Violations.Err.
var ErrValidation = Invalid("validation_failed", "the request contains invalid fields")

// KindOf returns the Kind of err, or "" for untyped (unexpected) errors.
func KindOf(err error) Kind {
	var e *Error
	if errors.As(err, &e) {
		return e.Kind
	}
	return ""
}

// Violations collects field-level validation failures so a caller gets all of
// them at once instead of fixing one per round trip.
type Violations struct {
	fields map[string]string
}

// Add records a violation; the first message recorded for a field wins.
func (v *Violations) Add(field, message string) {
	if v.fields == nil {
		v.fields = make(map[string]string)
	}
	if _, exists := v.fields[field]; !exists {
		v.fields[field] = message
	}
}

func (v *Violations) Any() bool { return len(v.fields) > 0 }

// Err returns nil when there are no violations.
func (v *Violations) Err() error {
	if !v.Any() {
		return nil
	}
	return &Error{
		Kind:    ErrValidation.Kind,
		Code:    ErrValidation.Code,
		Message: ErrValidation.Message,
		Fields:  v.fields,
	}
}
