package query_test

import (
	"testing"

	"github.com/zenstar13/survey-prototype/internal/analytics/app/query"
)

func TestBuildReportFillsZerosAndPercentages(t *testing.T) {
	desc := query.SurveyDescription{ID: "s", Title: "T", Status: "published", Questions: []query.DescribedQuestion{
		{ID: "g", Number: 1, Prompt: "Gender", Options: []query.DescribedOption{{ID: "m", Label: "Male"}, {ID: "f", Label: "Female"}}},
		{ID: "c", Number: 2, Prompt: "Cars", Options: []query.DescribedOption{{ID: "y", Label: "Yes"}, {ID: "n", Label: "No"}}},
	}}
	raw := query.RawStats{
		Known: true, TotalResponses: 4,
		Selections: map[string]map[string]int{"g": {"m": 3, "f": 1}, "c": {"y": 3}},
		Answered:   map[string]int{"g": 4, "c": 3},
		Relations: []query.RawRelation{{
			ID: "r", Name: "Gender × Cars", QuestionIDs: []string{"g", "c"}, Computed: true, Respondents: 3,
			Segments: map[string]int{"m": 2, "f": 1},
			Cells:    map[string]int{"m|y": 2, "f|y": 1},
		}},
	}
	rep := query.BuildReport(desc, raw)

	if got := rep.Questions[1].Skipped; got != 1 {
		t.Errorf("skipped = %d, want 1", got)
	}
	if got := rep.Questions[1].Options[1]; got.Count != 0 || got.Percent != 0 {
		t.Errorf("zero option not filled: %+v", got)
	}
	rel := rep.Relations[0]
	if rel.Excluded != 1 {
		t.Errorf("excluded = %d, want 1", rel.Excluded)
	}
	male := rel.Segments[0]
	if male.Breakdown[0].Count != 2 || male.Breakdown[0].PercentOfSegment != 100 {
		t.Errorf("male/yes cell wrong: %+v", male.Breakdown[0])
	}
	if male.Breakdown[1].Count != 0 {
		t.Errorf("male/no cell must be filled with 0: %+v", male.Breakdown[1])
	}
	if male.PercentOfRespondents != 66.7 {
		t.Errorf("rounding: got %v", male.PercentOfRespondents)
	}
}
