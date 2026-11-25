-- Platform: transactional outbox. Aggregates append integration events here in
-- the SAME transaction as their state change; the worker relays them to
-- subscribers (at-least-once delivery, subscribers are idempotent).
CREATE TABLE outbox_messages (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    topic        TEXT    NOT NULL,
    payload      TEXT    NOT NULL,
    occurred_at  TEXT    NOT NULL,
    processed_at TEXT,
    attempts     INTEGER NOT NULL DEFAULT 0,
    last_error   TEXT
);
CREATE INDEX idx_outbox_pending ON outbox_messages (id) WHERE processed_at IS NULL;
