package eval

import (
	"slices"

	"github.com/CaliLuke/loom-mcp/v2/trajectory"
)

// ToolEdit inserts or removes a tool from the baseline modal sequence.
type ToolEdit struct {
	Op   string `json:"op"`
	Name string `json:"name"`
}

// ToolSequence describes a modal sequence and its share of available attempts.
type ToolSequence struct {
	Sequence []string `json:"sequence"`
	Count    int      `json:"count"`
	Total    int      `json:"total"`
}

// ToolDelta compares modal sequences using a longest common subsequence edit list.
type ToolDelta struct {
	Baseline  ToolSequence `json:"baseline"`
	Candidate ToolSequence `json:"candidate"`
	Edits     []ToolEdit   `json:"edits"`
}

type sequenceSample struct {
	sequence []string
	repeat   int
}

// toolSequence visits session runs in order and children immediately after their tool.
func toolSequence(session trajectory.Session) []string {
	var sequence []string
	for _, run := range session {
		for _, step := range run.Steps {
			if step.Tool != nil {
				sequence = append(sequence, string(step.Tool.Name))
				sequence = append(sequence, toolSequence(step.Tool.Children)...)
			}
		}
	}
	return sequence
}

// attemptSequences excludes unavailable exports and attempts that never ran.
func attemptSequences(attempts []*StoredAttempt) (ToolSequence, int) {
	var samples []sequenceSample
	unavailable := 0
	for _, a := range attempts {
		if a.Verdict == VerdictNotRun {
			continue
		}
		session, err := a.Session()
		if err != nil || (len(a.Runs) == 0 && a.StorageError != "") {
			unavailable++
			continue
		}
		samples = append(samples, sequenceSample{sequence: toolSequence(session), repeat: a.Repeat})
	}
	return modalSequence(samples), unavailable
}

func modalSequence(samples []sequenceSample) ToolSequence {
	result := ToolSequence{Total: len(samples)}
	earliest := int(^uint(0) >> 1)
	for _, sample := range samples {
		count := 0
		for _, other := range samples {
			if slices.Equal(sample.sequence, other.sequence) {
				count++
			}
		}
		if count > result.Count || (count == result.Count && sample.repeat < earliest) {
			result.Sequence, result.Count, earliest = slices.Clone(sample.sequence), count, sample.repeat
		}
	}
	return result
}

// toolEdits uses deletion first to resolve equal-length LCS paths deterministically.
func toolEdits(base, cand []string) []ToolEdit {
	lengths := make([][]int, len(base)+1)
	for i := range lengths {
		lengths[i] = make([]int, len(cand)+1)
	}
	for i := len(base) - 1; i >= 0; i-- {
		for j := len(cand) - 1; j >= 0; j-- {
			if base[i] == cand[j] {
				lengths[i][j] = 1 + lengths[i+1][j+1]
			} else {
				lengths[i][j] = max(lengths[i+1][j], lengths[i][j+1])
			}
		}
	}
	var edits []ToolEdit
	for i, j := 0, 0; i < len(base) || j < len(cand); {
		switch {
		case i < len(base) && j < len(cand) && base[i] == cand[j]:
			i++
			j++
		case i < len(base) && (j == len(cand) || lengths[i+1][j] >= lengths[i][j+1]):
			edits = append(edits, ToolEdit{Op: "-", Name: base[i]})
			i++
		default:
			edits = append(edits, ToolEdit{Op: "+", Name: cand[j]})
			j++
		}
	}
	return edits
}
