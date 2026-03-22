package framework

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CaliLuke/loom-mcp/internal/upstreampaths"
)

// findExampleRoot locates the example directory.
func findExampleRoot() string {
	wd, _ := os.Getwd()
	for up := 0; up < 8; up++ {
		root := wd
		for i := 0; i < up; i++ {
			root = filepath.Dir(root)
		}
		// Use integration test fixture module exclusively.
		fixtureRoot := filepath.Join(root, "integration_tests", "fixtures", "assistant")
		if st, err := os.Stat(fixtureRoot); err == nil && st.IsDir() {
			if _, err := os.Stat(filepath.Join(fixtureRoot, "go.mod")); err == nil {
				return fixtureRoot
			}
		}
	}
	return ""
}

//nolint:gosec // Test fixture cloning preserves fixture modes and copies a known tree.
func cloneExampleRoot(exampleRoot string) (string, error) {
	tmpBase := filepath.Join(filepath.Dir(filepath.Dir(exampleRoot)), ".tmp")
	if err := os.MkdirAll(tmpBase, 0o750); err != nil {
		return "", fmt.Errorf("create temp example base: %w", err)
	}
	tmpRoot, err := os.MkdirTemp(tmpBase, "loom-mcp-example-*")
	if err != nil {
		return "", fmt.Errorf("create temp example root: %w", err)
	}
	walkErr := filepath.WalkDir(exampleRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(exampleRoot, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		target := filepath.Join(tmpRoot, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		if d.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		data, err := os.ReadFile(path) // #nosec G304 -- path comes from walking a trusted fixture tree
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode())
	})
	if walkErr != nil {
		_ = os.RemoveAll(tmpRoot)
		return "", fmt.Errorf("clone example root: %w", walkErr)
	}
	return tmpRoot, nil
}

func applySDKServerFixturePatch(exampleRoot string) error {
	const httpContent = `package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"sync"
	"time"

	assistantapi "example.com/assistant"
	assistant "example.com/assistant/gen/assistant"
	mcpassistant "example.com/assistant/gen/mcp_assistant"
	"goa.design/clue/debug"
	"goa.design/clue/log"
	goahttp "github.com/CaliLuke/loom/http"
)

type sdkAssistantService struct {
	assistant.Service
}

func (s sdkAssistantService) SystemInfo(ctx context.Context) (*assistant.SystemInfoResult, error) {
	name := "assistant-itest"
	version := "1.0.0"
	return &assistant.SystemInfoResult{Name: &name, Version: &version}, nil
}

func (s sdkAssistantService) AnalyzeSentiment(ctx context.Context, p *assistant.AnalyzeSentimentPayload) (*assistant.AnalyzeSentimentResult, error) {
	sentiment := "positive"
	return &assistant.AnalyzeSentimentResult{Sentiment: &sentiment}, nil
}

func (s sdkAssistantService) FigmaDesignSystem(ctx context.Context) (*assistant.DesignSystem, error) {
	return assistantapi.FixtureDesignSystem(), nil
}

func (s sdkAssistantService) GenerateDpiSpec(ctx context.Context, p *assistant.GenerateDpiSpecPayload) (*assistant.DPISpec, error) {
	return assistantapi.FixtureDPISpec(p), nil
}

type sdkPromptProvider struct{}

func (sdkPromptProvider) GetCodeReviewPrompt(arguments json.RawMessage) (*mcpassistant.PromptsGetResult, error) {
	description := "Code review guidance"
	text := "Review the provided code and suggest improvements."
	return &mcpassistant.PromptsGetResult{
		Description: &description,
		Messages: []*mcpassistant.PromptMessage{
			{Role: "system", Content: &mcpassistant.MessageContent{Type: "text", Text: &text}},
		},
	}, nil
}

func (sdkPromptProvider) GetContextualPromptsPrompt(ctx context.Context, arguments json.RawMessage) (*mcpassistant.PromptsGetResult, error) {
	text := "Dynamic contextual prompts"
	return &mcpassistant.PromptsGetResult{
		Messages: []*mcpassistant.PromptMessage{
			{Role: "system", Content: &mcpassistant.MessageContent{Type: "text", Text: &text}},
		},
	}, nil
}

func (sdkPromptProvider) GetFigmaImplementationPromptPrompt(ctx context.Context, arguments json.RawMessage) (*mcpassistant.PromptsGetResult, error) {
	var payload map[string]any
	if len(arguments) > 0 {
		if err := json.Unmarshal(arguments, &payload); err != nil {
			return nil, err
		}
	}

	screenTitle, _ := payload["screen_title"].(string)
	framework, _ := payload["framework"].(string)
	designTokensURI, _ := payload["design_tokens_uri"].(string)
	dpiJSON, _ := payload["dpi_json"].(string)

	var spec assistant.DPISpec
	if dpiJSON != "" {
		_ = json.Unmarshal([]byte(dpiJSON), &spec)
	}

	description := "Figma implementation handoff"
	text := assistantapi.FixtureImplementationPrompt(screenTitle, framework, designTokensURI, &spec)
	return &mcpassistant.PromptsGetResult{
		Description: &description,
		Messages: []*mcpassistant.PromptMessage{
			{Role: "system", Content: &mcpassistant.MessageContent{Type: "text", Text: &text}},
		},
	}, nil
}

// handleHTTPServer configures and starts an SDK-backed HTTP server on the given
// URL. It shuts down the server if any error is received in the error channel.
func handleHTTPServer(ctx context.Context, u *url.URL, _ mcpassistant.Service, _ *mcpassistant.Endpoints, wg *sync.WaitGroup, errc chan error, dbg bool) {
	mux := goahttp.NewMuxer()
	if dbg {
		debug.MountPprofHandlers(debug.Adapt(mux))
		debug.MountDebugLogEnabler(debug.Adapt(mux))
	}

	sdkServer, err := mcpassistant.NewSDKServer(sdkAssistantService{Service: assistantapi.NewAssistant()}, &mcpassistant.SDKServerOptions{
		PromptProvider: sdkPromptProvider{},
		RequestContext: func(reqCtx context.Context, r *http.Request) context.Context {
			if r == nil {
				return reqCtx
			}
			if allow := r.Header.Get("x-mcp-allow-names"); allow != "" {
				reqCtx = context.WithValue(reqCtx, "mcp_allow_names", allow)
			}
			if deny := r.Header.Get("x-mcp-deny-names"); deny != "" {
				reqCtx = context.WithValue(reqCtx, "mcp_deny_names", deny)
			}
			return reqCtx
		},
	})
	if err != nil {
		errc <- err
		return
	}

	mux.Handle("POST", "/rpc", sdkServer.Handler.ServeHTTP)
	mux.Handle("GET", "/rpc", sdkServer.Handler.ServeHTTP)
	mux.Handle("DELETE", "/rpc", sdkServer.Handler.ServeHTTP)

	var handler http.Handler = mux
	if dbg {
		handler = debug.HTTP()(handler)
	}
	handler = log.HTTP(ctx)(handler)

	srv := &http.Server{Addr: u.Host, Handler: handler, ReadHeaderTimeout: time.Second * 60}
	log.Printf(ctx, "SDK-backed MCP server mounted on /rpc")

	(*wg).Add(1)
	go func() {
		defer (*wg).Done()
		go func() {
			log.Printf(ctx, "HTTP server listening on %q", u.Host)
			errc <- srv.ListenAndServe()
		}()

		<-ctx.Done()
		log.Printf(ctx, "shutting down HTTP server at %q", u.Host)

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		if err := srv.Shutdown(ctx); err != nil {
			log.Printf(ctx, "failed to shutdown: %v", err)
		}
	}()
}
`
	const mainContent = `package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/signal"
	"sync"
	"syscall"

	assistantapi "example.com/assistant"
	mcpassistant "example.com/assistant/gen/mcp_assistant"
	"goa.design/clue/log"
)

func main() {
	// Define command line flags, add any other flag required to configure the
	// service.
	var (
		hostF     = flag.String("host", "dev", "Server host (valid values: dev)")
		domainF   = flag.String("domain", "", "Host domain name (overrides host domain specified in service design)")
		httpPortF = flag.String("http-port", "", "HTTP port (overrides host HTTP port specified in service design)")
		secureF   = flag.Bool("secure", false, "Use secure scheme (https or grpcs)")
		dbgF      = flag.Bool("debug", false, "Log request and response bodies")
	)
	flag.Parse()

	// Setup logger. Replace logger with your own log package of choice.
	format := log.FormatJSON
	if log.IsTerminal() {
		format = log.FormatTerminal
	}
	ctx := log.Context(context.Background(), log.WithFormat(format))
	if *dbgF {
		ctx = log.Context(ctx, log.WithDebug())
		log.Debugf(ctx, "debug logs enabled")
	}
	log.Print(ctx, log.KV{K: "http-port", V: *httpPortF})

	// Initialize the services.
	var mcpAssistantSvc mcpassistant.Service
	{
		mcpAssistantSvc = assistantapi.NewMcpAssistant()
	}

	// Wrap the services in endpoints that can be invoked from other services
	// potentially running in different processes.
	var mcpAssistantEndpoints *mcpassistant.Endpoints
	{
		mcpAssistantEndpoints = mcpassistant.NewEndpoints(mcpAssistantSvc)
	}

	// Create channel used by both the signal handler and server goroutines
	// to notify the main goroutine when to stop the server.
	errc := make(chan error)

	// Setup interrupt handler. This optional step configures the process so
	// that SIGINT and SIGTERM signals cause the services to stop gracefully.
	go func() {
		c := make(chan os.Signal, 1)
		signal.Notify(c, syscall.SIGINT, syscall.SIGTERM)
		errc <- fmt.Errorf("%s", <-c)
	}()

	var wg sync.WaitGroup
	ctx, cancel := context.WithCancel(ctx)

	// Start the servers and send errors (if any) to the error channel.
	switch *hostF {
	case "dev":
		{
			addr := "http://localhost:8080"
			u, err := url.Parse(addr)
			if err != nil {
				log.Fatalf(ctx, err, "invalid URL %#v\n", addr)
			}
			if *secureF {
				u.Scheme = "https"
			}
			if *domainF != "" {
				u.Host = *domainF
			}
			if *httpPortF != "" {
				h, _, err := net.SplitHostPort(u.Host)
				if err != nil {
					log.Fatalf(ctx, err, "invalid URL %#v\n", u.Host)
				}
				u.Host = net.JoinHostPort(h, *httpPortF)
			} else if u.Port() == "" {
				u.Host = net.JoinHostPort(u.Host, "80")
			}
			handleHTTPServer(ctx, u, mcpAssistantSvc, mcpAssistantEndpoints, &wg, errc, *dbgF)
		}

	default:
		log.Fatal(ctx, fmt.Errorf("invalid host argument: %q (valid hosts: dev)", *hostF))
	}

	// Wait for signal.
	log.Printf(ctx, "exiting (%v)", <-errc)

	// Send cancellation signal to the goroutines.
	cancel()

	wg.Wait()
	log.Printf(ctx, "exited")
}
`
	cmdDir, err := findServerCmdDir(exampleRoot)
	if err != nil {
		return fmt.Errorf("resolve SDK fixture command dir: %w", err)
	}
	if err := os.MkdirAll(cmdDir, 0o750); err != nil {
		return fmt.Errorf("create SDK fixture command dir: %w", err)
	}
	httpPath := filepath.Join(cmdDir, "http.go")
	if err := os.WriteFile(httpPath, []byte(httpContent), 0o600); err != nil {
		return fmt.Errorf("write SDK fixture http.go: %w", err)
	}
	_ = os.Remove(filepath.Join(cmdDir, "jsonrpc.go"))

	mainPath := filepath.Join(cmdDir, "main.go")
	if err := os.WriteFile(mainPath, []byte(mainContent), 0o600); err != nil { // #nosec G306,G703 -- mainPath is resolved under the trusted fixture command dir
		return fmt.Errorf("write SDK fixture main.go: %w", err)
	}
	return nil
}

// findServerCmdDir finds the server command directory.
func findServerCmdDir(exampleRoot string) (string, error) {
	cmdRoot := filepath.Join(exampleRoot, "cmd")
	entries, err := os.ReadDir(cmdRoot)
	if err != nil {
		return "", fmt.Errorf("read cmd root: %w", err)
	}
	var candidates []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(cmdRoot, e.Name())
		if _, err := os.Stat(filepath.Join(dir, "main.go")); err == nil {
			candidates = append(candidates, dir)
		}
	}
	if len(candidates) == 0 {
		return "", fmt.Errorf("no server cmd dirs found under %s", cmdRoot)
	}
	for _, dir := range candidates {
		if _, err := os.Stat(filepath.Join(dir, "http.go")); err == nil {
			return dir, nil
		}
	}
	return candidates[0], nil
}

// regenerateExample regenerates the example code.
func regenerateExample(t *testing.T, exampleRoot string) error {
	t.Helper()
	codegenMu.Lock()
	defer codegenMu.Unlock()

	root, err := os.OpenRoot(exampleRoot)
	if err != nil {
		return fmt.Errorf("open example root: %w", err)
	}
	defer func() {
		_ = root.Close()
	}()
	if err := cleanGeneratedExampleArtifacts(exampleRoot); err != nil {
		return err
	}
	tidyCmd := exec.CommandContext(context.Background(), "go", "mod", "tidy")
	tidyCmd.Dir = exampleRoot
	if out, err := tidyCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("go mod tidy failed: %w\n%s", err, string(out))
	}
	genCmd := exec.CommandContext(
		context.Background(),
		"go",
		"run",
		"-C",
		exampleRoot,
		upstreampaths.LoomCLIPackage,
		"gen",
		"example.com/assistant/design",
	) // #nosec G204
	if out, err := genCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("loom gen failed: %w\n%s", err, string(out))
	}
	_ = os.Remove(filepath.Join(exampleRoot, "assistant.go"))
	_ = os.Remove(filepath.Join(exampleRoot, "streaming.go"))
	_ = os.Remove(filepath.Join(exampleRoot, "websocket.go"))
	_ = os.Remove(filepath.Join(exampleRoot, "grpcstream.go"))
	_ = os.Remove(filepath.Join(exampleRoot, "mcp_assistant.go"))
	exCmd := exec.CommandContext(
		context.Background(),
		"go",
		"run",
		"-C",
		exampleRoot,
		upstreampaths.LoomCLIPackage,
		"example",
		"example.com/assistant/design",
	) // #nosec G204
	if out, err := exCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("loom example failed: %w\n%s", err, string(out))
	}
	_ = os.Remove(filepath.Join(exampleRoot, "mcp_assistant.go"))
	postTidy := exec.CommandContext(context.Background(), "go", "mod", "tidy")
	postTidy.Dir = exampleRoot
	if out, err := postTidy.CombinedOutput(); err != nil {
		return fmt.Errorf("post loom example tidy failed: %w\n%s", err, string(out))
	}
	return nil
}

// cleanGeneratedExampleArtifacts removes generated example artifacts that can interfere
// with repeated goa generation in tests.
func cleanGeneratedExampleArtifacts(exampleRoot string) error {
	root, err := os.OpenRoot(exampleRoot)
	if err != nil {
		return fmt.Errorf("open example root: %w", err)
	}
	defer func() {
		_ = root.Close()
	}()
	if err := root.RemoveAll("cmd"); err != nil {
		return fmt.Errorf("remove cmd directory: %w", err)
	}
	const generatedHeader = "Code generated by goa"
	err = fs.WalkDir(root.FS(), ".", func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		content, readErr := root.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if bytes.Contains(content, []byte(generatedHeader)) {
			if err := root.Remove(path); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("clean generated example artifacts: %w", err)
	}
	return nil
}

func restoreFixtureCommandTree(fixtureRoot string, exampleRoot string) error {
	sourceRoot := filepath.Join(fixtureRoot, "cmd")
	targetRoot := filepath.Join(exampleRoot, "cmd")
	if err := os.RemoveAll(targetRoot); err != nil {
		return fmt.Errorf("remove regenerated cmd directory: %w", err)
	}
	source, err := os.OpenRoot(sourceRoot)
	if err != nil {
		return fmt.Errorf("open fixture cmd root: %w", err)
	}
	defer func() {
		_ = source.Close()
	}()
	targetFS, err := os.OpenRoot(exampleRoot)
	if err != nil {
		return fmt.Errorf("open example root: %w", err)
	}
	defer func() {
		_ = targetFS.Close()
	}()
	return filepath.WalkDir(sourceRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(sourceRoot, path)
		if err != nil {
			return err
		}
		targetRel := filepath.Join("cmd", rel)
		targetPath := filepath.Join(targetRoot, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		if d.IsDir() {
			return os.MkdirAll(targetPath, info.Mode())
		}
		data, err := source.ReadFile(rel)
		if err != nil {
			return err
		}
		if err := targetFS.MkdirAll(filepath.Dir(targetRel), 0o750); err != nil {
			return err
		}
		return targetFS.WriteFile(targetRel, data, info.Mode())
	})
}

// buildServerBinary compiles the server binary once for fast parallel test starts.
func buildServerBinary(exampleRoot string) (string, error) {
	serverBinMu.Lock()
	defer serverBinMu.Unlock()

	cmdPath, err := findServerCmdDir(exampleRoot)
	if err != nil {
		return "", err
	}
	if cached, ok := serverBinCache[cmdPath]; ok {
		return cached.path, cached.err
	}

	tmpFile, err := os.CreateTemp("", "mcp-test-server-*")
	if err != nil {
		return "", fmt.Errorf("create temp file for binary: %w", err)
	}
	binPath := filepath.Clean(tmpFile.Name())
	if err := tmpFile.Close(); err != nil {
		return "", fmt.Errorf("close temp file for binary: %w", err)
	}

	buildCmd := exec.CommandContext(context.Background(), "go", "build", "-o", binPath, ".") // #nosec G204 -- cmdPath is resolved from the trusted fixture tree
	buildCmd.Dir = cmdPath
	out, err := buildCmd.CombinedOutput()
	if err != nil {
		buildErr := fmt.Errorf("go build failed in %s: %w\n%s", cmdPath, err, string(out))
		if removeErr := os.Remove(binPath); removeErr != nil {
			buildErr = errors.Join(buildErr, fmt.Errorf("remove temp binary failed: %w", removeErr))
		}
		serverBinCache[cmdPath] = serverBinaryBuild{err: buildErr}
		return "", buildErr
	}
	if _, err := os.Stat(binPath); err != nil {
		buildErr := fmt.Errorf("binary not found after build: %w", err)
		serverBinCache[cmdPath] = serverBinaryBuild{err: buildErr}
		return "", buildErr
	}
	serverBinCache[cmdPath] = serverBinaryBuild{path: binPath}
	return binPath, nil
}
