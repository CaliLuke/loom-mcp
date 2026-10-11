package eval

import (
	jsonopts "encoding/json"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/expect"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/prompt"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
)

// ExperimentHeader is the versioned summary stored in experiment.json.
type ExperimentHeader struct {
	Format       string                 `json:"format"`
	Version      int                    `json:"version"`
	ID           string                 `json:"id"`
	Label        string                 `json:"label"`
	Parent       string                 `json:"parent"`
	Status       string                 `json:"status"`
	Started      time.Time              `json:"started"`
	Finished     time.Time              `json:"finished"`
	Stop         StopReason             `json:"stop"`
	RunError     string                 `json:"run_error"`
	Source       VCS                    `json:"source"`
	Versions     Versions               `json:"versions"`
	Config       StoredConfig           `json:"config"`
	Scenarios    []ScenarioInfo         `json:"scenarios"`
	Variants     []StoredVariant        `json:"variants"`
	Planned      int                    `json:"planned"`
	Tokens       int                    `json:"tokens"`
	Fingerprints map[string]Fingerprint `json:"fingerprints"`
	Judge        *JudgeInfo             `json:"judge,omitempty"`
}

// StoredConfig contains the reproducible runner settings.
type StoredConfig struct {
	Repeats     int       `json:"repeats"`
	Concurrency int       `json:"concurrency"`
	MaxTokens   int       `json:"max_tokens"`
	Selection   Selection `json:"selection"`
}

// StoredVariant excludes opaque harness options; policy retains its Go field names.
type StoredVariant struct {
	ID     string               `json:"id"`
	Labels map[string]string    `json:"labels"`
	Policy *api.PolicyOverrides `json:"policy"`
}

// Fingerprint summarizes recorded model, prompt, tool and context evidence.
// Context contains sorted unique hashes, including across runs and attempts.
type Fingerprint struct {
	Models       map[model.ModelClass][]string `json:"models"`
	Prompts      []prompt.PromptRef            `json:"prompts"`
	SystemHashes []string                      `json:"system_hashes"`
	ToolHashes   []string                      `json:"tool_hashes"`
	Context      []string                      `json:"context"`
}

// JudgeInfo records judge provenance for schema version 1.
type JudgeInfo struct {
	ModelID        string `json:"model_id"`
	PromptHash     string `json:"prompt_hash"`
	CalibrationKey string `json:"calibration_key"`
	Calibrated     bool   `json:"calibrated"`
}

// StoredClaim retains a claim and its optional judging subject and tool selector.
// Subject and Tool retain the resolved claim and selector.
type StoredClaim struct {
	ID            string      `json:"id"`
	Text          string      `json:"text"`
	Informational bool        `json:"informational"`
	Subject       string      `json:"subject,omitempty"`
	Tool          tools.Ident `json:"tool,omitempty"`
}

// StoredJudgedStep locates a judged step in an attempt's ordered runs.
type StoredJudgedStep struct {
	Run    int         `json:"run"`
	Index  int         `json:"index"`
	Tool   tools.Ident `json:"tool"`
	CallID string      `json:"call_id"`
}

// StoredJudgment retains one claim judgment for schema version 1.
// Step is present only for judgments about a step.
type StoredJudgment struct {
	ClaimID       string            `json:"claim_id"`
	Subject       string            `json:"subject,omitempty"`
	Step          *StoredJudgedStep `json:"step,omitempty"`
	Label         string            `json:"label"`
	Rationale     string            `json:"rationale"`
	Informational bool              `json:"informational"`
	Uncalibrated  bool              `json:"uncalibrated"`
	Truncated     bool              `json:"truncated"`
	Error         string            `json:"error"`
}

// StoredAttempt owns one attempt's durable quality results and run evidence.
type StoredAttempt struct {
	AttemptKey
	Index        int              `json:"index"`
	Key          string           `json:"key"`
	Verdict      Verdict          `json:"verdict"`
	NotRun       StopReason       `json:"not_run"`
	HarnessError string           `json:"harness_error"`
	Error        string           `json:"error"`
	Started      time.Time        `json:"started"`
	Duration     time.Duration    `json:"duration"`
	Usage        model.TokenUsage `json:"usage"`
	Tokens       int              `json:"tokens"`
	JudgeUsage   model.TokenUsage `json:"judge_usage,omitempty,omitzero"`
	JudgeTokens  int              `json:"judge_tokens,omitempty,omitzero"`
	Task         string           `json:"task"`
	Reference    string           `json:"reference"`
	Checks       []expect.Check   `json:"checks"`
	Scores       []expect.Score   `json:"scores"`
	Claims       []StoredClaim    `json:"claims"`
	Judgments    []StoredJudgment `json:"judgments,omitempty"`
	Runs         []StoredRun      `json:"runs"`
	Fingerprint  Fingerprint      `json:"fingerprint"`
	StorageError string           `json:"storage_error,omitempty"`
}

// StoredRun retains all inputs needed to reconstruct the harness trajectory.
type StoredRun struct {
	RunID            string         `json:"run_id"`
	SessionID        string         `json:"session_id"`
	TraceID          string         `json:"trace_id"`
	RunError         string         `json:"run_error"`
	EventsUnverified bool           `json:"events_unverified"`
	Final            *model.Message `json:"final"`
	Export           jsontext.Value `json:"export"`
}

type attemptsHeader struct {
	Format     string `json:"format"`
	Version    int    `json:"version"`
	Experiment string `json:"experiment"`
}

const (
	statusRunning  = "running"
	statusComplete = "complete"
	statusStopped  = "stopped"
)

const experimentFormat = "loom-mcp.eval.experiment"
const attemptsFormat = "loom-mcp.eval.attempts"

func encodeStore(value any) ([]byte, error) {
	data, err := json.Marshal(value, json.Deterministic(true), jsonopts.FormatDurationAsNano(true), json.WithMarshalers(storeMarshalers))
	if err != nil {
		return nil, fmt.Errorf("encode eval store: %w", err)
	}
	raw := jsontext.Value(data)
	if err := raw.Format(jsontext.ReorderRawObjects(true)); err != nil {
		return nil, err
	}
	return raw, nil
}

func decodeStore(data []byte, value any) error {
	return json.Unmarshal(data, value, jsonopts.FormatDurationAsNano(true), json.RejectUnknownMembers(true), json.WithUnmarshalers(storeUnmarshalers))
}

func decodeAttemptLine(data []byte) (*StoredAttempt, error) {
	var a StoredAttempt
	if err := decodeStore(data, &a); err != nil {
		return nil, err
	}
	if a.Index < 0 || !idPattern.MatchString(a.Variant) || !idPattern.MatchString(a.Scenario) || !idPattern.MatchString(a.Item) || a.Repeat < 1 || a.Key != a.AttemptKey.String() {
		return nil, fmt.Errorf("invalid attempt index or key")
	}
	switch a.Verdict {
	case VerdictPassed, VerdictFailed, VerdictError, VerdictPending, VerdictNotRun:
	default:
		return nil, fmt.Errorf("invalid verdict %q", a.Verdict)
	}
	for i, r := range a.Runs {
		if len(r.Export) == 0 || string(r.Export) == "null" {
			if a.StorageError == "" {
				return nil, fmt.Errorf("runs[%d]: missing export", i)
			}
			continue
		}
		rec, err := runrecord.FromExport(r.Export)
		if err != nil {
			return nil, fmt.Errorf("runs[%d].export: %w", i, err)
		}
		if rec.RootRunID != r.RunID {
			return nil, fmt.Errorf("runs[%d]: export root mismatch", i)
		}
	}
	return &a, nil
}

func storedAttempt(r *AttemptRecord) (*StoredAttempt, error) {
	if r == nil {
		return nil, fmt.Errorf("eval: nil attempt")
	}
	a := &StoredAttempt{AttemptKey: r.AttemptKey, Key: r.AttemptKey.String(), Index: r.Index, Verdict: r.Verdict(), NotRun: r.NotRun, HarnessError: errorText(r.HarnessErr), Started: r.Started, Duration: r.Duration, Usage: r.Usage, Tokens: r.Tokens, JudgeUsage: r.JudgeUsage, JudgeTokens: r.JudgeTokens}
	if o := r.Outcome; o != nil {
		a.Task, a.Reference, a.Checks, a.Scores, a.Error = o.Task, o.Reference, o.Checks, o.Scores, errorText(o.Err)
		for _, claim := range o.Claims {
			a.Claims = append(a.Claims, wireClaim(claim))
		}
		for _, judgment := range o.Judgments {
			a.Judgments = append(a.Judgments, storedJudgment(judgment))
		}
		if o.Session != nil {
			for _, result := range o.Session.Runs {
				if result == nil || result.Record == nil {
					continue
				}
				data, err := result.Record.Export().Marshal()
				if err != nil {
					return nil, err
				}
				run := StoredRun{RunID: result.RunID, SessionID: result.SessionID, TraceID: result.TraceID, RunError: errorText(result.RunErr), EventsUnverified: result.EventsUnverified, Export: data}
				if result.Output != nil {
					run.Final = result.Output.Final
				}
				a.Runs = append(a.Runs, run)
				fingerprint, err := recordFingerprint(result.Record)
				if err != nil {
					return nil, err
				}
				a.Fingerprint = mergeFingerprint(a.Fingerprint, fingerprint)
			}
		}
	}
	return a, nil
}

func errorText(err error) string {
	if err != nil {
		return err.Error()
	}
	return ""
}

// storedJudgment converts errors to durable text and owns the optional locator.
func storedJudgment(j Judgment) StoredJudgment {
	stored := StoredJudgment{ClaimID: j.ClaimID, Subject: string(j.Subject), Label: string(j.Label), Rationale: j.Rationale, Informational: j.Informational, Uncalibrated: j.Uncalibrated, Truncated: j.Truncated, Error: errorText(j.Err)}
	if j.Step != nil {
		stored.Step = &StoredJudgedStep{Run: j.Step.Run, Index: j.Step.Index, Tool: j.Step.Tool, CallID: j.Step.CallID}
	}
	return stored
}
