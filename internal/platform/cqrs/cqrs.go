// Package cqrs defines the two handler shapes every module's application layer
// exposes, plus cross-cutting decorators.
//
// Commands change state and return only an error (identifiers are chosen by the
// caller and passed in). Queries never change state and return a read model.
package cqrs

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/zenstar13/survey-prototype/internal/kernel/errs"
)

type CommandHandler[C any] interface {
	Handle(ctx context.Context, cmd C) error
}

type QueryHandler[Q any, R any] interface {
	Handle(ctx context.Context, q Q) (R, error)
}

// CommandHandlerFunc / QueryHandlerFunc adapt functions to the handler interfaces.
type CommandHandlerFunc[C any] func(ctx context.Context, cmd C) error

func (f CommandHandlerFunc[C]) Handle(ctx context.Context, cmd C) error { return f(ctx, cmd) }

type QueryHandlerFunc[Q any, R any] func(ctx context.Context, q Q) (R, error)

func (f QueryHandlerFunc[Q, R]) Handle(ctx context.Context, q Q) (R, error) { return f(ctx, q) }

// DecorateCommand wraps a command handler with logging. Payloads are never
// logged: commands can carry personal data such as email addresses.
func DecorateCommand[C any](h CommandHandler[C], log *slog.Logger) CommandHandler[C] {
	name := typeName[C]()
	return CommandHandlerFunc[C](func(ctx context.Context, cmd C) error {
		start := time.Now()
		err := h.Handle(ctx, cmd)
		logOutcome(ctx, log, "command", name, start, err)
		return err
	})
}

// DecorateQuery wraps a query handler with logging.
func DecorateQuery[Q any, R any](h QueryHandler[Q, R], log *slog.Logger) QueryHandler[Q, R] {
	name := typeName[Q]()
	return QueryHandlerFunc[Q, R](func(ctx context.Context, q Q) (R, error) {
		start := time.Now()
		r, err := h.Handle(ctx, q)
		logOutcome(ctx, log, "query", name, start, err)
		return r, err
	})
}

func logOutcome(ctx context.Context, log *slog.Logger, kind, name string, start time.Time, err error) {
	attrs := []any{kind, name, "duration", time.Since(start).String()}
	switch {
	case err == nil:
		log.DebugContext(ctx, kind+" handled", attrs...)
	case errs.KindOf(err) != "":
		// Expected business outcome (validation, conflict...).
		log.InfoContext(ctx, kind+" rejected", append(attrs, "reason", err.Error())...)
	default:
		log.ErrorContext(ctx, kind+" failed", append(attrs, "error", err.Error())...)
	}
}

func typeName[T any]() string {
	var zero T
	n := fmt.Sprintf("%T", zero)
	if i := strings.LastIndex(n, "."); i >= 0 {
		return n[i+1:]
	}
	return n
}
