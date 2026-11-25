// Package domain is the survey authoring model.
//
// Survey is the aggregate root; Questions and Options are entities that only
// exist inside it. The aggregate enforces the lifecycle
//
//	draft ──Publish──▶ published ──Close──▶ closed
//
// and the central invariant: once a survey leaves draft, its content (title,
// description, questions, options) can never change.
package domain

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/zenstar13/survey-prototype/internal/kernel/errs"
)

const (
	MaxTitleLength       = 200
	MaxDescriptionLength = 2000
	MaxPromptLength      = 500
	MaxOptionLabelLength = 200
	MaxQuestions         = 100
	MinOptions           = 2
	MaxOptions           = 50
)

var (
	ErrNotFound      = errs.NotFound("survey_not_found", "survey not found")
	ErrNotEditable   = errs.Conflict("survey_not_editable", "only draft surveys can be edited; a published survey is immutable")
	ErrCannotPublish = errs.Conflict("survey_cannot_be_published", "only draft surveys can be published")
	ErrCannotClose   = errs.Conflict("survey_cannot_be_closed", "only published surveys can be closed")
)

// Status is the lifecycle state of a survey.
type Status string

const (
	StatusDraft     Status = "draft"
	StatusPublished Status = "published"
	StatusClosed    Status = "closed"
)

// QuestionType says how many options a respondent may pick.
type QuestionType string

const (
	SingleChoice   QuestionType = "single_choice"   // exactly one option (e.g. "Do you like cars?" Yes/No)
	MultipleChoice QuestionType = "multiple_choice" // one or more options
)

func (t QuestionType) Valid() bool { return t == SingleChoice || t == MultipleChoice }

// OptionSpec / QuestionSpec describe content to build; IDs are chosen by the
// application layer.
type OptionSpec struct {
	ID    string
	Label string
}

type QuestionSpec struct {
	ID       string
	Prompt   string
	Type     QuestionType
	Required bool
	Options  []OptionSpec
}

type Option struct {
	id    string
	label string
}

func (o Option) ID() string    { return o.id }
func (o Option) Label() string { return o.label }

type Question struct {
	id       string
	prompt   string
	qtype    QuestionType
	required bool
	options  []Option
}

func (q Question) ID() string         { return q.id }
func (q Question) Prompt() string     { return q.prompt }
func (q Question) Type() QuestionType { return q.qtype }
func (q Question) Required() bool     { return q.required }
func (q Question) Options() []Option  { return append([]Option(nil), q.options...) }

// Survey is the aggregate root.
type Survey struct {
	id          string
	title       string
	description string
	status      Status
	questions   []Question
	createdAt   time.Time
	updatedAt   time.Time
	publishedAt *time.Time
	closedAt    *time.Time
	version     int

	events []Event
}

// NewDraft creates a survey in draft state after validating its content.
func NewDraft(id, title, description string, questions []QuestionSpec, now time.Time) (*Survey, error) {
	if strings.TrimSpace(id) == "" {
		return nil, errs.Invalid("survey_id_required", "survey id is required")
	}
	c, err := buildContent(title, description, questions)
	if err != nil {
		return nil, err
	}
	s := &Survey{id: id, status: StatusDraft, createdAt: now, updatedAt: now}
	s.apply(c)
	return s, nil
}

// ReviseDraft replaces the whole content of a draft.
func (s *Survey) ReviseDraft(title, description string, questions []QuestionSpec, now time.Time) error {
	if s.status != StatusDraft {
		return ErrNotEditable
	}
	c, err := buildContent(title, description, questions)
	if err != nil {
		return err
	}
	s.apply(c)
	s.updatedAt = now
	return nil
}

// Publish makes the survey live: it starts accepting responses and its content
// is frozen forever.
func (s *Survey) Publish(now time.Time) error {
	if s.status != StatusDraft {
		return ErrCannotPublish
	}
	s.status = StatusPublished
	s.publishedAt = &now
	s.updatedAt = now
	s.events = append(s.events, Published{SurveyID: s.id, At: now})
	return nil
}

// Close stops accepting responses. Results stay available.
func (s *Survey) Close(now time.Time) error {
	if s.status != StatusPublished {
		return ErrCannotClose
	}
	s.status = StatusClosed
	s.closedAt = &now
	s.updatedAt = now
	s.events = append(s.events, Closed{SurveyID: s.id, At: now})
	return nil
}

func (s *Survey) IsAcceptingResponses() bool { return s.status == StatusPublished }

func (s *Survey) ID() string              { return s.id }
func (s *Survey) Title() string           { return s.title }
func (s *Survey) Description() string     { return s.description }
func (s *Survey) Status() Status          { return s.status }
func (s *Survey) Questions() []Question   { return append([]Question(nil), s.questions...) }
func (s *Survey) CreatedAt() time.Time    { return s.createdAt }
func (s *Survey) UpdatedAt() time.Time    { return s.updatedAt }
func (s *Survey) PublishedAt() *time.Time { return s.publishedAt }
func (s *Survey) ClosedAt() *time.Time    { return s.closedAt }
func (s *Survey) Version() int            { return s.version }

// PullEvents returns and clears the domain events recorded since load.
func (s *Survey) PullEvents() []Event {
	e := s.events
	s.events = nil
	return e
}

type content struct {
	title       string
	description string
	questions   []Question
}

func (s *Survey) apply(c content) {
	s.title = c.title
	s.description = c.description
	s.questions = c.questions
}

func buildContent(title, description string, specs []QuestionSpec) (content, error) {
	var v errs.Violations

	title = strings.TrimSpace(title)
	switch {
	case title == "":
		v.Add("title", "is required")
	case utf8.RuneCountInString(title) > MaxTitleLength:
		v.Add("title", fmt.Sprintf("must be at most %d characters", MaxTitleLength))
	}
	description = strings.TrimSpace(description)
	if utf8.RuneCountInString(description) > MaxDescriptionLength {
		v.Add("description", fmt.Sprintf("must be at most %d characters", MaxDescriptionLength))
	}
	switch {
	case len(specs) == 0:
		v.Add("questions", "at least one question is required")
	case len(specs) > MaxQuestions:
		v.Add("questions", fmt.Sprintf("at most %d questions are allowed", MaxQuestions))
	}

	ids := map[string]bool{}
	questions := make([]Question, 0, len(specs))
	for i, qs := range specs {
		path := fmt.Sprintf("questions[%d]", i)
		if qs.ID == "" || ids[qs.ID] {
			v.Add(path+".id", "must be a unique identifier")
		}
		ids[qs.ID] = true

		prompt := strings.TrimSpace(qs.Prompt)
		switch {
		case prompt == "":
			v.Add(path+".prompt", "is required")
		case utf8.RuneCountInString(prompt) > MaxPromptLength:
			v.Add(path+".prompt", fmt.Sprintf("must be at most %d characters", MaxPromptLength))
		}
		if !qs.Type.Valid() {
			v.Add(path+".type", "must be one of: single_choice, multiple_choice")
		}
		switch {
		case len(qs.Options) < MinOptions:
			v.Add(path+".options", fmt.Sprintf("at least %d options are required", MinOptions))
		case len(qs.Options) > MaxOptions:
			v.Add(path+".options", fmt.Sprintf("at most %d options are allowed", MaxOptions))
		}

		labels := map[string]bool{}
		options := make([]Option, 0, len(qs.Options))
		for j, os := range qs.Options {
			opath := fmt.Sprintf("%s.options[%d]", path, j)
			if os.ID == "" || ids[os.ID] {
				v.Add(opath+".id", "must be a unique identifier")
			}
			ids[os.ID] = true
			label := strings.TrimSpace(os.Label)
			key := strings.ToLower(label)
			switch {
			case label == "":
				v.Add(opath, "label is required")
			case utf8.RuneCountInString(label) > MaxOptionLabelLength:
				v.Add(opath, fmt.Sprintf("label must be at most %d characters", MaxOptionLabelLength))
			case labels[key]:
				v.Add(opath, "duplicate option label within the question")
			}
			labels[key] = true
			options = append(options, Option{id: os.ID, label: label})
		}
		questions = append(questions, Question{id: qs.ID, prompt: prompt, qtype: qs.Type, required: qs.Required, options: options})
	}

	if err := v.Err(); err != nil {
		return content{}, err
	}
	return content{title: title, description: description, questions: questions}, nil
}

// Snapshot is the persisted form of a survey, used only by repositories to
// rebuild the aggregate. It is trusted: no validation is re-run.
type Snapshot struct {
	ID          string
	Title       string
	Description string
	Status      Status
	Questions   []QuestionSpec
	CreatedAt   time.Time
	UpdatedAt   time.Time
	PublishedAt *time.Time
	ClosedAt    *time.Time
	Version     int
}

func Reconstitute(sn Snapshot) *Survey {
	qs := make([]Question, 0, len(sn.Questions))
	for _, q := range sn.Questions {
		opts := make([]Option, 0, len(q.Options))
		for _, o := range q.Options {
			opts = append(opts, Option{id: o.ID, label: o.Label})
		}
		qs = append(qs, Question{id: q.ID, prompt: q.Prompt, qtype: q.Type, required: q.Required, options: opts})
	}
	return &Survey{
		id: sn.ID, title: sn.Title, description: sn.Description, status: sn.Status, questions: qs,
		createdAt: sn.CreatedAt, updatedAt: sn.UpdatedAt, publishedAt: sn.PublishedAt, closedAt: sn.ClosedAt,
		version: sn.Version,
	}
}
