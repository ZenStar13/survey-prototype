-- Module: response (answering).
CREATE TABLE responses (
    id           TEXT PRIMARY KEY,
    survey_id    TEXT NOT NULL REFERENCES surveys (id),
    email        TEXT NOT NULL, -- normalised (trimmed, lower-cased)
    submitted_at TEXT NOT NULL,
    UNIQUE (survey_id, email)   -- one response per email per survey
);

-- One row per selected option. A skipped optional question has no rows.
CREATE TABLE response_answers (
    response_id TEXT NOT NULL REFERENCES responses (id) ON DELETE CASCADE,
    question_id TEXT NOT NULL REFERENCES survey_questions (id),
    option_id   TEXT NOT NULL,
    PRIMARY KEY (response_id, question_id, option_id),
    -- the option must belong to the answered question
    FOREIGN KEY (option_id, question_id) REFERENCES survey_question_options (id, question_id)
);
CREATE INDEX idx_response_answers_question ON response_answers (question_id, option_id);

CREATE TRIGGER trg_responses_only_for_published_surveys
BEFORE INSERT ON responses
WHEN (SELECT status FROM surveys WHERE id = NEW.survey_id) IS NOT 'published'
BEGIN SELECT RAISE(ABORT, 'survey is not accepting responses'); END;
