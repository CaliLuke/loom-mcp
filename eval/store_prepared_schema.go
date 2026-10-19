package eval

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

func validateExecutionKind(version int, kind ExecutionKind) error {
	if version == 1 && kind == "" {
		return nil
	}
	if version == 2 && (kind == ExecutionEvaluation || kind == ExecutionProbe) {
		return nil
	}
	return fmt.Errorf("eval: unsupported version %d or execution kind %q", version, kind)
}

// rejectLegacyMembers checks presence, including null and explicitly empty values.
func rejectLegacyMembers(data []byte, names ...string) error {
	var fields map[string]jsontext.Value
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for _, name := range names {
		if _, exists := fields[name]; exists {
			return fmt.Errorf("eval: version 1 cannot contain %s", name)
		}
	}
	return nil
}

func validateStoredPrepared(a *StoredAttempt) error {
	if a.ExecutionKind == "" {
		if a.Prepared != nil || a.Probe != nil || len(a.Artifacts) != 0 || a.Verdict == VerdictUnassessed {
			return errors.New("eval: legacy attempt contains prepared fields")
		}
		return nil
	}
	if err := validateExecutionKind(2, a.ExecutionKind); err != nil {
		return err
	}
	if a.Prepared != nil {
		if err := validatePreparedReceipt(*a.Prepared); err != nil {
			return err
		}
	}
	seen := make(map[string]bool)
	for _, artifact := range a.Artifacts {
		if err := validateArtifactName(artifact.Name, artifact.MediaType); err != nil {
			return err
		}
		if seen[artifact.Name] || !validPreparedDigest(artifact.Digest) || len(artifact.Digest) != 64 || artifact.Size < 0 || a.Prepared == nil {
			return errors.New("eval: invalid retained artifact")
		}
		seen[artifact.Name] = true
	}
	if a.ExecutionKind == ExecutionEvaluation {
		if a.Probe != nil || a.Verdict == VerdictUnassessed {
			return errors.New("eval: evaluation contains probe status or verdict")
		}
		return nil
	}
	return validateStoredProbe(a)
}

func validateStoredProbe(a *StoredAttempt) error {
	p := a.Probe
	if p == nil || p.Quality != probeQualityNotAssessed {
		return errors.New("eval: probe requires unassessed status")
	}
	if len(a.Checks)+len(a.Scores)+len(a.Claims)+len(a.Judgments) != 0 || a.JudgeTokens != 0 || a.JudgeUsage != (model.TokenUsage{}) {
		return errors.New("eval: probe cannot contain quality results")
	}
	switch p.Delivery {
	case probeDeliveryNotDeclared, probeStatusComplete, probeStatusIncomplete:
	default:
		return errors.New("eval: invalid probe delivery")
	}
	switch p.Evidence {
	case probeStatusComplete, probeStatusIncomplete, probeEvidenceProvisional:
	default:
		return errors.New("eval: invalid probe evidence")
	}
	switch p.Execution {
	case probeExecutionNotRun:
		if a.Verdict != VerdictNotRun || a.NotRun == "" {
			return errors.New("eval: not-run probe requires not-run verdict")
		}
	case probeExecutionSucceeded:
		if a.Verdict != VerdictUnassessed && a.Verdict != VerdictError {
			return errors.New("eval: successful probe requires unassessed or error verdict")
		}
		if a.Verdict == VerdictUnassessed && (a.Prepared == nil || a.Error != "" || a.HarnessError != "" || a.NotRun != "") {
			return errors.New("eval: unassessed probe requires successful preparation and execution")
		}
	case probeExecutionFailed, probeExecutionCanceled:
		if a.Verdict != VerdictError || a.NotRun != "" {
			return errors.New("eval: failed probe requires error verdict")
		}
	default:
		return errors.New("eval: invalid probe execution")
	}
	return nil
}
