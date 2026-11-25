// Command surveyd runs the survey platform: the public (end-user) server, the
// admin server and the analytics worker. By default all three run in one
// process; set SURVEY_RUN=public|admin|worker (comma separated) to split them
// across processes sharing the same SQLite file.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/zenstar13/survey-prototype/internal/bootstrap"
)

func main() {
	level := slog.LevelInfo
	if os.Getenv("SURVEY_LOG_LEVEL") == "debug" {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))

	cfg, err := bootstrap.ConfigFromEnv()
	if err != nil {
		log.Error("invalid configuration", "error", err.Error())
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	app, err := bootstrap.New(ctx, cfg, log)
	if err != nil {
		log.Error("startup failed", "error", err.Error())
		os.Exit(1)
	}
	defer app.Close()

	if err := app.Run(ctx); err != nil {
		log.Error("stopped with error", "error", err.Error())
		os.Exit(1)
	}
}
