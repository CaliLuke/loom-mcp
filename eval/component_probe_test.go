package eval_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/eval/candidate"
	"github.com/CaliLuke/loom-mcp/v2/eval/testfixture"
	"github.com/CaliLuke/loom-mcp/v2/expect"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
)

const componentSkill = "---\nid: transform\nname: Transform\ndescription: Compute the requested transformation.\n---\nUse components.run_helper with the task input. Return its actual stdout in the answer.\n"

const componentReverseSource = `package main

import (
 "fmt"
 "io"
 "os"
)

func main() {
 input, err := io.ReadAll(os.Stdin)
 if err != nil {
  fmt.Fprintln(os.Stderr, err)
  os.Exit(1)
 }
 runes := []rune(string(input))
 for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
  runes[i], runes[j] = runes[j], runes[i]
 }
 fmt.Print(string(runes))
}
`

const componentWeightedSource = `package main

import (
 "fmt"
 "io"
 "os"
)

func main() {
 input, err := io.ReadAll(os.Stdin)
 if err != nil {
  fmt.Fprintln(os.Stderr, err)
  os.Exit(1)
 }
 total := 0
 for i, r := range []rune(string(input)) {
  total += (i+1)*int(r)
 }
 fmt.Printf("weighted=%d", total)
}
`

func TestComponentProbeExecutesSubmittedSource(t *testing.T) {
	for _, tc := range []struct{ name, source, want string }{
		{"reverse", componentReverseSource, "Computed: moóL\n"},
		{"weighted_runes", componentWeightedSource, "Computed: weighted=1331\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			frozen := frozenComponent(t, tc.source)
			host := testfixture.NewComponentHost(frozen)
			dir := t.TempDir()
			store, err := eval.Open(dir)
			require.NoError(t, err)
			cfg := componentProbeConfig(host)
			writer, err := store.BeginProbe(t.Context(), cfg, eval.ExperimentMeta{Label: tc.name})
			require.NoError(t, err)
			cfg.PreparedWriter = writer
			report, runErr := eval.Probe(t.Context(), cfg)
			require.NoError(t, runErr)
			require.NoError(t, writer.Finish(report, runErr))
			require.Len(t, report.Attempts, 1)
			require.NoError(t, report.Attempts[0].HarnessErr)
			assert.Equal(t, 4, host.ModelCalls())
			require.NotEmpty(t, host.Directory())
			_, err = os.Stat(host.Directory())
			require.ErrorIs(t, err, os.ErrNotExist)

			reopened, err := eval.Open(dir)
			require.NoError(t, err)
			output, err := reopened.ReadArtifact(t.Context(), writer.ID(), cfg.Key, "answer.txt")
			require.NoError(t, err)
			assert.Equal(t, tc.want, string(output))
			source, err := frozen.Read("helper.go")
			require.NoError(t, err)
			assert.Equal(t, tc.source, string(source))
			experiment, err := reopened.Load(writer.ID())
			require.NoError(t, err)
			require.Len(t, experiment.Attempts, 1)
			saved := experiment.Attempts[0]
			require.NotNil(t, saved.Prepared)
			assert.Equal(t, frozen.Digest(), saved.Prepared.CandidateDigest)
			digest := sha256.Sum256([]byte(tc.source))
			assert.Equal(t, hex.EncodeToString(digest[:]), saved.Prepared.SourceSnapshot)
			require.NotNil(t, saved.Prepared.Build)
			assert.Len(t, saved.Prepared.Build.ExecutableDigest, 64)
			assert.Contains(t, saved.Prepared.Build.Compiler, "go version")
			assert.Contains(t, saved.Prepared.Build.Dependencies, "stdlib")
			require.NotNil(t, saved.Probe)
			assert.Equal(t, eval.ProbeStatus{Execution: "succeeded", Delivery: "complete", Evidence: "complete", Quality: "not_assessed"}, *saved.Probe)
			captures, err := reopened.ReadEvidence(t.Context(), writer.ID())
			require.NoError(t, err)
			require.Len(t, captures, 1)
			require.NotNil(t, captures[0].Snapshot)
			require.Len(t, captures[0].Snapshot.Records, 1)
			require.Len(t, captures[0].Snapshot.Records[0].Runs, 1)
			calls := captures[0].Snapshot.Records[0].Runs[0].Calls
			require.Len(t, calls, 4)
			require.NoError(t, agenttest.LastToolResult(tools.Ident("skills.list_skills"), "transform")(calls[1].Request))
			require.NoError(t, agenttest.LastToolResult(tools.Ident("skills.load_skill"), "components.run_helper")(calls[2].Request))
			require.NoError(t, agenttest.LastToolResult(tools.Ident("components.run_helper"), tc.want[len("Computed: "):len(tc.want)-1])(calls[3].Request))
		})
	}
}

func TestComponentProbeInvalidSourceFailsBeforeDispatch(t *testing.T) {
	host := testfixture.NewComponentHost(frozenComponent(t, "package main\nfunc main( broken"))
	store, err := eval.Open(t.TempDir())
	require.NoError(t, err)
	cfg := componentProbeConfig(host)
	writer, err := store.BeginProbe(t.Context(), cfg, eval.ExperimentMeta{Label: "invalid"})
	require.NoError(t, err)
	cfg.PreparedWriter = writer
	report, runErr := eval.Probe(t.Context(), cfg)
	require.NotNil(t, report)
	require.NoError(t, writer.Finish(report, runErr))
	require.Len(t, report.Attempts, 1)
	require.Error(t, report.Attempts[0].HarnessErr)
	assert.Contains(t, report.Attempts[0].HarnessErr.Error(), "compile submitted helper")
	assert.Zero(t, host.ModelCalls())
	require.NotEmpty(t, host.Directory())
	_, err = os.Stat(host.Directory())
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestComponentProbeRejectsUnsupportedBindingBeforeDispatch(t *testing.T) {
	for _, binding := range []candidate.Binding{{ID: "go-helper", Version: "unsupported"}, {ID: "unsupported", Version: "1"}} {
		t.Run(binding.ID+"@"+binding.Version, func(t *testing.T) {
			surface := componentSurface()
			surface.Slots[1].Binding = binding
			host := testfixture.NewComponentHost(frozenComponentOnSurface(t, componentReverseSource, surface))
			store, err := eval.Open(t.TempDir())
			require.NoError(t, err)
			cfg := componentProbeConfig(host)
			writer, err := store.BeginProbe(t.Context(), cfg, eval.ExperimentMeta{Label: "unsupported"})
			require.NoError(t, err)
			cfg.PreparedWriter = writer
			report, runErr := eval.Probe(t.Context(), cfg)
			require.NotNil(t, report)
			require.NoError(t, writer.Finish(report, runErr))
			require.Len(t, report.Attempts, 1)
			require.Error(t, report.Attempts[0].HarnessErr)
			assert.Contains(t, report.Attempts[0].HarnessErr.Error(), "unsupported component binding")
			assert.Zero(t, host.ModelCalls())
		})
	}
}

func TestComponentPreparedConcurrentIsolation(t *testing.T) {
	sourceRoot, err := os.OpenRoot(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, sourceRoot.Close()) })
	require.NoError(t, sourceRoot.WriteFile("helper.go", []byte(componentReverseSource), 0o600))
	source, err := sourceRoot.ReadFile("helper.go")
	require.NoError(t, err)
	hosts := map[string]*testfixture.ComponentHost{
		"reverse":  testfixture.NewComponentHost(frozenComponent(t, string(source))),
		"weighted": testfixture.NewComponentHost(frozenComponent(t, componentWeightedSource)),
	}
	prepared := make(chan struct{}, 2)
	proceed := make(chan struct{})
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	cfg := eval.Config{Concurrency: 2, Variants: []eval.Variant{{ID: "reverse"}, {ID: "weighted"}}, PrepareTimeout: 30 * time.Second, CleanupTimeout: 5 * time.Second,
		Scenarios: []eval.Scenario{{ID: "transform", Agent: "component.solver", Timeout: 30 * time.Second, Items: eval.Items(eval.Item{ID: "one", Input: "Lóom", Task: "Use the installed skill."}), Expect: []expect.Expectation{expect.Completed()}}},
		PrepareAttempt: func(ctx context.Context, info eval.PreparedAttemptInfo) (*eval.PreparedAttempt, error) {
			lease, err := hosts[info.Variant.ID].Prepare(ctx, info)
			if err != nil {
				return lease, err
			}
			prepared <- struct{}{}
			select {
			case <-proceed:
				return lease, nil
			case <-ctx.Done():
				return lease, ctx.Err()
			}
		},
	}
	storeDir := t.TempDir()
	store, err := eval.Open(storeDir)
	require.NoError(t, err)
	writer, err := store.Begin(ctx, cfg, eval.ExperimentMeta{Label: "concurrent source"})
	require.NoError(t, err)
	cfg.PreparedWriter = writer
	type result struct {
		report *eval.Report
		err    error
	}
	done := make(chan result, 1)
	go func() {
		report, err := eval.Run(ctx, cfg)
		done <- result{report: report, err: err}
	}()
	for range 2 {
		select {
		case <-prepared:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	require.NoError(t, sourceRoot.WriteFile("helper.go", []byte("invalid replacement after both preparations"), 0o600))
	close(proceed)
	var completed result
	select {
	case completed = <-done:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	require.NoError(t, completed.err)
	require.NoError(t, writer.Finish(completed.report, completed.err))
	require.Len(t, completed.report.Attempts, 2)
	reopened, err := eval.Open(storeDir)
	require.NoError(t, err)
	for _, attempt := range completed.report.Attempts {
		require.NoError(t, attempt.HarnessErr)
		assert.Equal(t, "one", attempt.Item)
		_, err := os.Stat(hosts[attempt.Variant].Directory())
		require.ErrorIs(t, err, os.ErrNotExist)
		output, err := reopened.ReadArtifact(ctx, writer.ID(), attempt.AttemptKey, "answer.txt")
		require.NoError(t, err)
		assert.Equal(t, map[string]string{"reverse": "Computed: moóL\n", "weighted": "Computed: weighted=1331\n"}[attempt.Variant], string(output))
	}
	captures, err := reopened.ReadEvidence(ctx, writer.ID())
	require.NoError(t, err)
	require.Len(t, captures, 2)
	for _, capture := range captures {
		require.NotNil(t, capture.Snapshot)
		require.Len(t, capture.Snapshot.Records, 1)
		run := capture.Snapshot.Records[0].Runs[0]
		require.Len(t, run.Calls, 4)
		stdout := map[string]string{"reverse": "moóL", "weighted": "weighted=1331"}[capture.Key.Variant]
		assert.NoError(t, agenttest.LastToolResult(tools.Ident("components.run_helper"), stdout)(run.Calls[3].Request))
	}
}

func componentProbeConfig(host *testfixture.ComponentHost) eval.ProbeConfig {
	return eval.ProbeConfig{Key: eval.AttemptKey{Variant: "candidate", Scenario: "transform", Item: "one", Repeat: 1}, Item: eval.Item{ID: "one", Input: "Lóom", Task: "Transform the supplied word using the installed skill."}, Variant: eval.Variant{ID: "candidate"}, Agent: "component.solver", Timeout: 30 * time.Second, PrepareTimeout: 30 * time.Second, CleanupTimeout: 5 * time.Second, PrepareAttempt: host.Prepare}
}

func componentSurface() candidate.Surface {
	return candidate.Surface{ID: "component-fixture", Version: "1", MaxTotalBytes: 16384, Slots: []candidate.Slot{
		{Name: "skills", Activation: candidate.OnDemand, Operations: []candidate.Operation{candidate.Put}, MediaTypes: []string{"text/markdown"}, MaxBytes: 8192, MaxComponents: 1, Binding: candidate.Binding{ID: "skill-files", Version: "1"}},
		{Name: "helpers", Activation: candidate.OnDemand, Operations: []candidate.Operation{candidate.Put}, MediaTypes: []string{"text/x-go"}, MaxBytes: 8192, MaxComponents: 1, Binding: candidate.Binding{ID: "go-helper", Version: "1"}},
	}}
}

func frozenComponent(t *testing.T, source string) *candidate.Frozen {
	t.Helper()
	return frozenComponentOnSurface(t, source, componentSurface())
}

func frozenComponentOnSurface(t *testing.T, source string, surface candidate.Surface) *candidate.Frozen {
	t.Helper()
	baseline := []candidate.Component{{Name: "SKILL.md", Slot: "skills", MediaType: "text/markdown", Data: []byte(componentSkill), Dependencies: []string{"helper.go"}}, {Name: "helper.go", Slot: "helpers", MediaType: "text/x-go", Data: []byte("package main\n\nfunc main() {\n}\n")}}
	store, err := candidate.Open(t.Context(), filepath.Join(t.TempDir(), "candidates"), surface, baseline)
	require.NoError(t, err)
	discovery := store.Discover()
	assert.Equal(t, surface.ID, discovery.Surface.ID)
	draft, err := store.Create(t.Context())
	require.NoError(t, err)
	draft, err = store.Edit(t.Context(), draft.ID, draft.Revision, []candidate.Edit{{Operation: candidate.Put, Component: candidate.Component{Name: "helper.go", Slot: "helpers", MediaType: "text/x-go", Data: []byte(source)}}})
	require.NoError(t, err)
	require.NoError(t, store.Validate(t.Context(), draft.ID, draft.Revision))
	frozen, err := store.Freeze(t.Context(), draft.ID, draft.Revision)
	require.NoError(t, err)
	return frozen
}
