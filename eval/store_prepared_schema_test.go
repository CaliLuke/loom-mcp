package eval

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"strings"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/expect"
	"github.com/stretchr/testify/require"
)

func TestProbeSchemaRejectsInvalidResults(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*StoredAttempt)
	}{
		{"missing_status", func(a *StoredAttempt) {
			a.Probe = nil
		}},
		{"quality_pass", func(a *StoredAttempt) {
			a.Probe.Quality = "passed"
		}},
		{"execution_unknown", func(a *StoredAttempt) {
			a.Probe.Execution = "unknown"
		}},
		{"delivery_unknown", func(a *StoredAttempt) {
			a.Probe.Delivery = "unknown"
		}},
		{"evidence_unknown", func(a *StoredAttempt) {
			a.Probe.Evidence = "unknown"
		}},
		{"passed_verdict", func(a *StoredAttempt) {
			a.Verdict = VerdictPassed
		}},
		{"failed_verdict", func(a *StoredAttempt) {
			a.Verdict = VerdictFailed
		}},
		{"pending_verdict", func(a *StoredAttempt) {
			a.Verdict = VerdictPending
		}},
		{"quality_checks", func(a *StoredAttempt) {
			a.Checks = []expect.Check{{Name: "quality", Passed: true}}
		}},
		{"quality_scores", func(a *StoredAttempt) {
			a.Scores = []expect.Score{{Name: "quality", Value: 1}}
		}},
		{"quality_claims", func(a *StoredAttempt) {
			a.Claims = []StoredClaim{{ID: "claim"}}
		}},
		{"quality_judgments", func(a *StoredAttempt) {
			a.Judgments = []StoredJudgment{{ClaimID: "claim"}}
		}},
		{"judge_tokens", func(a *StoredAttempt) {
			a.JudgeTokens = 1
		}},
		{"judge_usage", func(a *StoredAttempt) {
			a.JudgeUsage.InputTokens = 1
		}},
		{"not_run_success", func(a *StoredAttempt) {
			a.Probe.Execution = "not_run"
		}},
		{"failed_success", func(a *StoredAttempt) {
			a.Probe.Execution = "failed"
		}},
		{"canceled_success", func(a *StoredAttempt) {
			a.Probe.Execution = "canceled"
		}},
		{"unassessed_error", func(a *StoredAttempt) {
			a.Error = "failed"
		}},
		{"unassessed_harness_error", func(a *StoredAttempt) {
			a.HarnessError = "failed"
		}},
		{"unassessed_without_receipt", func(a *StoredAttempt) {
			a.Prepared = nil
		}},
		{"evaluation_probe_status", func(a *StoredAttempt) {
			a.ExecutionKind = ExecutionEvaluation
		}},
		{"unknown_kind", func(a *StoredAttempt) {
			a.ExecutionKind = "unknown"
		}},
		{"legacy_unassessed", func(a *StoredAttempt) {
			a.ExecutionKind = ""
			a.Probe = nil
			a.Prepared = nil
		}},
		{"invalid_candidate_digest", func(a *StoredAttempt) {
			a.Prepared.CandidateDigest = "wrong"
		}},
		{"missing_identity", func(a *StoredAttempt) {
			a.Prepared.InputSnapshot = ""
		}},
		{"invalid_environment", func(a *StoredAttempt) {
			a.Prepared.Environment.Mode = "mutable"
		}},
		{"invalid_driver_version", func(a *StoredAttempt) {
			a.Prepared.DriverVersion = "\xff"
		}},
		{"missing_bindings", func(a *StoredAttempt) {
			a.Prepared.Bindings = nil
		}},
		{"duplicate_bindings", func(a *StoredAttempt) {
			a.Prepared.Bindings = append(a.Prepared.Bindings, a.Prepared.Bindings[0])
		}},
		{"invalid_build", func(a *StoredAttempt) {
			a.Prepared.Build = &PreparedBuild{}
		}},
		{"invalid_artifact_name", func(a *StoredAttempt) {
			a.Artifacts = []StoredArtifact{{Name: "../escape", MediaType: "text/plain"}}
		}},
		{"invalid_artifact_digest", func(a *StoredAttempt) {
			a.Artifacts = []StoredArtifact{{Name: "output", MediaType: "text/plain", Digest: "wrong"}}
		}},
		{"invalid_artifact_media", func(a *StoredAttempt) {
			a.Artifacts = []StoredArtifact{{Name: "output", MediaType: "bad media"}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := storedProbeFixture().Attempts[0]
			tc.mutate(a)
			data, err := encodeStore(a)
			if tc.name == "invalid_driver_version" {
				require.Error(t, validatePreparedReceipt(*a.Prepared))
				return
			}
			require.NoError(t, err)
			_, err = decodeAttemptLine(data)
			require.Error(t, err)
		})
	}
}

func TestProbeSchemaStatusAxes(t *testing.T) {
	for _, tc := range []struct {
		execution string
		verdict   Verdict
		notRun    StopReason
	}{
		{"succeeded", VerdictUnassessed, ""}, {"succeeded", VerdictError, ""}, {"failed", VerdictError, ""}, {"canceled", VerdictError, ""}, {"not_run", VerdictNotRun, StopCanceled},
	} {
		t.Run(tc.execution+"/"+string(tc.verdict), func(t *testing.T) {
			a := storedProbeFixture().Attempts[0]
			a.Probe.Execution, a.Verdict, a.NotRun = tc.execution, tc.verdict, tc.notRun
			data, err := encodeStore(a)
			require.NoError(t, err)
			_, err = decodeAttemptLine(data)
			require.NoError(t, err)
		})
	}
}

func TestPreparedSchemaVersionIsolation(t *testing.T) {
	legacy := loadReportFixture(t)
	for _, field := range []string{"execution_kind", "prepared", "probe", "artifacts"} {
		t.Run(field, func(t *testing.T) {
			data, err := encodeStore(legacy.Attempts[0])
			require.NoError(t, err)
			var fields map[string]jsontext.Value
			require.NoError(t, json.Unmarshal(data, &fields))
			fields[field] = jsontext.Value("null")
			data, err = encodeStore(fields)
			require.NoError(t, err)
			header, err := encodeStore(attemptsHeader{Format: attemptsFormat, Version: 1, Experiment: legacy.Header.ID})
			require.NoError(t, err)
			_, _, err = readAttempts(strings.NewReader(string(header)+"\n"+string(data)+"\n"), "attempts", &legacy.Header)
			require.Error(t, err)
		})
	}
	for _, version := range []int{0, 1, 3} {
		h := storedProbeFixture().Header
		h.Version = version
		data, err := encodeStore(h)
		require.NoError(t, err)
		_, err = DecodeExperimentHeader(data, h.ID)
		require.Error(t, err)
	}
	for _, mutate := range []func(*ExperimentHeader){
		func(h *ExperimentHeader) {
			h.ExecutionKind = "unknown"
		},
		func(h *ExperimentHeader) {
			h.ExecutionKind = ""
		},
		func(h *ExperimentHeader) {
			h.Parent = reportFixtureID
		},
		func(h *ExperimentHeader) {
			h.Judge = &JudgeInfo{}
		},
		func(h *ExperimentHeader) {
			h.Planned = 2
		},
	} {
		h := storedProbeFixture().Header
		mutate(&h)
		data, err := encodeStore(h)
		require.NoError(t, err)
		_, err = DecodeExperimentHeader(data, h.ID)
		require.Error(t, err)
	}
}

func TestPreparedSchemaMixedLog(t *testing.T) {
	for _, tc := range []struct {
		name    string
		version int
		kind    ExecutionKind
	}{{"version", 1, ExecutionProbe}, {"kind", 2, ExecutionEvaluation}} {
		t.Run(tc.name, func(t *testing.T) {
			e := storedProbeFixture()
			a := e.Attempts[0]
			a.ExecutionKind = tc.kind
			if tc.kind == ExecutionEvaluation {
				a.Probe, a.Verdict = nil, VerdictPassed
			}
			header, err := encodeStore(attemptsHeader{Format: attemptsFormat, Version: tc.version, Experiment: e.Header.ID})
			require.NoError(t, err)
			attempt, err := encodeStore(a)
			require.NoError(t, err)
			_, _, err = readAttempts(strings.NewReader(string(header)+"\n"+string(attempt)+"\n"), "attempts", &e.Header)
			require.Error(t, err)
		})
	}
}

func TestPreparedSchemaLegacyOptionalConfig(t *testing.T) {
	for _, tc := range []struct {
		name, status, fields string
		valid                bool
	}{
		{"omitted_running", "running", "", true},
		{"omitted_complete", "complete", "", true},
		{"omitted_stopped", "stopped", "", true},
		{"null", "running", `,"config":null`, true},
		{"empty_object", "running", `,"config":{}`, true},
		{"legacy_settings", "running", `,"config":{"repeats":1,"concurrency":1}`, true},
		{"prepare_timeout_zero", "running", `,"config":{"prepare_timeout":0}`, false},
		{"cleanup_timeout_null", "running", `,"config":{"cleanup_timeout":null}`, false},
		{"execution_kind_null", "running", `,"execution_kind":null`, false},
		{"unknown_config_member", "running", `,"config":{"unknown":0}`, false},
		{"malformed_repeats", "running", `,"config":{"repeats":"invalid"}`, false},
		{"config_array", "running", `,"config":[]`, false},
		{"config_string", "running", `,"config":"invalid"`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := []byte(`{"format":"loom-mcp.eval.experiment","version":1,"id":"` + reportFixtureID + `","status":"` + tc.status + `"` + tc.fields + `}`)
			header, err := DecodeExperimentHeader(data, reportFixtureID)
			if !tc.valid {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.status, header.Status)
			require.Equal(t, 1, header.Version)
		})
	}
}
