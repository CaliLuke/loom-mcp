package framework

import (
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Runner manages the application-owned SDK server used by integration tests.
type Runner struct {
	server         *exec.Cmd
	baseURL        *url.URL
	client         *http.Client
	skipGeneration bool

	stdoutTail *ringBuffer
	stderrTail *ringBuffer
	exitCh     chan error

	externalServer bool
}

type ringBuffer struct {
	mu  sync.Mutex
	max int
	buf []byte
}

type serverBinaryBuild struct {
	path string
	err  error
}

type preparedExample struct {
	root string
	err  error
}

const tailMaxBytes = 4096

var (
	codegenMu            sync.Mutex
	preparedExampleCache = map[string]preparedExample{}
	serverBinMu          sync.Mutex
	serverBinCache       = map[string]serverBinaryBuild{}
)

// NewRunner creates an SDK integration server runner.
func NewRunner() *Runner {
	return &Runner{client: &http.Client{Timeout: 2 * time.Second}}
}

// SupportsServer reports whether the integration framework can reach a server.
func SupportsServer() bool {
	if os.Getenv("TEST_SERVER_URL") != "" {
		return true
	}
	return findExampleRoot() != ""
}

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

func (r *Runner) rpcURL() string {
	if r.baseURL == nil {
		return ""
	}
	u := *r.baseURL
	path := strings.TrimRight(u.Path, "/")
	if !strings.HasSuffix(path, "/rpc") {
		path += "/rpc"
	}
	u.Path = path
	return u.String()
}
