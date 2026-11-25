// Command demo seeds the example survey through the real APIs and prints the
// resulting report. Start surveyd first (with SURVEY_ADMIN_EMAIL/PASSWORD set),
// then run:  go run ./cmd/demo
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"strings"
	"time"
)

var (
	adminURL  = flag.String("admin", "http://127.0.0.1:8081", "admin server base URL")
	publicURL = flag.String("public", "http://127.0.0.1:8080", "public server base URL")
	email     = flag.String("email", envOr("SURVEY_ADMIN_EMAIL", "admin@example.com"), "admin email")
	password  = flag.String("password", envOr("SURVEY_ADMIN_PASSWORD", "change-me-please"), "admin password")
	n         = flag.Int("responses", 40, "number of responses to submit")
)

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

type option struct{ ID, Label string }
type question struct {
	ID, Prompt string
	Options    []option
}
type surveyView struct {
	ID        string
	Questions []question
}

var token string

func call(base, method, path string, body, out any) int {
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, base+path, r)
	req.Header.Set("Content-Type", "application/json")
	if base == *adminURL && token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		fail("%s %s: %v (is surveyd running?)", method, path, err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if out != nil && res.StatusCode < 300 {
		if err := json.Unmarshal(raw, out); err != nil {
			fail("decode %s: %v", path, err)
		}
	}
	if res.StatusCode >= 300 && out != nil {
		fail("%s %s -> %d: %s", method, path, res.StatusCode, raw)
	}
	return res.StatusCode
}

func fail(f string, a ...any) {
	fmt.Fprintf(os.Stderr, "demo: "+f+"\n", a...)
	os.Exit(1)
}

func main() {
	flag.Parse()

	var login struct{ Token string }
	call(*adminURL, "POST", "/api/v1/auth/login", map[string]string{"email": *email, "password": *password}, &login)
	token = login.Token
	fmt.Println("✓ logged in to the admin server")

	no := false
	var created struct{ ID string }
	call(*adminURL, "POST", "/api/v1/surveys", map[string]any{
		"title":       "Toys & you",
		"description": "A tiny example survey.",
		"questions": []map[string]any{
			{"prompt": "What gender are you?", "type": "single_choice", "options": []string{"Male", "Female", "Non-binary", "Prefer not to say"}},
			{"prompt": "Do you like cars?", "type": "single_choice", "options": []string{"Yes", "No"}},
			{"prompt": "Do you like dolls?", "type": "single_choice", "options": []string{"Yes, a lot", "Sometimes", "Not at all"}},
			{"prompt": "Select the amount of toys you usually have", "type": "single_choice", "required": &no, "options": []string{"0-1", "2-3", "4-6", "7+"}},
		},
	}, &created)
	call(*adminURL, "POST", "/api/v1/surveys/"+created.ID+"/publish", nil, &struct{}{})
	fmt.Printf("✓ created and published survey %s\n", created.ID)

	if st := call(*adminURL, "PUT", "/api/v1/surveys/"+created.ID, map[string]any{"title": "x", "questions": []any{}}, nil); st != http.StatusConflict {
		fail("editing a live survey should be refused, got %d", st)
	}
	fmt.Println("✓ editing the live survey is refused (409)")

	var sv surveyView
	call(*publicURL, "GET", "/api/v1/surveys/"+created.ID, nil, &sv)
	q := sv.Questions
	for _, rel := range []struct {
		name string
		qs   []string
	}{
		{"Gender × Cars", []string{q[0].ID, q[1].ID}},
		{"Gender × Dolls", []string{q[0].ID, q[2].ID}},
		{"Gender × Cars × Dolls", []string{q[0].ID, q[1].ID, q[2].ID}},
		{"Gender × Toy count", []string{q[0].ID, q[3].ID}},
	} {
		call(*adminURL, "POST", "/api/v1/surveys/"+created.ID+"/relations", map[string]any{"name": rel.name, "question_ids": rel.qs}, &struct{}{})
		fmt.Printf("✓ defined relation %q\n", rel.name)
	}

	rng := rand.New(rand.NewSource(42))
	pick := func(qq question, weights ...int) string {
		total := 0
		for _, w := range weights {
			total += w
		}
		x := rng.Intn(total)
		for i, w := range weights {
			if x < w {
				return qq.Options[i].ID
			}
			x -= w
		}
		return qq.Options[0].ID
	}
	run := time.Now().UnixNano()
	for i := 0; i < *n; i++ {
		g := pick(q[0], 45, 45, 7, 3)
		carsW, dollsW := []int{50, 50}, []int{33, 33, 34}
		switch g {
		case q[0].Options[0].ID:
			carsW, dollsW = []int{75, 25}, []int{10, 30, 60}
		case q[0].Options[1].ID:
			carsW, dollsW = []int{35, 65}, []int{55, 30, 15}
		}
		answers := []map[string]any{
			{"question_id": q[0].ID, "option_ids": []string{g}},
			{"question_id": q[1].ID, "option_ids": []string{pick(q[1], carsW...)}},
			{"question_id": q[2].ID, "option_ids": []string{pick(q[2], dollsW...)}},
		}
		if rng.Intn(5) > 0 { // ~20% skip the optional question
			answers = append(answers, map[string]any{"question_id": q[3].ID, "option_ids": []string{pick(q[3], 10, 35, 35, 20)}})
		}
		addr := fmt.Sprintf("respondent%d+%d@example.com", i, run)
		if st := call(*publicURL, "POST", "/api/v1/surveys/"+created.ID+"/responses", map[string]any{"email": addr, "answers": answers}, nil); st != http.StatusCreated {
			fail("submit %d -> %d", i, st)
		}
		if i == 0 {
			if st := call(*publicURL, "POST", "/api/v1/surveys/"+created.ID+"/responses", map[string]any{"email": strings.ToUpper(addr), "answers": answers}, nil); st != http.StatusConflict {
				fail("duplicate email should be refused, got %d", st)
			}
			fmt.Println("✓ a second response with the same email is refused (409)")
		}
	}
	fmt.Printf("✓ submitted %d responses\n", *n)

	var rep report
	deadline := time.Now().Add(20 * time.Second)
	for {
		call(*publicURL, "GET", "/api/v1/surveys/"+created.ID+"/results", nil, &rep)
		if !rep.Stale && rep.TotalResponses == *n {
			break
		}
		if time.Now().After(deadline) {
			fail("statistics not ready after 20s (is the worker running?)")
		}
		time.Sleep(300 * time.Millisecond)
	}
	fmt.Printf("✓ worker computed statistics at %s\n\n", rep.ComputedAt)
	print(rep)
	fmt.Printf("\nOpen %s/#/results/%s to see this in the browser.\n", *publicURL, created.ID)
}

type report struct {
	Title          string
	TotalResponses int    `json:"total_responses"`
	ComputedAt     string `json:"computed_at"`
	Stale          bool
	Questions      []struct {
		Number            int
		Prompt            string
		Answered, Skipped int
		Options           []struct {
			Label   string
			Count   int
			Percent float64
		}
	}
	Relations []struct {
		Name        string
		Respondents int
		Excluded    int
		Questions   []struct{ Prompt string }
		Segments    []struct {
			Option      struct{ Label string }
			Respondents int
			Breakdown   []struct {
				Options []struct{ Label string }
				Count   int
				Pct     float64 `json:"percent_of_segment"`
			}
		}
	}
}

func print(r report) {
	fmt.Printf("== %s — %d responses ==\n", r.Title, r.TotalResponses)
	for _, q := range r.Questions {
		fmt.Printf("\n%d. %s  (answered %d, skipped %d)\n", q.Number, q.Prompt, q.Answered, q.Skipped)
		for _, o := range q.Options {
			fmt.Printf("   %-20s %3d  %5.1f%%  %s\n", o.Label, o.Count, o.Percent, strings.Repeat("█", int(o.Percent/4)))
		}
	}
	for _, rel := range r.Relations {
		fmt.Printf("\n-- %s (%d respondents, %d excluded for skipping) --\n", rel.Name, rel.Respondents, rel.Excluded)
		for _, s := range rel.Segments {
			if s.Respondents == 0 {
				continue
			}
			fmt.Printf("   Of %d who answered %q:\n", s.Respondents, s.Option.Label)
			for _, c := range s.Breakdown {
				labels := make([]string, len(c.Options))
				for i, o := range c.Options {
					labels[i] = fmt.Sprintf("%q", o.Label)
				}
				fmt.Printf("      %3d (%5.1f%%) answered %s\n", c.Count, c.Pct, strings.Join(labels, " + "))
			}
		}
	}
}
