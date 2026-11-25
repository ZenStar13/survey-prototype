package domain_test

import (
	"errors"
	"testing"
	"time"

	"github.com/zenstar13/survey-prototype/internal/kernel/errs"
	"github.com/zenstar13/survey-prototype/internal/survey/domain"
)

var now = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func questions() []domain.QuestionSpec {
	return []domain.QuestionSpec{
		{ID: "q1", Prompt: "What gender are you?", Type: domain.SingleChoice, Required: true,
			Options: []domain.OptionSpec{{ID: "o1", Label: "Male"}, {ID: "o2", Label: "Female"}}},
		{ID: "q2", Prompt: "Do you like cars?", Type: domain.SingleChoice, Required: true,
			Options: []domain.OptionSpec{{ID: "o3", Label: "Yes"}, {ID: "o4", Label: "No"}}},
	}
}

func TestNewDraftValidatesContent(t *testing.T) {
	_, err := domain.NewDraft("s1", " ", "", []domain.QuestionSpec{
		{ID: "q1", Prompt: "", Type: "free_text", Options: []domain.OptionSpec{{ID: "o1", Label: "A"}, {ID: "o2", Label: "a"}}},
	}, now)
	if !errors.Is(err, errs.ErrValidation) {
		t.Fatalf("want validation error, got %v", err)
	}
	var e *errs.Error
	errors.As(err, &e)
	for _, f := range []string{"title", "questions[0].prompt", "questions[0].type", "questions[0].options[1]"} {
		if _, ok := e.Fields[f]; !ok {
			t.Errorf("missing violation for %s in %v", f, e.Fields)
		}
	}
}

func TestPublishedSurveyIsImmutable(t *testing.T) {
	s, err := domain.NewDraft("s1", "Toys", "", questions(), now)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ReviseDraft("Toys v2", "", questions(), now); err != nil {
		t.Fatalf("drafts are editable: %v", err)
	}
	if err := s.Publish(now); err != nil {
		t.Fatal(err)
	}
	if !s.IsAcceptingResponses() {
		t.Fatal("published survey must accept responses")
	}
	if err := s.ReviseDraft("Hacked", "", questions(), now); !errors.Is(err, domain.ErrNotEditable) {
		t.Fatalf("want ErrNotEditable, got %v", err)
	}
	if err := s.Publish(now); !errors.Is(err, domain.ErrCannotPublish) {
		t.Fatalf("want ErrCannotPublish, got %v", err)
	}
	if ev := s.PullEvents(); len(ev) != 1 {
		t.Fatalf("want 1 Published event, got %d", len(ev))
	}
}

func TestCloseLifecycle(t *testing.T) {
	s, _ := domain.NewDraft("s1", "Toys", "", questions(), now)
	if err := s.Close(now); !errors.Is(err, domain.ErrCannotClose) {
		t.Fatalf("draft cannot be closed, got %v", err)
	}
	_ = s.Publish(now)
	if err := s.Close(now); err != nil {
		t.Fatal(err)
	}
	if s.IsAcceptingResponses() {
		t.Fatal("closed survey must not accept responses")
	}
}
