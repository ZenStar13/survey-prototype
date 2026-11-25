-- Module: survey (authoring). Owner of surveys, questions and options.
CREATE TABLE surveys (
    id           TEXT PRIMARY KEY,
    title        TEXT    NOT NULL,
    description  TEXT    NOT NULL DEFAULT '',
    status       TEXT    NOT NULL CHECK (status IN ('draft', 'published', 'closed')),
    version      INTEGER NOT NULL DEFAULT 1,
    created_at   TEXT    NOT NULL,
    updated_at   TEXT    NOT NULL,
    published_at TEXT,
    closed_at    TEXT
);
CREATE INDEX idx_surveys_status ON surveys (status);

CREATE TABLE survey_questions (
    id        TEXT PRIMARY KEY,
    survey_id TEXT    NOT NULL REFERENCES surveys (id) ON DELETE CASCADE,
    position  INTEGER NOT NULL,
    prompt    TEXT    NOT NULL,
    type      TEXT    NOT NULL CHECK (type IN ('single_choice', 'multiple_choice')),
    required  INTEGER NOT NULL CHECK (required IN (0, 1)),
    UNIQUE (survey_id, position)
);

CREATE TABLE survey_question_options (
    id          TEXT PRIMARY KEY,
    question_id TEXT    NOT NULL REFERENCES survey_questions (id) ON DELETE CASCADE,
    position    INTEGER NOT NULL,
    label       TEXT    NOT NULL,
    UNIQUE (question_id, position),
    UNIQUE (id, question_id) -- target of the composite FK in response_answers
);

-- Defence in depth: the Survey aggregate already refuses to edit a survey that
-- is no longer a draft. These triggers make the database refuse it as well, so
-- no future code path (or manual SQL) can mutate a live survey.
CREATE TRIGGER trg_survey_questions_no_insert_when_live
BEFORE INSERT ON survey_questions
WHEN (SELECT status FROM surveys WHERE id = NEW.survey_id) <> 'draft'
BEGIN SELECT RAISE(ABORT, 'survey is not a draft: its questions are immutable'); END;

CREATE TRIGGER trg_survey_questions_no_update_when_live
BEFORE UPDATE ON survey_questions
WHEN (SELECT status FROM surveys WHERE id = OLD.survey_id) <> 'draft'
BEGIN SELECT RAISE(ABORT, 'survey is not a draft: its questions are immutable'); END;

CREATE TRIGGER trg_survey_questions_no_delete_when_live
BEFORE DELETE ON survey_questions
WHEN (SELECT status FROM surveys WHERE id = OLD.survey_id) <> 'draft'
BEGIN SELECT RAISE(ABORT, 'survey is not a draft: its questions are immutable'); END;

CREATE TRIGGER trg_survey_options_no_insert_when_live
BEFORE INSERT ON survey_question_options
WHEN (SELECT s.status FROM surveys s JOIN survey_questions q ON q.survey_id = s.id WHERE q.id = NEW.question_id) <> 'draft'
BEGIN SELECT RAISE(ABORT, 'survey is not a draft: its options are immutable'); END;

CREATE TRIGGER trg_survey_options_no_update_when_live
BEFORE UPDATE ON survey_question_options
WHEN (SELECT s.status FROM surveys s JOIN survey_questions q ON q.survey_id = s.id WHERE q.id = OLD.question_id) <> 'draft'
BEGIN SELECT RAISE(ABORT, 'survey is not a draft: its options are immutable'); END;

CREATE TRIGGER trg_survey_options_no_delete_when_live
BEFORE DELETE ON survey_question_options
WHEN (SELECT s.status FROM surveys s JOIN survey_questions q ON q.survey_id = s.id WHERE q.id = OLD.question_id) <> 'draft'
BEGIN SELECT RAISE(ABORT, 'survey is not a draft: its options are immutable'); END;

CREATE TRIGGER trg_surveys_content_immutable_when_live
BEFORE UPDATE OF title, description ON surveys
WHEN OLD.status <> 'draft' AND (NEW.title IS NOT OLD.title OR NEW.description IS NOT OLD.description)
BEGIN SELECT RAISE(ABORT, 'survey is not a draft: its content is immutable'); END;

CREATE TRIGGER trg_surveys_status_moves_forward
BEFORE UPDATE OF status ON surveys
WHEN NOT (NEW.status = OLD.status
          OR (OLD.status = 'draft' AND NEW.status = 'published')
          OR (OLD.status = 'published' AND NEW.status = 'closed'))
BEGIN SELECT RAISE(ABORT, 'invalid survey status transition'); END;

CREATE TRIGGER trg_surveys_no_delete_when_live
BEFORE DELETE ON surveys
WHEN OLD.status <> 'draft'
BEGIN SELECT RAISE(ABORT, 'only draft surveys can be deleted'); END;
