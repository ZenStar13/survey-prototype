package domain

import "time"

// Event is a domain event raised by the Survey aggregate.
type Event interface{ isSurveyEvent() }

type Published struct {
	SurveyID string
	At       time.Time
}

type Closed struct {
	SurveyID string
	At       time.Time
}

func (Published) isSurveyEvent() {}
func (Closed) isSurveyEvent()    {}
