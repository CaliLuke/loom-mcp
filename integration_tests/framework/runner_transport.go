package framework

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	mcpruntime "goa.design/goa-ai/runtime/mcp"
)

const assistantServiceName = "assistant"

var assistantCLISubcommands = map[string]string{
	"AnalyzeText":        "analyze-text",
	"SearchKnowledge":    "search-knowledge",
	"ExecuteCode":        "execute-code",
	"ListDocuments":      "list-documents",
	"GetSystemInfo":      "get-system-info",
	"GeneratePrompts":    "generate-prompts",
	"SendNotification":   "send-notification",
	"SubscribeToUpdates": "subscribe-to-updates",
	"ProcessBatch":       "process-batch",
}

// runSteps executes test steps.
func (r *Runner) runSteps(t *testing.T, steps []Step, defaults *Defaults, pre *Pre) {
	if shouldAutoInitialize(pre) {
		_ = r.ensureInitialized()
	}

	for _, step := range steps {
		headers := mergeStepHeaders(defaults, step)
		method := methodFromOp(step.Op)
		if isStreamingStep(step, headers) {
			r.runStepStreaming(t, step, headers, method)
			continue
		}
		r.runStepNonStreaming(t, step, headers, method, defaults)
	}
}

// runStepStreaming executes a streaming step and validates the response.
func (r *Runner) runStepStreaming(t *testing.T, step Step, headers map[string]string, method string) {
	resEvents, err := r.executeSSE(method, step.Input, headers, step.StreamExpect)
	assertExpectedError(t, step.Expect, err)
	if step.Expect != nil && step.Expect.Status == statusError {
		return
	}
	require.NoError(t, err)
	if step.StreamExpect != nil {
		assertStreamEvents(t, resEvents, step.StreamExpect)
	}
}

// runStepNonStreaming executes a non-streaming step using either HTTP or CLI mode and validates the response.
func (r *Runner) runStepNonStreaming(
	t *testing.T,
	step Step,
	headers map[string]string,
	method string,
	defaults *Defaults,
) {
	t.Helper()

	result, raw, err := r.executeNonStreamingStep(t, step, headers, method, defaults)
	if step.Expect != nil && step.Expect.Status == "no_response" {
		assert.Empty(t, raw)
		return
	}
	assertExpectedError(t, step.Expect, err)
	if step.Expect != nil && step.Expect.Status == statusError {
		return
	}
	require.NoError(t, err)
	if step.Expect != nil && step.Expect.Result != nil {
		validateSubset(t, result, step.Expect.Result)
	}
}

// cliSubcommandFromOp maps an operation to the CLI subcommand for a given service.
func (r *Runner) cliSubcommandFromOp(svc string, op string) string {
	if svc == assistantServiceName {
		if subcmd, ok := assistantCLISubcommands[op]; ok {
			return subcmd
		}
	}
	return op
}

// ensureInitialized sends an initialize request.
func (r *Runner) ensureInitialized() error {
	payload := map[string]any{
		"protocolVersion": "2025-06-18",
		"capabilities":    map[string]any{"tools": true, "resources": true, "prompts": true},
		"clientInfo":      map[string]any{"name": "runner", "version": "1.0.0"},
	}
	_, _, err := r.executeJSONRPC("initialize", payload, map[string]string{"Content-Type": "application/json"}, true)
	return err
}

// executeJSONRPC sends a JSON-RPC request and returns the result map, raw bytes, and error.
func (r *Runner) executeJSONRPC(
	method string,
	input map[string]any,
	headers map[string]string,
	notification bool,
) (map[string]any, []byte, error) {
	if input == nil {
		input = map[string]any{}
	}
	reqObj := map[string]any{"jsonrpc": "2.0", "method": method, "params": input}
	if !notification {
		reqObj["id"] = 1
	}
	body, _ := json.Marshal(reqObj)
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, r.baseURL.String()+"/rpc", bytes.NewReader(body))
	applyJSONRPCHeaders(req, r.sessionID, headers)
	// #nosec G704 -- test runner issues requests to localhost (or a validated TEST_SERVER_URL)
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	r.captureSessionID(resp)
	return decodeJSONRPCResponse(resp)
}

// executeSSE sends a request expecting SSE and returns captured events.
//
//nolint:cyclop,gocognit,nestif // SSE parsing is stateful and kept local to the transport boundary.
func (r *Runner) executeSSE(
	method string,
	input map[string]any,
	headers map[string]string,
	spec *StreamExpect,
) ([]sseEvent, error) {
	if input == nil {
		input = map[string]any{}
	}
	reqObj := map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": input}
	body, _ := json.Marshal(reqObj)
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, r.baseURL.String()+"/rpc", bytes.NewReader(body))
	if r.sessionID != "" {
		req.Header.Set(mcpruntime.HeaderKeySessionID, r.sessionID)
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	if req.Header.Get("Accept") == "" {
		req.Header.Set("Accept", "text/event-stream")
	}
	// #nosec G704 -- test runner issues requests to localhost (or a validated TEST_SERVER_URL)
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	r.captureSessionID(resp)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(raw))
	}
	if ct := resp.Header.Get("Content-Type"); ct != "" && !strings.HasPrefix(strings.ToLower(ct), "text/event-stream") {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected content type: %s body: %s", ct, string(raw))
	}

	timeout := 10 * time.Second
	if spec != nil && spec.TimeoutMS > 0 {
		timeout = time.Duration(spec.TimeoutMS) * time.Millisecond
	}
	deadline := time.Now().Add(timeout)
	reader := bufio.NewReader(resp.Body)
	var events []sseEvent
	var cur sseEvent
	sawErrorEvent := false
	var lastErrMsg string
	var lastErrCode any
	for time.Now().Before(deadline) {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				break
			}
			return events, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			if cur.Event != "" || len(cur.Data) > 0 {
				events = append(events, cur)
				if cur.Event == "error" {
					sawErrorEvent = true
					if eobj, ok := cur.Data["error"].(map[string]any); ok {
						lastErrCode = eobj["code"]
						if msg, ok := eobj["message"].(string); ok {
							lastErrMsg = msg
						}
					}
				}
				cur = sseEvent{}
			}
			if spec != nil && spec.MinEvents > 0 && len(events) >= spec.MinEvents {
				break
			}
			continue
		}
		if after, ok := strings.CutPrefix(line, "event:"); ok {
			cur.Event = strings.TrimSpace(after)
			continue
		}
		if after, ok := strings.CutPrefix(line, "data:"); ok {
			data := after
			var decoded map[string]any
			_ = json.Unmarshal([]byte(data), &decoded)
			if cur.Data == nil {
				cur.Data = map[string]any{}
			}
			maps.Copy(cur.Data, decoded)
		}
	}
	if spec == nil && sawErrorEvent {
		return events, fmt.Errorf("MCP error %v: %s", lastErrCode, lastErrMsg)
	}
	return events, nil
}

func shouldAutoInitialize(pre *Pre) bool {
	return pre != nil && pre.AutoInitialize != nil && *pre.AutoInitialize
}

func mergeStepHeaders(defaults *Defaults, step Step) map[string]string {
	headers := map[string]string{}
	if defaults != nil {
		maps.Copy(headers, defaults.Headers)
	}
	maps.Copy(headers, step.Headers)
	return headers
}

func isStreamingStep(step Step, headers map[string]string) bool {
	accept := strings.ToLower(headers["Accept"])
	return accept == "text/event-stream" || step.StreamExpect != nil
}

func assertStreamEvents(t *testing.T, events []sseEvent, spec *StreamExpect) {
	t.Helper()

	if spec.MinEvents > 0 {
		assert.GreaterOrEqual(t, len(events), spec.MinEvents)
	}
	for i := range spec.Events {
		if i >= len(events) {
			break
		}
		exp := spec.Events[i]
		act := events[i]
		if exp.Event != "" {
			assert.Equal(t, exp.Event, act.Event)
		}
		if exp.Data != nil {
			validateSubset(t, act.Data, exp.Data)
		}
	}
}

func assertExpectedError(t *testing.T, expect *Expect, err error) {
	t.Helper()

	if expect == nil || expect.Status != statusError {
		return
	}
	require.Error(t, err)
	if expect.Error != nil && expect.Error.Code != 0 {
		assert.Contains(t, err.Error(), strconv.Itoa(expect.Error.Code))
	}
	if expect.Error != nil && expect.Error.Message != "" {
		assert.Contains(t, err.Error(), expect.Error.Message)
	}
}

func (r *Runner) executeNonStreamingStep(
	t *testing.T,
	step Step,
	headers map[string]string,
	method string,
	defaults *Defaults,
) (map[string]any, []byte, error) {
	t.Helper()

	if r.clientMode(defaults) == "cli" {
		return r.executeCLIStep(t, step, defaults)
	}
	notify := step.Notification || (step.Expect != nil && step.Expect.Status == "no_response")
	return r.executeJSONRPC(method, step.Input, headers, notify)
}

func (r *Runner) executeCLIStep(t *testing.T, step Step, defaults *Defaults) (map[string]any, []byte, error) {
	t.Helper()

	if !SupportsCLI() {
		t.Skip("CLI mode requires the generated example CLI; restore the example directory to run CLI scenarios")
	}
	serviceName := cliServiceName(defaults)
	subcmd := r.cliSubcommandFromOp(serviceName, step.Op)
	cliPath := resolveCLIPath(t)
	cliArgs := append(
		[]string{"run", "-C", cliPath, ".", "-url", r.baseURL.String(), serviceName, subcmd},
		cliBodyArgs(step.Input, subcmd)...,
	)
	cmd := exec.CommandContext(context.Background(), "go", cliArgs...) // #nosec G204
	var out bytes.Buffer
	var errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	runErr := cmd.Run()
	if step.ExpectRetry != nil {
		require.Error(t, runErr)
		if step.ExpectRetry.PromptContains != "" {
			assert.Contains(t, errb.String(), step.ExpectRetry.PromptContains)
		}
		for _, s := range step.ExpectRetry.Contains {
			assert.Contains(t, errb.String(), s)
		}
		return nil, nil, nil
	}
	if step.Expect != nil && step.Expect.Status == statusError {
		require.Error(t, runErr, "cli stderr: %s", errb.String())
		return nil, nil, runErr
	}
	require.NoErrorf(t, runErr, "cli stderr: %s", errb.String())
	var result map[string]any
	_ = json.Unmarshal(out.Bytes(), &result)
	return result, out.Bytes(), nil
}

func (r *Runner) clientMode(defaults *Defaults) string {
	if defaults != nil && defaults.ClientMode != "" {
		return strings.ToLower(defaults.ClientMode)
	}
	return "http"
}

func cliServiceName(defaults *Defaults) string {
	if defaults == nil || defaults.Client == "" {
		return assistantServiceName
	}
	parts := strings.Split(defaults.Client, ".")
	last := strings.TrimPrefix(parts[len(parts)-1], "mcp_")
	if last == "" {
		return assistantServiceName
	}
	return last
}

func resolveCLIPath(t *testing.T) string {
	t.Helper()

	exampleRoot := findExampleRoot()
	require.NotEmpty(t, exampleRoot)
	serverCmdPath, err := findServerCmdDir(exampleRoot)
	require.NoError(t, err)
	return filepath.Join(exampleRoot, "cmd", filepath.Base(serverCmdPath)+"-cli")
}

func cliBodyArgs(input map[string]any, subcmd string) []string {
	if input == nil || !cliSubcommandNeedsBody(subcmd) {
		return nil
	}
	body, _ := json.Marshal(input)
	return []string{"--body", string(body)}
}

func cliSubcommandNeedsBody(subcmd string) bool {
	switch subcmd {
	case "analyze-text", "search-knowledge", "execute-code", "generate-prompts", "send-notification", "subscribe-to-updates", "process-batch":
		return true
	default:
		return false
	}
}

func applyJSONRPCHeaders(req *http.Request, sessionID string, headers map[string]string) {
	if sessionID != "" {
		req.Header.Set(mcpruntime.HeaderKeySessionID, sessionID)
	}
	for key, value := range headers {
		if strings.HasPrefix(key, "MCP_") {
			_ = os.Setenv(key, value)
			continue
		}
		req.Header.Set(key, value)
	}
	if req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}
}

func decodeJSONRPCResponse(resp *http.Response) (map[string]any, []byte, error) {
	raw, _ := io.ReadAll(resp.Body)
	if len(raw) == 0 {
		return nil, raw, nil
	}
	var env struct {
		Result map[string]any `json:"result"`
		Error  map[string]any `json:"error"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, raw, fmt.Errorf("invalid response JSON: %w", err)
	}
	if env.Error == nil {
		return env.Result, raw, nil
	}
	code, _ := env.Error["code"].(float64)
	msg, _ := env.Error["message"].(string)
	return nil, raw, fmt.Errorf("MCP error %d: %s", int(code), msg)
}
