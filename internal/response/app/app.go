// Package app wires the response module's use cases.
package app

import (
	"github.com/zenstar13/survey-prototype/internal/platform/cqrs"
	"github.com/zenstar13/survey-prototype/internal/response/app/command"
)

type Application struct {
	Commands Commands
}

type Commands struct {
	Submit cqrs.CommandHandler[command.SubmitResponse]
}
