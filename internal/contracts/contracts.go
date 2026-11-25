// Package contracts is the published language between modules: the topics and
// payloads of integration events carried by the outbox.
//
// Payloads are versioned (".v1") and deliberately minimal. In particular
// response.submitted carries NO email address: personal data stays inside the
// response module.
package contracts

import "time"

const (
	TopicSurveyPublished   = "survey.published.v1"
	TopicSurveyClosed      = "survey.closed.v1"
	TopicResponseSubmitted = "response.submitted.v1"
	// Analytics-internal events, routed through the outbox like the others so
	// that statistics are recomputed asynchronously.
	TopicRelationDefined = "analytics.relation_defined.v1"
	TopicRelationRemoved = "analytics.relation_removed.v1"
)

type SurveyPublished struct {
	SurveyID    string    `json:"survey_id"`
	PublishedAt time.Time `json:"published_at"`
}

type SurveyClosed struct {
	SurveyID string    `json:"survey_id"`
	ClosedAt time.Time `json:"closed_at"`
}

type ResponseSubmitted struct {
	ResponseID  string           `json:"response_id"`
	SurveyID    string           `json:"survey_id"`
	SubmittedAt time.Time        `json:"submitted_at"`
	Answers     []AnswerSelected `json:"answers"`
}

type AnswerSelected struct {
	QuestionID string   `json:"question_id"`
	OptionIDs  []string `json:"option_ids"`
}

type RelationChanged struct {
	RelationID string    `json:"relation_id"`
	SurveyID   string    `json:"survey_id"`
	At         time.Time `json:"at"`
}
