package domain_test

import (
	"errors"
	"testing"
	"time"

	"github.com/zenstar13/survey-prototype/internal/analytics/domain"
	"github.com/zenstar13/survey-prototype/internal/kernel/errs"
)

var survey = domain.Survey{ID: "s1", Live: true, Questions: []domain.SurveyQuestion{
	{ID: "q1", Prompt: "Gender"}, {ID: "q2", Prompt: "Cars"}, {ID: "q3", Prompt: "Dolls"},
}}

func TestDefineRelation(t *testing.T) {
	r, err := domain.DefineRelation("r1", survey, "", []string{"q1", "q2", "q3"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if r.Name() != "Gender × Cars × Dolls" {
		t.Errorf("unexpected default name %q", r.Name())
	}
	if len(r.PullEvents()) != 1 {
		t.Error("want RelationDefined event")
	}
}

func TestDefineRelationRules(t *testing.T) {
	cases := map[string][]string{
		"too few":  {"q1"},
		"unknown":  {"q1", "zz"},
		"repeated": {"q1", "q1"},
	}
	for name, ids := range cases {
		if _, err := domain.DefineRelation("r1", survey, "", ids, time.Now()); !errors.Is(err, errs.ErrValidation) {
			t.Errorf("%s: want validation error, got %v", name, err)
		}
	}
	draft := survey
	draft.Live = false
	if _, err := domain.DefineRelation("r1", draft, "", []string{"q1", "q2"}, time.Now()); !errors.Is(err, domain.ErrSurveyNotLive) {
		t.Errorf("draft: want ErrSurveyNotLive, got %v", err)
	}
}
