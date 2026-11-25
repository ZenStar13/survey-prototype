// Package events holds the analytics module's asynchronous driving adapters:
// outbox subscribers and the statistics refresh job run by the worker.
package events

import (
	"context"
	"fmt"

	"github.com/zenstar13/survey-prototype/internal/analytics/app"
	"github.com/zenstar13/survey-prototype/internal/analytics/app/command"
	"github.com/zenstar13/survey-prototype/internal/analytics/app/query"
	"github.com/zenstar13/survey-prototype/internal/contracts"
	"github.com/zenstar13/survey-prototype/internal/platform/outbox"
)

// Subscribe registers analytics' interest in integration events.
func Subscribe(relay *outbox.Relay, a app.Application) {
	relay.Subscribe(contracts.TopicResponseSubmitted, func(ctx context.Context, m outbox.Message) error {
		var e contracts.ResponseSubmitted
		if err := m.Decode(&e); err != nil {
			return fmt.Errorf("decode %s: %w", m.Topic, err)
		}
		facts := command.ResponseFacts{ResponseID: e.ResponseID, SurveyID: e.SurveyID, SubmittedAt: e.SubmittedAt}
		for _, ans := range e.Answers {
			facts.Answers = append(facts.Answers, command.FactAnswer{QuestionID: ans.QuestionID, OptionIDs: ans.OptionIDs})
		}
		return a.Commands.RecordResponse.Handle(ctx, command.RecordResponse{Facts: facts})
	})

	// Publishing creates the (empty) statistics; relation changes require a
	// recomputation (which also backfills a new relation with existing data).
	relay.Subscribe(contracts.TopicSurveyPublished, func(ctx context.Context, m outbox.Message) error {
		var e contracts.SurveyPublished
		if err := m.Decode(&e); err != nil {
			return fmt.Errorf("decode %s: %w", m.Topic, err)
		}
		return a.Commands.MarkStatsStale.Handle(ctx, command.MarkStatsStale{SurveyID: e.SurveyID})
	})
	relationChanged := func(ctx context.Context, m outbox.Message) error {
		var e contracts.RelationChanged
		if err := m.Decode(&e); err != nil {
			return fmt.Errorf("decode %s: %w", m.Topic, err)
		}
		return a.Commands.MarkStatsStale.Handle(ctx, command.MarkStatsStale{SurveyID: e.SurveyID})
	}
	relay.Subscribe(contracts.TopicRelationDefined, relationChanged)
	relay.Subscribe(contracts.TopicRelationRemoved, relationChanged)
}

// StatsRefresher is a worker job: it recomputes every stale survey.
type StatsRefresher struct{ app app.Application }

func NewStatsRefresher(a app.Application) *StatsRefresher { return &StatsRefresher{app: a} }

func (j *StatsRefresher) Name() string { return "analytics-stats-refresher" }

func (j *StatsRefresher) Run(ctx context.Context) error {
	ids, err := j.app.Queries.StaleSurveys.Handle(ctx, query.StaleSurveys{Limit: 50})
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := j.app.Commands.RefreshStats.Handle(ctx, command.RefreshStats{SurveyID: id}); err != nil {
			return fmt.Errorf("refresh survey %s: %w", id, err)
		}
	}
	return nil
}
