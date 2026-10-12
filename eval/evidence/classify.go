package evidence

import (
	"cmp"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/callrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
)

type observedCall struct {
	run          string
	seq, attempt int
	fragments    []fragment
}

// Classify reads caller-ordered session records (each may include children).
// Only adopted planner requests establish needs and recall. A result first seen
// after the last adopted planner request cannot count as recalled.
func Classify(records []*runrecord.RunRecord, spec Spec, threshold bool) (Result, error) {
	if err := validate(spec); err != nil {
		return Result{}, err
	}
	calls, runs, err := observe(records)
	if err != nil {
		return Result{}, err
	}
	out, allRecalled := classifyNeeds(calls, spec)
	answer, err := finalText(records)
	if err != nil {
		return Result{}, err
	}
	out.Used = out.NeedFound
	for _, token := range spec.Answer {
		out.Used = out.Used && strings.Contains(answer, token)
	}
	out.UnsupportedClaim = unsupported(answer, spec, calls)
	out.RecallQualifying = threshold && out.NeedFound && out.AbsentWhenNeeded
	out.RecallSuccess = out.RecallQualifying && allRecalled && out.Used
	if err := countEvents(&out, runs); err != nil {
		return Result{}, err
	}
	return out, nil
}
func validate(s Spec) error {
	if len(s.Needs) == 0 {
		return fmt.Errorf("evidence: no needs")
	}
	for id, e := range s.Evidence {
		if (e.Value == "") == (e.Asset == "") {
			return fmt.Errorf("evidence: %s requires exactly one value or asset", id)
		}
		if e.Asset != "" {
			b, err := hex.DecodeString(e.Asset)
			if err != nil || len(b) != 32 || strings.ToLower(e.Asset) != e.Asset {
				return fmt.Errorf("evidence: invalid asset digest %s", id)
			}
		}
	}
	for _, n := range s.Needs {
		if n.Trigger == "" || len(n.Evidence) == 0 {
			return fmt.Errorf("evidence: empty need")
		}
		for _, id := range n.Evidence {
			if _, ok := s.Evidence[id]; !ok {
				return fmt.Errorf("evidence: unknown evidence %s", id)
			}
		}
	}
	return nil
}
func observe(records []*runrecord.RunRecord) ([]observedCall, []runrecord.Run, error) {
	var out []observedCall
	var runs []runrecord.Run
	seen := map[string]bool{}
	for _, record := range records {
		if record == nil {
			return nil, nil, fmt.Errorf("evidence: nil record")
		}
		for _, run := range record.Runs {
			if seen[run.RunID] {
				continue
			}
			seen[run.RunID] = true
			runs = append(runs, run)
			calls := slices.Clone(run.Calls)
			slices.SortFunc(calls, func(a, b runrecord.Call) int {
				if c := cmp.Compare(a.Started.Key.Seq, b.Started.Key.Seq); c != 0 {
					return c
				}
				return cmp.Compare(a.Started.Key.Attempt, b.Started.Key.Attempt)
			})
			for _, c := range calls {
				if c.Class != runrecord.Adopted || c.Activity != callrecord.ActivityPlanner {
					continue
				}
				if c.Request == nil {
					return nil, nil, fmt.Errorf("evidence: adopted call missing request")
				}
				fs, err := fragments(c.Request.Messages)
				if err != nil {
					return nil, nil, err
				}
				out = append(out, observedCall{run.RunID, c.Started.Key.Seq, c.Started.Key.Attempt, fs})
			}
		}
	}
	return out, runs, nil
}
func classifyNeed(calls []observedCall, need Need, evidence map[EvidenceID]Evidence) NeedResult {
	n := NeedResult{Trigger: need.Trigger, Present: map[EvidenceID][]Source{}}
	for i, c := range calls {
		found := false
		for _, f := range c.fragments {
			found = found || strings.Contains(f.text, need.Trigger)
		}
		if !found {
			continue
		}
		n.Found = true
		n.RunID = c.run
		n.Seq = c.seq
		n.Attempt = c.attempt
		for _, id := range need.Evidence {
			for _, f := range c.fragments {
				if contains(f, evidence[id]) && !slices.Contains(n.Present[id], f.source) {
					n.Present[id] = append(n.Present[id], f.source)
				}
			}
			if len(n.Present[id]) == 0 {
				n.Absent = append(n.Absent, id)
				if recalledAfter(calls[i+1:], c.run, evidence[id]) {
					n.Recalled = append(n.Recalled, id)
				}
			}
		}
		return n
	}
	return n
}
func recalledAfter(calls []observedCall, run string, e Evidence) bool {
	for _, c := range calls {
		if c.run != run {
			continue
		}
		for _, f := range c.fragments {
			if f.source == Recall && contains(f, e) {
				return true
			}
		}
	}
	return false
}

func classifyNeeds(calls []observedCall, spec Spec) (Result, bool) {
	out := Result{NeedFound: true}
	sources := map[Source]bool{}
	allRecalled := true
	for _, need := range spec.Needs {
		n := classifyNeed(calls, need, spec.Evidence)
		out.NeedFound = out.NeedFound && n.Found
		out.AbsentWhenNeeded = out.AbsentWhenNeeded || len(n.Absent) > 0
		allRecalled = allRecalled && len(n.Absent) == len(n.Recalled)
		for _, ss := range n.Present {
			for _, s := range ss {
				sources[s] = true
			}
		}
		if len(n.Recalled) > 0 {
			sources[Recall] = true
		}
		out.Needs = append(out.Needs, n)
	}
	for s := range sources {
		out.Sources = append(out.Sources, s)
	}
	slices.Sort(out.Sources)
	return out, allRecalled
}
