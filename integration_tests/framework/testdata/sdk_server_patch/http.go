package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	assistantapi "example.com/assistant"
	assistant "example.com/assistant/gen/assistant"
	mcpassistant "example.com/assistant/gen/mcp_assistant"
	"github.com/CaliLuke/loom/clue/debug"
	"github.com/CaliLuke/loom/clue/log"
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
		if err := json.Unmarshal([]byte(dpiJSON), &spec); err != nil {
			return nil, fmt.Errorf("decode dpi_json: %w", err)
		}
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

	corsPolicy, err := goahttp.NewRuntimeCORSPolicy(goahttp.CORSPolicy{Origins: []goahttp.CORSOrigin{{Pattern: "*"}}})
	if err != nil {
		errc <- fmt.Errorf("configure runtime CORS: %w", err)
		return
	}
	sdkServer, err := mcpassistant.NewSDKServer(sdkAssistantService{Service: assistantapi.NewAssistant()}, &mcpassistant.SDKServerOptions{
		PromptProvider: sdkPromptProvider{},
		RuntimeCORS:    &corsPolicy,
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
