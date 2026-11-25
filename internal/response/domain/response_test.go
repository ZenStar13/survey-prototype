package domain_test

import (
	"errors"
	"testing"
	"time"

	"github.com/zenstar13/survey-prototype/internal/kernel/errs"
	"github.com/zenstar13/survey-prototype/internal/response/domain"
)

var now = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func form() domain.Form {
	return domain.Form{SurveyID: "s1", AcceptingResponses: true, Questions: []domain.FormQuestion{
		{ID: "gender", Required: true, OptionIDs: []string{"m", "f"}},
		{ID: "toys", Required: false, AllowsMultiple: true, OptionIDs: []string{"cars", "dolls", "lego"}},
	}}
}

func fields(t *testing.T, err error) map[string]string {
	t.Helper()
	var e *errs.Error
	if !errors.As(err, &e) || !errors.Is(err, errs.ErrValidation) {
		t.Fatalf("want validation error, got %v", err)
	}
	return e.Fields
}

func TestSubmitValid(t *testing.T) {
	r, err := domain.Submit("r1", form(), "  Ann@Example.COM ", []domain.AnswerInput{
		{QuestionID: "toys", OptionIDs: []string{"cars", "lego"}},
		{QuestionID: "gender", OptionIDs: []string{"f"}},
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if r.Email().String() != "ann@example.com" {
		t.Errorf("email not normalised: %q", r.Email())
	}
	if a := r.Answers(); len(a) != 2 || a[0].QuestionID() != "gender" {
		t.Errorf("answers must follow survey order: %+v", a)
	}
}

func TestOptionalQuestionMayBeSkipped(t *testing.T) {
	r, err := domain.Submit("r1", form(), "a@b.co", []domain.AnswerInput{{QuestionID: "gender", OptionIDs: []string{"m"}}}, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Answers()) != 1 {
		t.Fatal("skipped optional question must not produce an answer")
	}
}

func TestSubmitReportsAllViolations(t *testing.T) {
	_, err := domain.Submit("r1", form(), "not-an-email", []domain.AnswerInput{
		{QuestionID: "toys", OptionIDs: []string{"cars", "bikes"}},
		{QuestionID: "nope", OptionIDs: []string{"x"}},
	}, now)
	f := fields(t, err)
	for _, k := range []string{"email", "answers.gender", "answers.toys", "answers[1].question_id"} {
		if _, ok := f[k]; !ok {
			t.Errorf("missing violation %s in %v", k, f)
		}
	}
}

func TestSingleChoiceAcceptsOneOption(t *testing.T) {
	_, err := domain.Submit("r1", form(), "a@b.co", []domain.AnswerInput{{QuestionID: "gender", OptionIDs: []string{"m", "f"}}}, now)
	if _, ok := fields(t, err)["answers.gender"]; !ok {
		t.Fatal("expected violation on single choice")
	}
}

func TestClosedSurveyRejects(t *testing.T) {
	f := form()
	f.AcceptingResponses = false
	_, err := domain.Submit("r1", f, "a@b.co", nil, now)
	if !errors.Is(err, domain.ErrSurveyNotAccepting) {
		t.Fatalf("want ErrSurveyNotAccepting, got %v", err)
	}
}
