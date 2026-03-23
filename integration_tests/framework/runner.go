package framework

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"sync"
	"testing"
	"time"

	mcpruntime "github.com/CaliLuke/loom-mcp/runtime/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

const (
	statusError = "error"
)

// Runner runs scenarios against the generated example server.
type Runner struct {
	server         *exec.Cmd
	baseURL        *url.URL
	client         *http.Client
	sessionID      string
	skipGeneration bool

	stdoutTail *ringBuffer
	stderrTail *ringBuffer
	exitCh     chan error

	externalServer bool
}

// Scenario models a test scenario (new multi-step form only).
type Scenario struct {
	Name     string    `yaml:"name"`
	Defaults *Defaults `yaml:"defaults"`
	Pre      *Pre      `yaml:"pre"`
	Steps    []Step    `yaml:"steps"`
}

// Defaults apply to steps when not explicitly set in a step.
type Defaults struct {
	Client     string            `yaml:"client"`      // e.g., "jsonrpc.mcp_assistant" (hint to pick generated client)
	Headers    map[string]string `yaml:"headers"`     // default headers for all steps
	ClientMode string            `yaml:"client_mode"` // http | cli (optional)
}

// Pre controls scenario-level behavior (e.g., auto-initialize handshake).
type Pre struct {
	AutoInitialize *bool `yaml:"auto_initialize"` // default true
}

// Step defines a single operation invocation using a generated client.
type Step struct {
	Name         string            `yaml:"name"`
	Client       string            `yaml:"client"`       // overrides defaults.client
	Op           string            `yaml:"op"`           // generated endpoint method name, e.g., "ToolsCall"
	Input        map[string]any    `yaml:"input"`        // maps to payload fields
	Headers      map[string]string `yaml:"headers"`      // per-step headers (e.g., Accept)
	Notification bool              `yaml:"notification"` // send as JSON-RPC notification (no id)
	Expect       *Expect           `yaml:"expect"`
	StreamExpect *StreamExpect     `yaml:"stream_expect"`
	ExpectRetry  *ExpectRetry      `yaml:"expect_retry"` // generated client retry expectation
}

// ExpectedError captures expected JSON-RPC error.
type ExpectedError struct {
	Code    int    `yaml:"code"`
	Message string `yaml:"message"`
}

// Expect describes non-streaming expectations.
type Expect struct {
	Status string         `yaml:"status"` // success | error | no_response
	Error  *ExpectedError `yaml:"error"`
	Result map[string]any `yaml:"result"`
}

// ExpectRetry describes retry expectations for generated client mode
type ExpectRetry struct {
	PromptContains string   `yaml:"prompt_contains"`
	Contains       []string `yaml:"contains"`
}

// StreamExpect describes streaming expectations.
type StreamExpect struct {
	MinEvents int              `yaml:"min_events"`
	TimeoutMS int              `yaml:"timeout_ms"`
	Events    []StreamEventExp `yaml:"events"`
}

// StreamEventExp matches SSE event/data partially.
type StreamEventExp struct {
	Event string         `yaml:"event"`
	Data  map[string]any `yaml:"data"`
}

// scenariosFile is the YAML root.
type scenariosFile struct {
	Scenarios []Scenario `yaml:"scenarios"`
}

// ringBuffer captures only the last max bytes written.
type ringBuffer struct {
	mu  sync.Mutex
	max int
	buf []byte
}

// sseEvent represents a server-sent event.
type sseEvent struct {
	Event string
	Data  map[string]any
}

const tailMaxBytes = 4096

var (
	codegenMu            sync.Mutex
	preparedExampleCache = map[string]preparedExample{}

	// Pre-compiled binary state keyed by the fixture command identity so cloned
	// SDK fixtures can reuse the same binary instead of rebuilding identical
	// temp-directory copies for every scenario.
	serverBinMu    sync.Mutex
	serverBinCache = map[string]serverBinaryBuild{}
)

type serverBinaryBuild struct {
	path string
	err  error
}

type preparedExample struct {
	root string
	err  error
}

// LoadScenarios loads scenarios from a YAML file path.
func LoadScenarios(path string) ([]Scenario, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- test helper reads scenarios file from testdata path
	if err != nil {
		return nil, fmt.Errorf("read scenarios: %w", err)
	}
	var f scenariosFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse scenarios: %w", err)
	}
	return f.Scenarios, nil
}

// NewRunner creates a new runner with fixed timeout.
func NewRunner() *Runner {
	return &Runner{
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

// SupportsServer reports whether the integration framework can reach a server.
func SupportsServer() bool {
	if os.Getenv("TEST_SERVER_URL") != "" {
		return true
	}
	return findExampleRoot() != ""
}

// SupportsCLI reports whether CLI-based scenarios can run.
func SupportsCLI() bool {
	return findExampleRoot() != ""
}

// Run executes the scenarios in a single managed-server session.
func (r *Runner) Run(t *testing.T, scenarios []Scenario) error {
	t.Helper()
	if len(scenarios) == 0 {
		t.Skip("no scenarios to run")
	}

	if err := r.startServer(t); err != nil {
		return err
	}
	// Use t.Cleanup instead of defer so stopServer runs after all parallel
	// subtests complete. With defer, stopServer would run immediately when
	// Run returns (before parallel subtests execute).
	t.Cleanup(r.stopServer)

	for _, sc := range scenarios {
		scenario := sc
		t.Run(scenario.Name, func(t *testing.T) {
			r.runSteps(t, scenario.Steps, scenario.Defaults, scenario.Pre)
		})
	}
	return nil
}

// Write implements io.Writer keeping only the last max bytes.
func (r *ringBuffer) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.buf == nil {
		r.buf = make([]byte, 0, r.max)
	}
	r.buf = append(r.buf, p...)
	if len(r.buf) > r.max {
		r.buf = r.buf[len(r.buf)-r.max:]
	}
	return len(p), nil
}

// Bytes returns a copy of the buffer contents.
func (r *ringBuffer) Bytes() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.buf) == 0 {
		return nil
	}
	out := make([]byte, len(r.buf))
	copy(out, r.buf)
	return out
}

// validateSubset ensures expected fields are present in actual using testify assertions.
func validateSubset(t *testing.T, actual map[string]any, expected map[string]any) {
	for k, vexp := range expected {
		vact, ok := actual[k]
		require.Truef(t, ok, "missing key %q", k)
		switch ev := vexp.(type) {
		case map[string]any:
			am, ok := toMap(vact)
			require.Truef(t, ok, "key %q: expected object", k)
			validateSubset(t, am, ev)
		case []any:
			aarr, ok := vact.([]any)
			require.Truef(t, ok, "key %q: expected array", k)
			require.GreaterOrEqualf(
				t,
				len(aarr),
				len(ev),
				"key %q: expected at least %d items, got %d",
				k,
				len(ev),
				len(aarr),
			)
			for i := range ev {
				if elemExp, ok := ev[i].(map[string]any); ok {
					elemAct, ok := toMap(aarr[i])
					require.Truef(t, ok, "key %q[%d]: expected object", k, i)
					validateSubset(t, elemAct, elemExp)
				}
			}
		default:
			assert.Equalf(t, fmt.Sprintf("%v", vexp), fmt.Sprintf("%v", vact), "key %q mismatch", k)
		}
	}
}

// toMap converts various map types to map[string]any.
func toMap(v any) (map[string]any, bool) {
	if m, ok := v.(map[string]any); ok {
		return m, true
	}
	if m, ok := v.(map[string]interface{}); ok {
		res := make(map[string]any, len(m))
		for k, vv := range m {
			res[k] = vv
		}
		return res, true
	}
	return nil, false
}

// getFreePort finds an available port on localhost.
func getFreePort() (string, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0") //nolint:noctx // test helper just picks a free port
	if err != nil {
		return "", fmt.Errorf("listen for free port: %w", err)
	}
	defer func() { _ = l.Close() }()
	_, portStr, err := net.SplitHostPort(l.Addr().String())
	if err != nil {
		return "", err
	}
	return portStr, nil
}

var methodNamesByOperation = map[string]string{
	"Initialize":           "initialize",
	"Ping":                 "ping",
	"EventsStream":         "events/stream",
	"ToolsList":            "tools/list",
	"ToolsCall":            "tools/call",
	"ResourcesList":        "resources/list",
	"ResourcesRead":        "resources/read",
	"ResourcesSubscribe":   "resources/subscribe",
	"ResourcesUnsubscribe": "resources/unsubscribe",
	"PromptsList":          "prompts/list",
	"PromptsGet":           "prompts/get",
	"NotifyStatusUpdate":   "notify_status_update",
	"Subscribe":            "subscribe",
	"Unsubscribe":          "unsubscribe",
}

// methodFromOp maps operation names to JSON-RPC method names.
func methodFromOp(op string) string {
	if method, ok := methodNamesByOperation[op]; ok {
		return method
	}
	return op
}

func (r *Runner) captureSessionID(resp *http.Response) {
	if r == nil || resp == nil {
		return
	}
	if sessionID := resp.Header.Get(mcpruntime.HeaderKeySessionID); sessionID != "" {
		r.sessionID = sessionID
	}
}
