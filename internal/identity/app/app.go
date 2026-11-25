// Package app wires the identity module's use cases.
package app

import (
	"github.com/zenstar13/survey-prototype/internal/identity/app/command"
	"github.com/zenstar13/survey-prototype/internal/identity/app/query"
	"github.com/zenstar13/survey-prototype/internal/platform/cqrs"
)

type Application struct {
	Commands Commands
	Queries  Queries
}

type Commands struct {
	EnsureAdmin cqrs.CommandHandler[command.EnsureAdmin]
	Login       cqrs.CommandHandler[command.Login]
	Logout      cqrs.CommandHandler[command.Logout]
}

type Queries struct {
	Authenticate cqrs.QueryHandler[query.Authenticate, query.Principal]
}
