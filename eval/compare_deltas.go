package eval

import "slices"

type claimKey struct{ id, subject, tool string }
type scoreSamples struct {
	values    []float64
	threshold *float64
}

func compareGroup(base, cand reportGroup) GroupDelta {
	g := GroupDelta{Scenario: cand.Scenario, Item: cand.Item, Baseline: base.GroupStats, Candidate: cand.GroupStats}
	g.Status = regression(g.Baseline.Passed, g.Baseline.Ran, g.Candidate.Passed, g.Candidate.Ran)
	sample := failingSample(cand.attempts)
	if g.Status == statusImproved {
		sample = failingSample(base.attempts)
	}
	g.Checks = compareChecks(base.attempts, cand.attempts, sample)
	g.Claims = compareClaims(base.attempts, cand.attempts, sample)
	g.Scores = compareScores(base.attempts, cand.attempts)
	for _, attempts := range [][]*StoredAttempt{base.attempts, cand.attempts} {
		for _, a := range attempts {
			for _, j := range a.Judgments {
				g.Judgments++
				if j.Truncated {
					g.TruncatedJudgments++
				}
				g.Uncalibrated = g.Uncalibrated || j.Uncalibrated
			}
		}
	}
	g.Truncated = g.TruncatedJudgments > 0
	bseq, bu := attemptSequences(base.attempts)
	cseq, cu := attemptSequences(cand.attempts)
	g.Baseline.ToolsUnavailable, g.Candidate.ToolsUnavailable = bu, cu
	if bseq.Total > 0 && cseq.Total > 0 && !slices.Equal(bseq.Sequence, cseq.Sequence) {
		g.Tools = &ToolDelta{Baseline: bseq, Candidate: cseq, Edits: toolEdits(bseq.Sequence, cseq.Sequence)}
	}
	return g
}

func failingSample(attempts []*StoredAttempt) *StoredAttempt {
	var sample *StoredAttempt
	for _, a := range attempts {
		if a.Verdict != VerdictPassed && (sample == nil || a.Repeat < sample.Repeat) {
			sample = a
		}
	}
	return sample
}

func compareChecks(base, cand []*StoredAttempt, sample *StoredAttempt) []CheckDelta {
	b, bn := checkCounts(base)
	c, cn := checkCounts(cand)
	var deltas []CheckDelta
	for _, name := range orderedNames(cn, bn) {
		bc, cc := b[name], c[name]
		if equalFraction(bc.Passed, bc.Total, cc.Passed, cc.Total) {
			continue
		}
		delta := CheckDelta{Name: name, Baseline: bc, Candidate: cc}
		if sample != nil {
			for _, check := range sample.Checks {
				if check.Name == name && !check.Passed {
					delta.Diagnostic = check.Diagnostic
					break
				}
			}
		}
		deltas = append(deltas, delta)
	}
	return deltas
}

func checkCounts(attempts []*StoredAttempt) (map[string]CheckCounts, []string) {
	counts := make(map[string]CheckCounts)
	var names []string
	for _, a := range attempts {
		for _, check := range a.Checks {
			count, ok := counts[check.Name]
			if !ok {
				names = append(names, check.Name)
			}
			count.Total++
			if check.Passed {
				count.Passed++
			}
			counts[check.Name] = count
		}
	}
	return counts, names
}

func compareClaims(base, cand []*StoredAttempt, sample *StoredAttempt) []ClaimDelta {
	b, bn := claimCounts(base)
	c, cn := claimCounts(cand)
	var deltas []ClaimDelta
	for _, key := range orderedNames(cn, bn) {
		bc, cc := b[key], c[key]
		if equalFraction(bc.Entailed, bc.Total, cc.Entailed, cc.Total) {
			continue
		}
		delta := ClaimDelta{ClaimID: key.id, Subject: key.subject, Tool: key.tool, Baseline: bc, Candidate: cc, Uncalibrated: bc.Uncalibrated+cc.Uncalibrated > 0, Truncated: bc.Truncated+cc.Truncated > 0}
		if sample != nil {
			for _, j := range sample.Judgments {
				if judgmentKey(sample, j) == key && (j.Label != reportEntailed || j.Error != "") {
					delta.Rationale = j.Rationale
					if j.Error != "" {
						delta.Rationale = j.Error
					}
					break
				}
			}
		}
		deltas = append(deltas, delta)
	}
	return deltas
}

func claimCounts(attempts []*StoredAttempt) (map[claimKey]ClaimCounts, []claimKey) {
	counts := make(map[claimKey]ClaimCounts)
	var names []claimKey
	for _, a := range attempts {
		for _, j := range a.Judgments {
			key := judgmentKey(a, j)
			count, ok := counts[key]
			if !ok {
				names = append(names, key)
			}
			count.Total++
			if j.Label == reportEntailed && j.Error == "" {
				count.Entailed++
			}
			if j.Error != "" {
				count.Errors++
			}
			if j.Uncalibrated {
				count.Uncalibrated++
			}
			if j.Truncated {
				count.Truncated++
			}
			counts[key] = count
		}
	}
	return counts, names
}

func judgmentKey(a *StoredAttempt, j StoredJudgment) claimKey {
	key := claimKey{id: j.ClaimID, subject: claimSubject(j.Subject)}
	for _, claim := range a.Claims {
		if claim.ID == j.ClaimID {
			key.tool = string(claim.Tool)
			if j.Subject == "" {
				key.subject = claimSubject(claim.Subject)
			}
			break
		}
	}
	return key
}

func compareScores(base, cand []*StoredAttempt) []ScoreDelta {
	b, bn := scoreValues(base)
	c, cn := scoreValues(cand)
	var deltas []ScoreDelta
	for _, name := range orderedNames(cn, bn) {
		bs, bok := b[name]
		cs, cok := c[name]
		bm, cm := medianScore(bs.values), medianScore(cs.values)
		if bok && cok && bm == cm {
			continue
		}
		delta := ScoreDelta{Name: name, Threshold: cs.threshold}
		if bok {
			delta.Baseline = &bm
		}
		if cok {
			delta.Candidate = &cm
		} else {
			delta.Threshold = bs.threshold
		}
		deltas = append(deltas, delta)
	}
	return deltas
}

func scoreValues(attempts []*StoredAttempt) (map[string]scoreSamples, []string) {
	values := make(map[string]scoreSamples)
	var names []string
	for _, a := range attempts {
		for _, score := range a.Scores {
			sample, ok := values[score.Name]
			if !ok {
				names = append(names, score.Name)
				sample.threshold = score.Threshold
			}
			sample.values = append(sample.values, score.Value)
			values[score.Name] = sample
		}
	}
	return values, names
}

// equalFraction distinguishes missing evidence from an observed zero fraction.
func equalFraction(ap, at, bp, bt int) bool {
	if at == 0 || bt == 0 {
		return at == bt
	}
	return int64(ap)*int64(bt) == int64(bp)*int64(at)
}

func orderedNames[T comparable](first, second []T) []T {
	names := slices.Clone(first)
	for _, name := range second {
		if !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	return names
}
