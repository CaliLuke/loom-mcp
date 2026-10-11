package agenttest_test

import (
	"context"
	"fmt"
	goruntime "runtime"
	"strings"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/expect"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type evalTB struct {
	testing.TB
	diagnostics []string
	logs        []string
	failed      bool
	skipped     bool
}

type inspectClient struct {
	model.Client
	complete, stream int
	callInfo         bool
}

type rawCompleteClient struct {
	model.Client
	provider model.Provider
}

func TestDecideCompleteAndStream(t *testing.T) {
	responses := make([]*model.Response, 0, 2)
	for _, stream := range []bool{false, true} {
		client, script := agenttest.ScriptClient(t, agenttest.Respond(agenttest.RawToolCall("echo.echo", rawjson.Message(`{"value":"hello"}`))))
		inspector := &inspectClient{Client: client}
		r, err := agenttest.NewRequest("registered", &model.Request{Model: "provider", Stream: stream, Tools: []*model.ToolDefinition{{Name: "echo.echo", InputSchema: map[string]any{"type": "object"}}}, Messages: agenttest.User("hello")})
		require.NoError(t, err)
		d := agenttest.Decide(t, inspector, r)
		assert.Equal(t, "registered", d.ModelID)
		assert.Positive(t, d.Latency)
		assert.False(t, inspector.callInfo)
		assert.Equal(t, stream, inspector.stream == 1)
		assert.Equal(t, !stream, inspector.complete == 1)
		assert.Equal(t, stream, script.Requests()[0].Stream)
		expect.Require(t, expect.Calls(d.Calls), expect.Call(echoTool(), func(p any) error {
			if p.(map[string]any)["value"] != "hello" {
				return fmt.Errorf("unexpected payload: %v", p)
			}
			return nil
		}, nil))
		responses = append(responses, d.Response)
	}
	assert.Equal(t, responses[0], responses[1])
}

func TestDecideErrorsFailTest(t *testing.T) {
	r, err := agenttest.NewRequest("id", &model.Request{})
	require.NoError(t, err)
	bad, err := agenttest.NewScript(agenttest.Respond(agenttest.RawToolCall("absent", rawjson.Message(`{}`))))
	require.NoError(t, err)
	for _, tc := range []struct {
		name       string
		client     model.Client
		req        agenttest.Request
		diagnostic string
	}{
		{"nil", nil, r, "model client is nil; is the ID registered?"},
		{"deferred", bad.Client(), r.WithUserMessage("missing"), "no eligible user message"},
		{"identity", rawCompleteClient{Client: bad.Client(), provider: bad}, r, "tool_identity"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tb := &evalTB{TB: t}
			runTB(func() {
				agenttest.Decide(tb, tc.client, tc.req)
			})
			assert.True(t, tb.failed)
			assert.Contains(t, strings.Join(tb.diagnostics, "\n"), tc.diagnostic)
		})
	}
}

func runTB(fn func()) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	<-done
}

func (t *evalTB) Error(args ...any) {
	t.failed = true
	t.diagnostics = append(t.diagnostics, fmt.Sprint(args...))
}
func (t *evalTB) Errorf(format string, args ...any) {
	t.Error(fmt.Sprintf(format, args...))
}
func (t *evalTB) Fail() {
	t.failed = true
}
func (t *evalTB) Failed() bool {
	return t.failed
}
func (t *evalTB) FailNow() {
	t.Fail()
	goruntime.Goexit()
}
func (t *evalTB) Fatal(args ...any) {
	t.Error(args...)
	goruntime.Goexit()
}
func (t *evalTB) Fatalf(format string, args ...any) {
	t.Error(fmt.Sprintf(format, args...))
	goruntime.Goexit()
}
func (t *evalTB) Skip(args ...any) {
	t.skipped = true
	t.diagnostics = append(t.diagnostics, fmt.Sprint(args...))
	goruntime.Goexit()
}
func (t *evalTB) Skipf(format string, args ...any) {
	t.Skip(fmt.Sprintf(format, args...))
}
func (t *evalTB) SkipNow() {
	t.Skip("skipped")
}
func (t *evalTB) Log(args ...any) {
	t.logs = append(t.logs, fmt.Sprint(args...))
}
func (t *evalTB) Logf(format string, args ...any) {
	t.Log(fmt.Sprintf(format, args...))
}
func (c *inspectClient) Complete(ctx context.Context, req *model.Request) (*model.Response, error) {
	c.complete++
	_, c.callInfo = model.CallInfoFrom(ctx)
	return c.Client.Complete(ctx, req)
}
func (c *inspectClient) Stream(ctx context.Context, req *model.Request) (model.ValidatedStreamer, error) {
	c.stream++
	_, c.callInfo = model.CallInfoFrom(ctx)
	return c.Client.Stream(ctx, req)
}

func (c rawCompleteClient) Complete(ctx context.Context, req *model.Request) (*model.Response, error) {
	return c.provider.Complete(ctx, req)
}
