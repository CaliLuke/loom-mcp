package eval

import "fmt"

func comparisonWarnings(base, cand Side, candidateAttempts []*StoredAttempt, parent *Experiment) ([]Warning, error) {
	bh, ch := base.Experiment.Header, cand.Experiment.Header
	var warnings []Warning
	warnings = append(warnings, datasetWarnings(bh, ch)...)
	same, err := equalAgent(base, cand)
	if err != nil {
		return nil, err
	}
	if same {
		warnings = append(warnings, Warning{Code: "no_agent_change", Message: "recorded agent fingerprints, models and policy are identical"})
	}
	if judgeChanged(bh.Judge, ch.Judge) {
		warnings = append(warnings, Warning{Code: "judge_changed", Message: "judge model or prompt changed"})
	}
	uncalibrated := judgeUncalibrated(bh.Judge) || judgeUncalibrated(ch.Judge)
	judgmentUncalibrated, truncated := comparisonJudgmentFlags(base, cand, candidateAttempts)
	uncalibrated = uncalibrated || judgmentUncalibrated
	if uncalibrated {
		warnings = append(warnings, Warning{Code: "judge_uncalibrated", Message: "uncalibrated judgments qualify these results"})
	}
	if truncated {
		warnings = append(warnings, Warning{Code: "judgments_truncated", Message: "some judgments used truncated subjects"})
	}
	experiments := []*Experiment{base.Experiment, cand.Experiment}
	if parent != nil && !sameExperiment(parent, base.Experiment) && !sameExperiment(parent, cand.Experiment) {
		experiments = append(experiments, parent)
	}
	for _, e := range experiments {
		if e.Header.Status == statusRunning || e.TruncatedTail {
			warnings = append(warnings, Warning{Code: "incomplete", Message: fmt.Sprintf("experiment %s is running or has a truncated tail", e.Header.ID)})
		}
	}
	return warnings, nil
}

func equalAgent(base, cand Side) (bool, error) {
	b, err := agentEvidence(base)
	if err != nil {
		return false, err
	}
	c, err := agentEvidence(cand)
	if err != nil {
		return false, err
	}
	return string(b) == string(c), nil
}

func agentEvidence(side Side) ([]byte, error) {
	for _, v := range side.Experiment.Header.Variants {
		if v.ID == side.Variant {
			// Labels and variant IDs do not describe the agent's recorded behavior.
			return encodeStore(struct {
				Fingerprint Fingerprint `json:"fingerprint"`
				Policy      any         `json:"policy"`
			}{Fingerprint: side.Experiment.Header.Fingerprints[side.Variant], Policy: v.Policy})
		}
	}
	return nil, fmt.Errorf("eval: unknown variant %q", side.Variant)
}

func judgeUncalibrated(judge *JudgeInfo) bool {
	return judge != nil && !judge.Calibrated
}
func judgeChanged(base, cand *JudgeInfo) bool {
	if base == nil || cand == nil {
		return base != cand
	}
	return base.ModelID != cand.ModelID || base.PromptHash != cand.PromptHash
}

func datasetWarnings(bh, ch ExperimentHeader) []Warning {
	var warnings []Warning
	for _, cs := range ch.Scenarios {
		for _, bs := range bh.Scenarios {
			if cs.ID == bs.ID && cs.DatasetHash != bs.DatasetHash {
				warnings = append(warnings, Warning{Code: "dataset_changed", Message: "dataset changed for " + cs.ID})
			}
		}
	}
	return warnings
}

func comparisonJudgmentFlags(base, cand Side, candidateAttempts []*StoredAttempt) (bool, bool) {
	uncalibrated, truncated := false, false
	for i, attempts := range [][]*StoredAttempt{base.Experiment.Attempts, candidateAttempts} {
		variant := base.Variant
		if i == 1 {
			variant = cand.Variant
		}
		for _, a := range attempts {
			if a.Variant != variant {
				continue
			}
			for _, j := range a.Judgments {
				uncalibrated = uncalibrated || j.Uncalibrated
				truncated = truncated || j.Truncated
			}
		}
	}
	return uncalibrated, truncated
}
