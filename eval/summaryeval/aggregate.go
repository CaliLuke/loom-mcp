package summaryeval

import (
	"maps"
	"slices"
	"time"
)

type (
	// Retention is an entailed fraction, without confidence intervals.
	Retention struct {
		Entailed  int     `json:"entailed"`
		Claims    int     `json:"claims"`
		Retention float64 `json:"retention"`
	}
	// SideRetention includes totals and application/rubric categories.
	SideRetention struct {
		Entailed   int                  `json:"entailed"`
		Claims     int                  `json:"claims"`
		Retention  float64              `json:"retention"`
		Categories map[string]Retention `json:"categories"`
	}
	// VariantAggregate includes only verified, completed paired comparisons.
	VariantAggregate struct {
		OldBytes int           `json:"old_bytes"`
		NewBytes int           `json:"new_bytes"`
		Tokens   int           `json:"tokens"`
		Chunks   int           `json:"chunks"`
		Latency  time.Duration `json:"latency"`
		Variant  string        `json:"variant"`
		Cuts     int           `json:"cuts"`
		Old      SideRetention `json:"old"`
		New      SideRetention `json:"new"`
		Better   int           `json:"better"`
		Worse    int           `json:"worse"`
		Same     int           `json:"same"`
	}
)

// Aggregate pools claim counts for verified cuts; unverified and incomplete lines are excluded.
func Aggregate(lines []Line) []VariantAggregate {
	variants := map[string]*VariantAggregate{}
	for _, line := range lines {
		if line.Verification.Status != verificationVerified || line.NotRun != "" || line.Error != "" || line.Variant == "" {
			continue
		}
		a := variants[line.Variant]
		if a == nil {
			a = &VariantAggregate{Variant: line.Variant, Old: SideRetention{Categories: map[string]Retention{}}, New: SideRetention{Categories: map[string]Retention{}}}
			variants[line.Variant] = a
		}
		a.Cuts++
		a.OldBytes += line.Old.Bytes
		a.NewBytes += line.New.Bytes
		a.Tokens += tokenCount(line.New.Usage) + tokenCount(line.JudgeUsage)
		a.Chunks += line.New.Chunks
		a.Latency += line.New.Latency
		aggregateJudgments(a, line.Judgments)
	}
	result := make([]VariantAggregate, 0, len(variants))
	for _, id := range slices.Sorted(maps.Keys(variants)) {
		result = append(result, *variants[id])
	}
	return result
}

func addRetention(side *SideRetention, j Judgment) {
	side.Claims++
	category := side.Categories[j.Category]
	category.Claims++
	if j.Label == "entailed" {
		side.Entailed++
		category.Entailed++
	}
	side.Retention = float64(side.Entailed) / float64(side.Claims)
	category.Retention = float64(category.Entailed) / float64(category.Claims)
	side.Categories[j.Category] = category
}

func labelRank(label string) int {
	switch label {
	case "entailed":
		return 2
	case "contradicted":
		return 0
	default:
		return 1
	}
}

// aggregateJudgments counts paired labels without assigning intervals.
func aggregateJudgments(a *VariantAggregate, judgments []Judgment) {
	old := map[string]string{}
	for _, j := range judgments {
		if j.Side == sideOld {
			addRetention(&a.Old, j)
			old[j.ClaimID] = j.Label
		}
	}
	for _, j := range judgments {
		if j.Side != sideNew {
			continue
		}
		addRetention(&a.New, j)
		prior, ok := old[j.ClaimID]
		if !ok {
			continue
		}
		switch {
		case labelRank(j.Label) > labelRank(prior):
			a.Better++
		case labelRank(j.Label) < labelRank(prior):
			a.Worse++
		default:
			a.Same++
		}
	}
}
