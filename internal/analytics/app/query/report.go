// Package query holds the analytics module's read side: the survey report
// (overall stats + relation cross tabs) and relation listings.
package query

import (
	"context"
	"math"
	"time"
)

// --- ports -------------------------------------------------------------------

// SurveyDescription is analytics' view of a survey's labels, for rendering.
type SurveyDescription struct {
	ID        string
	Title     string
	Status    string
	Questions []DescribedQuestion
}

type DescribedQuestion struct {
	ID       string
	Number   int
	Prompt   string
	Type     string
	Required bool
	Options  []DescribedOption
}

type DescribedOption struct {
	ID    string
	Label string
}

// Catalog describes surveys (anti-corruption port onto the survey module).
type Catalog interface {
	// Describe returns domain.ErrSurveyNotFound if the survey does not exist,
	// or is a draft and publicOnly is set.
	Describe(ctx context.Context, surveyID string, publicOnly bool) (SurveyDescription, error)
}

// RawStats is the persisted projection of a survey, as counts only.
type RawStats struct {
	Known          bool // the worker has seen this survey
	Stale          bool // newer data exists than this snapshot
	ComputedAt     *time.Time
	TotalResponses int
	Selections     map[string]map[string]int // question id -> option id -> count
	Answered       map[string]int            // question id -> respondents who answered it
	Relations      []RawRelation
}

type RawRelation struct {
	ID          string
	Name        string
	QuestionIDs []string
	CreatedAt   time.Time
	Computed    bool
	Respondents int
	Segments    map[string]int // segment option id -> respondents
	Cells       map[string]int // "opt1|opt2|..." -> respondents
}

type ReadModel interface {
	Stats(ctx context.Context, surveyID string) (RawStats, error)
	StaleSurveys(ctx context.Context, limit int) ([]string, error)
}

// --- report DTOs ---------------------------------------------------------------

type SurveyReport struct {
	SurveyID       string           `json:"survey_id"`
	Title          string           `json:"title"`
	Status         string           `json:"status"`
	TotalResponses int              `json:"total_responses"`
	ComputedAt     *time.Time       `json:"computed_at"`
	Stale          bool             `json:"stale"` // newer responses are still being processed
	Questions      []QuestionStats  `json:"questions"`
	Relations      []RelationReport `json:"relations"`
}

type QuestionStats struct {
	QuestionID string       `json:"question_id"`
	Number     int          `json:"number"`
	Prompt     string       `json:"prompt"`
	Type       string       `json:"type"`
	Required   bool         `json:"required"`
	Answered   int          `json:"answered"`
	Skipped    int          `json:"skipped"`
	Options    []OptionStat `json:"options"`
}

// OptionStat.Percent is relative to the respondents who answered the question
// (for multiple-choice questions the percentages can add up to more than 100).
type OptionStat struct {
	OptionID string  `json:"option_id"`
	Label    string  `json:"label"`
	Count    int     `json:"count"`
	Percent  float64 `json:"percent"`
}

type OptionRef struct {
	QuestionID string `json:"question_id"`
	OptionID   string `json:"option_id"`
	Label      string `json:"label"`
}

type RelationQuestion struct {
	QuestionID string `json:"question_id"`
	Number     int    `json:"number"`
	Prompt     string `json:"prompt"`
}

// RelationReport is the cross tab of a relation. Respondents who skipped any of
// the related (optional) questions are excluded and counted in Excluded.
type RelationReport struct {
	RelationID  string             `json:"relation_id"`
	Name        string             `json:"name"`
	Questions   []RelationQuestion `json:"questions"`
	Computed    bool               `json:"computed"` // false until the worker has processed it
	Respondents int                `json:"respondents"`
	Excluded    int                `json:"excluded"`
	Segments    []Segment          `json:"segments"`
}

// Segment groups the respondents by their answer to the relation's first question.
type Segment struct {
	Option               OptionRef `json:"option"`
	Respondents          int       `json:"respondents"`
	PercentOfRespondents float64   `json:"percent_of_respondents"`
	Breakdown            []Cell    `json:"breakdown"`
}

// Cell is one combination of answers to the remaining questions within a segment.
type Cell struct {
	Options              []OptionRef `json:"options"`
	Count                int         `json:"count"`
	PercentOfSegment     float64     `json:"percent_of_segment"`
	PercentOfRespondents float64     `json:"percent_of_respondents"`
}

// --- queries -------------------------------------------------------------------

type GetSurveyReport struct {
	SurveyID   string
	PublicOnly bool
}

type GetSurveyReportHandler struct {
	catalog Catalog
	rm      ReadModel
}

func NewGetSurveyReportHandler(c Catalog, rm ReadModel) GetSurveyReportHandler {
	return GetSurveyReportHandler{catalog: c, rm: rm}
}

func (h GetSurveyReportHandler) Handle(ctx context.Context, q GetSurveyReport) (SurveyReport, error) {
	desc, err := h.catalog.Describe(ctx, q.SurveyID, q.PublicOnly)
	if err != nil {
		return SurveyReport{}, err
	}
	raw, err := h.rm.Stats(ctx, q.SurveyID)
	if err != nil {
		return SurveyReport{}, err
	}
	return BuildReport(desc, raw), nil
}

// StaleSurveys lists surveys whose statistics need recomputing (for the worker).
type StaleSurveys struct{ Limit int }

type StaleSurveysHandler struct{ rm ReadModel }

func NewStaleSurveysHandler(rm ReadModel) StaleSurveysHandler { return StaleSurveysHandler{rm: rm} }

func (h StaleSurveysHandler) Handle(ctx context.Context, q StaleSurveys) ([]string, error) {
	return h.rm.StaleSurveys(ctx, q.Limit)
}

// BuildReport combines labels and counts into the report, filling in zero
// counts and computing percentages. It is pure, so it is unit-testable.
func BuildReport(desc SurveyDescription, raw RawStats) SurveyReport {
	rep := SurveyReport{
		SurveyID: desc.ID, Title: desc.Title, Status: desc.Status,
		TotalResponses: raw.TotalResponses, ComputedAt: raw.ComputedAt, Stale: raw.Stale,
		Questions: []QuestionStats{}, Relations: []RelationReport{},
	}
	if !raw.Known {
		rep.Stale = desc.Status != "draft"
	}
	total := raw.TotalResponses

	byID := make(map[string]DescribedQuestion, len(desc.Questions))
	for _, q := range desc.Questions {
		byID[q.ID] = q
		answered := raw.Answered[q.ID]
		qs := QuestionStats{
			QuestionID: q.ID, Number: q.Number, Prompt: q.Prompt, Type: q.Type, Required: q.Required,
			Answered: answered, Skipped: max(total-answered, 0), Options: make([]OptionStat, 0, len(q.Options)),
		}
		for _, o := range q.Options {
			c := raw.Selections[q.ID][o.ID]
			qs.Options = append(qs.Options, OptionStat{OptionID: o.ID, Label: o.Label, Count: c, Percent: percent(c, answered)})
		}
		rep.Questions = append(rep.Questions, qs)
	}

	for _, rel := range raw.Relations {
		qs := make([]DescribedQuestion, 0, len(rel.QuestionIDs))
		for _, id := range rel.QuestionIDs {
			if q, ok := byID[id]; ok {
				qs = append(qs, q)
			}
		}
		if len(qs) != len(rel.QuestionIDs) || len(qs) < 2 {
			continue // cannot happen for a live survey; never render half a relation
		}
		rr := RelationReport{RelationID: rel.ID, Name: rel.Name, Computed: rel.Computed, Segments: []Segment{}}
		for _, q := range qs {
			rr.Questions = append(rr.Questions, RelationQuestion{QuestionID: q.ID, Number: q.Number, Prompt: q.Prompt})
		}
		if rel.Computed {
			rr.Respondents = rel.Respondents
			rr.Excluded = max(total-rel.Respondents, 0)
			combos := cartesian(qs[1:])
			for _, o := range qs[0].Options {
				segN := rel.Segments[o.ID]
				seg := Segment{
					Option:               OptionRef{QuestionID: qs[0].ID, OptionID: o.ID, Label: o.Label},
					Respondents:          segN,
					PercentOfRespondents: percent(segN, rel.Respondents),
					Breakdown:            make([]Cell, 0, len(combos)),
				}
				for _, combo := range combos {
					key := o.ID
					for _, ref := range combo {
						key += "|" + ref.OptionID
					}
					c := rel.Cells[key]
					seg.Breakdown = append(seg.Breakdown, Cell{
						Options: combo, Count: c,
						PercentOfSegment: percent(c, segN), PercentOfRespondents: percent(c, rel.Respondents),
					})
				}
				rr.Segments = append(rr.Segments, seg)
			}
		}
		rep.Relations = append(rep.Relations, rr)
	}
	return rep
}

// cartesian returns every combination of one option per question, in order.
func cartesian(qs []DescribedQuestion) [][]OptionRef {
	out := [][]OptionRef{{}}
	for _, q := range qs {
		next := make([][]OptionRef, 0, len(out)*len(q.Options))
		for _, prefix := range out {
			for _, o := range q.Options {
				combo := append(append(make([]OptionRef, 0, len(prefix)+1), prefix...), OptionRef{QuestionID: q.ID, OptionID: o.ID, Label: o.Label})
				next = append(next, combo)
			}
		}
		out = next
	}
	return out
}

// percent rounds to one decimal; 0 when the base is empty.
func percent(n, base int) float64 {
	if base <= 0 {
		return 0
	}
	return math.Round(float64(n)*1000/float64(base)) / 10
}
