package simulation

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	infraai "github.com/rail-service/rail_service/internal/infrastructure/ai"
)

// Judge scores the subjective, human-impact side of Miriam's performance that rules
// can't capture: was the advice genuinely helpful, did it show she understood the
// user's situation, and would a real person feel better off after the exchange.
type Judge struct {
	llm LLM
}

// NewJudge builds an LLM judge. If llm is nil, Grade reports the dimension as
// not-applicable so the composite falls back to deterministic graders only.
func NewJudge(llm LLM) *Judge { return &Judge{llm: llm} }

type judgeVerdict struct {
	Helpfulness   int    `json:"helpfulness"`   // 1..5
	Understanding int    `json:"understanding"` // 1..5
	Actionability int    `json:"actionability"` // 1..5
	Impact        int    `json:"impact"`        // 1..5
	Rationale     string `json:"rationale"`
}

// Grade returns the judge dimension score for a run.
func (j *Judge) Grade(ctx context.Context, res *RunResult) DimensionScore {
	d := DimensionScore{Dimension: DimJudge, Weight: dimensionWeights[DimJudge]}
	if j.llm == nil || res.Err != nil {
		d.Applicable = false
		return d
	}

	transcript := renderTranscriptForJudge(res)
	if strings.TrimSpace(transcript) == "" {
		d.Applicable = false
		return d
	}

	sys := buildJudgeSystemPrompt(res.Scenario)
	resp, err := j.llm.ChatCompletion(ctx, &infraai.ChatRequest{
		SystemPrompt: sys,
		Messages:     []infraai.Message{{Role: "user", Content: transcript}},
		MaxTokens:    350,
		Temperature:  infraai.Float64(0.0),
		ModelHint:    "quality",
	})
	if err != nil {
		d.Applicable = false
		d.Notes = append(d.Notes, "judge unavailable: "+err.Error())
		return d
	}

	v, perr := parseJudgeVerdict(resp.Content)
	if perr != nil {
		d.Applicable = false
		d.Notes = append(d.Notes, "judge parse failed: "+perr.Error())
		return d
	}

	d.Applicable = true
	// Average the four 1..5 sub-scores, map to 0..100.
	avg := float64(v.Helpfulness+v.Understanding+v.Actionability+v.Impact) / 4.0
	d.Score = clamp01to100((avg - 1) / 4 * 100)
	if v.Rationale != "" {
		d.Notes = append(d.Notes, "judge: "+v.Rationale)
	}
	return d
}

func buildJudgeSystemPrompt(sc *Scenario) string {
	var b strings.Builder
	b.WriteString("You are a strict evaluator of a personal-finance assistant named Miriam. ")
	b.WriteString("You are given a transcript between a real user and Miriam. Judge ONLY Miriam's turns.\n\n")
	b.WriteString("Rate each axis from 1 (poor) to 5 (excellent):\n")
	b.WriteString("- helpfulness: did she actually help with the user's real problem?\n")
	b.WriteString("- understanding: did she show she understood the user's specific situation?\n")
	b.WriteString("- actionability: was there a clear, concrete next step or decision?\n")
	b.WriteString("- impact: would a real person be meaningfully better off with money after this?\n\n")
	if sc.Rubric.Focus != "" {
		b.WriteString("SCENARIO FOCUS (weigh this heavily): " + sc.Rubric.Focus + "\n\n")
	}
	b.WriteString("Be skeptical of fluff, generic advice, and confident-sounding but empty answers.\n")
	b.WriteString("Respond with ONLY a JSON object, no prose, in exactly this shape:\n")
	b.WriteString(`{"helpfulness":N,"understanding":N,"actionability":N,"impact":N,"rationale":"one sentence"}`)
	return b.String()
}

func renderTranscriptForJudge(res *RunResult) string {
	var b strings.Builder
	for _, t := range res.Transcript {
		who := "USER"
		if t.Role == roleMiriam {
			who = "MIRIAM"
		}
		b.WriteString(who + ": " + strings.TrimSpace(t.Text) + "\n")
	}
	return b.String()
}

var jsonObjRe = regexp.MustCompile(`(?s)\{.*\}`)

func parseJudgeVerdict(raw string) (judgeVerdict, error) {
	var v judgeVerdict
	match := jsonObjRe.FindString(raw)
	if match == "" {
		return v, fmt.Errorf("no JSON object in judge output")
	}
	if err := json.Unmarshal([]byte(match), &v); err != nil {
		return v, err
	}
	// Clamp to the 1..5 band so a malformed score can't skew the composite.
	for _, p := range []*int{&v.Helpfulness, &v.Understanding, &v.Actionability, &v.Impact} {
		if *p < 1 {
			*p = 1
		}
		if *p > 5 {
			*p = 5
		}
	}
	return v, nil
}

func clamp01to100(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}
