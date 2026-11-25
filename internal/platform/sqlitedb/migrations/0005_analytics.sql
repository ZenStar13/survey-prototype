-- Module: analytics (relations + statistics).

-- Relations ("cross tabs") between 2..4 questions of one survey, in order. The
-- first question is the segment ("of the people who answered X ...").
CREATE TABLE analytics_relations (
    id         TEXT PRIMARY KEY,
    survey_id  TEXT NOT NULL,
    name       TEXT NOT NULL,
    signature  TEXT NOT NULL, -- ordered question ids, prevents duplicates
    created_at TEXT NOT NULL,
    UNIQUE (survey_id, signature)
);
CREATE TABLE analytics_relation_questions (
    relation_id TEXT    NOT NULL REFERENCES analytics_relations (id) ON DELETE CASCADE,
    position    INTEGER NOT NULL,
    question_id TEXT    NOT NULL,
    PRIMARY KEY (relation_id, position),
    UNIQUE (relation_id, question_id)
);

-- Analytics' own copy of the answers, fed by response.submitted events. The
-- module never reads the response module's tables, so the two can be split
-- into separate services/databases later without touching the domain.
CREATE TABLE analytics_respondents (
    response_id  TEXT PRIMARY KEY,
    survey_id    TEXT NOT NULL,
    submitted_at TEXT NOT NULL
);
CREATE INDEX idx_analytics_respondents_survey ON analytics_respondents (survey_id);

CREATE TABLE analytics_answer_facts (
    response_id TEXT NOT NULL REFERENCES analytics_respondents (response_id) ON DELETE CASCADE,
    survey_id   TEXT NOT NULL,
    question_id TEXT NOT NULL,
    option_id   TEXT NOT NULL,
    PRIMARY KEY (response_id, question_id, option_id)
);
CREATE INDEX idx_analytics_facts_survey_question ON analytics_answer_facts (survey_id, question_id, option_id);

-- Staleness tracking: data_version bumps on every relevant event, the worker
-- rebuilds the projections and records the version it computed.
CREATE TABLE analytics_survey_state (
    survey_id        TEXT PRIMARY KEY,
    data_version     INTEGER NOT NULL DEFAULT 0,
    computed_version INTEGER NOT NULL DEFAULT 0,
    computed_at      TEXT,
    total_responses  INTEGER NOT NULL DEFAULT 0
);

-- Projections: disposable read models, fully rebuilt by the worker.
CREATE TABLE analytics_question_stats (
    survey_id   TEXT    NOT NULL,
    question_id TEXT    NOT NULL,
    option_id   TEXT    NOT NULL,
    selections  INTEGER NOT NULL,
    PRIMARY KEY (survey_id, question_id, option_id)
);
CREATE TABLE analytics_question_totals (
    survey_id   TEXT    NOT NULL,
    question_id TEXT    NOT NULL,
    answered    INTEGER NOT NULL,
    PRIMARY KEY (survey_id, question_id)
);
CREATE TABLE analytics_relation_totals (
    relation_id TEXT PRIMARY KEY REFERENCES analytics_relations (id) ON DELETE CASCADE,
    respondents INTEGER NOT NULL -- respondents who answered every related question
);
CREATE TABLE analytics_relation_segments (
    relation_id TEXT    NOT NULL REFERENCES analytics_relations (id) ON DELETE CASCADE,
    option_id   TEXT    NOT NULL, -- option of the first (segment) question
    respondents INTEGER NOT NULL,
    PRIMARY KEY (relation_id, option_id)
);
CREATE TABLE analytics_relation_cells (
    relation_id TEXT    NOT NULL REFERENCES analytics_relations (id) ON DELETE CASCADE,
    combination TEXT    NOT NULL, -- option ids in relation order, joined with '|'
    respondents INTEGER NOT NULL,
    PRIMARY KEY (relation_id, combination)
);
