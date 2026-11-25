// Package outbox implements the transactional outbox pattern.
//
// Writers call Append inside the transaction that changes their aggregate, so an
// event is recorded if and only if the change commits. The Relay (run by the
// worker) delivers pending messages to in-process subscribers in order.
// Delivery is at-least-once: a crash between handling and marking a message as
// processed redelivers it, so subscribers must be idempotent.
package outbox

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/zenstar13/survey-prototype/internal/platform/clock"
	"github.com/zenstar13/survey-prototype/internal/platform/sqlitedb"
)

type Message struct {
	ID         int64
	Topic      string
	Payload    json.RawMessage
	OccurredAt time.Time
	Attempts   int
}

// Decode unmarshals the payload into v.
func (m Message) Decode(v any) error { return json.Unmarshal(m.Payload, v) }

// Append records an event in the caller's transaction.
func Append(ctx context.Context, tx *sql.Tx, topic string, payload any, occurredAt time.Time) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("outbox: marshal %s: %w", topic, err)
	}
	_, err = tx.ExecContext(ctx,
		`INSERT INTO outbox_messages (topic, payload, occurred_at) VALUES (?, ?, ?)`,
		topic, string(b), sqlitedb.FormatTime(occurredAt))
	if err != nil {
		return fmt.Errorf("outbox: append %s: %w", topic, err)
	}
	return nil
}

type Handler func(ctx context.Context, msg Message) error

// Relay delivers pending outbox messages to subscribers.
type Relay struct {
	db          *sql.DB
	log         *slog.Logger
	clock       clock.Clock
	handlers    map[string][]Handler
	batchSize   int
	maxAttempts int
}

func NewRelay(db *sql.DB, log *slog.Logger, clk clock.Clock) *Relay {
	return &Relay{db: db, log: log, clock: clk, handlers: map[string][]Handler{}, batchSize: 200, maxAttempts: 10}
}

// Subscribe registers h for topic. Must be called before the worker starts.
func (r *Relay) Subscribe(topic string, h Handler) {
	r.handlers[topic] = append(r.handlers[topic], h)
}

func (r *Relay) Name() string { return "outbox-relay" }

// Run drains the outbox. On a handler failure it records the error and stops,
// so later messages are not delivered before the failed one (it is retried on
// the next run). After maxAttempts a message is parked (dead-lettered) and
// stays in the table for inspection.
func (r *Relay) Run(ctx context.Context) error {
	for {
		msgs, err := r.pending(ctx)
		if err != nil {
			return err
		}
		if len(msgs) == 0 {
			return nil
		}
		for _, m := range msgs {
			if err := r.dispatch(ctx, m); err != nil {
				r.log.WarnContext(ctx, "outbox message failed", "id", m.ID, "topic", m.Topic, "attempt", m.Attempts+1, "error", err.Error())
				if _, uerr := r.db.ExecContext(ctx,
					`UPDATE outbox_messages SET attempts = attempts + 1, last_error = ? WHERE id = ?`,
					err.Error(), m.ID); uerr != nil {
					return uerr
				}
				return nil
			}
			if _, err := r.db.ExecContext(ctx,
				`UPDATE outbox_messages SET processed_at = ?, attempts = attempts + 1, last_error = NULL WHERE id = ?`,
				sqlitedb.FormatTime(r.clock.Now()), m.ID); err != nil {
				return err
			}
		}
	}
}

func (r *Relay) dispatch(ctx context.Context, m Message) error {
	for _, h := range r.handlers[m.Topic] {
		if err := h(ctx, m); err != nil {
			return err
		}
	}
	return nil
}

func (r *Relay) pending(ctx context.Context) ([]Message, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, topic, payload, occurred_at, attempts
		FROM outbox_messages
		WHERE processed_at IS NULL AND attempts < ?
		ORDER BY id
		LIMIT ?`, r.maxAttempts, r.batchSize)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		var m Message
		var payload, occurred string
		if err := rows.Scan(&m.ID, &m.Topic, &payload, &occurred, &m.Attempts); err != nil {
			return nil, err
		}
		m.Payload = json.RawMessage(payload)
		if m.OccurredAt, err = sqlitedb.ParseTime(occurred); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
