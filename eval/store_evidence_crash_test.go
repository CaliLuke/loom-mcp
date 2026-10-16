package eval

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
)

type evidenceEvalProvider struct {
	ready func() error
}

func TestCaptureEvidenceProcessTermination(t *testing.T) {
	mode := os.Getenv("LOOM_EVAL_EVIDENCE_MODE")
	if mode == "capture" {
		store, err := Open(".")
		require.NoError(t, err)
		var writer *Writer
		provider, err := model.NewClient(evidenceEvalProvider{ready: func() error {
			_, err := fmt.Fprintln(os.Stdout, writer.ID())
			return err
		}})
		require.NoError(t, err)
		cfg := storeConfig()
		cfg.Scenarios[0].Agent = "report"
		cfg.Harness = NewHarnessFunc(func(ctx context.Context, rt *runtime.Runtime) error {
			if err := rt.RegisterModel("report", provider); err != nil {
				return err
			}
			return registerReportTools(ctx, rt)
		}, nil)
		writer, err = store.Begin(t.Context(), cfg, ExperimentMeta{Identity: &Identity{}})
		require.NoError(t, err)
		factory := harnessFactory(cfg)
		cfg.Harness = nil
		cfg.AttemptHarness = writer.CaptureHarness(factory)
		_, err = Run(t.Context(), cfg)
		t.Fatal("blocked capture returned", err)
	}
	if mode == "read" {
		store, err := Open(".")
		require.NoError(t, err)
		id := os.Getenv("LOOM_EVAL_EVIDENCE_ID")
		rows, err := store.ReadEvidence(t.Context(), id)
		require.NoError(t, err)
		require.Len(t, rows, 1)
		assert.Equal(t, AttemptKey{Variant: DefaultVariant, Scenario: "chat", Item: "one", Repeat: 1}, rows[0].Key)
		require.Len(t, rows[0].Snapshot.Records, 1)
		run := rows[0].Snapshot.Records[0].Runs[0]
		require.Len(t, run.Calls, 1)
		assert.Equal(t, runrecord.Incomplete, run.Calls[0].Class)
		assert.Nil(t, run.Calls[0].Completion)
		assert.Equal(t, "hello", run.Calls[0].Request.Messages[0].Parts[0].(model.TextPart).Text)
		experiment, err := store.Load(id)
		require.NoError(t, err)
		assert.Empty(t, experiment.Attempts)
		assert.Equal(t, statusRunning, experiment.Header.Status)
		return
	}
	dir := t.TempDir()
	executable, err := os.Executable()
	require.NoError(t, err)
	require.NoError(t, os.Symlink(executable, filepath.Join(dir, "evidence-test-helper")))
	child := exec.CommandContext(t.Context(), "./evidence-test-helper", "-test.run=^TestCaptureEvidenceProcessTermination$")
	child.Env = append(os.Environ(), "LOOM_EVAL_EVIDENCE_MODE=capture")
	child.Dir = dir
	child.Stderr = os.Stderr
	stdout, err := child.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, child.Start())
	scanner := bufio.NewScanner(stdout)
	require.True(t, scanner.Scan())
	id := scanner.Text()
	require.NoError(t, child.Process.Kill())
	require.Error(t, child.Wait())
	reader := exec.CommandContext(t.Context(), "./evidence-test-helper", "-test.run=^TestCaptureEvidenceProcessTermination$")
	reader.Dir = dir
	reader.Env = append(os.Environ(), "LOOM_EVAL_EVIDENCE_MODE=read", "LOOM_EVAL_EVIDENCE_ID="+id)
	output, err := reader.CombinedOutput()
	require.NoError(t, err, "%s", output)
}

func (p evidenceEvalProvider) Complete(ctx context.Context, _ *model.Request) (*model.Response, error) {
	if err := p.ready(); err != nil {
		return nil, err
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

func (evidenceEvalProvider) Stream(context.Context, *model.Request) (model.Streamer, error) {
	return nil, model.ErrStreamingUnsupported
}
