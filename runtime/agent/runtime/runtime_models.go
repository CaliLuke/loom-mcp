package runtime

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"cloud.google.com/go/auth"
	bedrock "github.com/CaliLuke/loom-mcp/v2/features/model/bedrock"
	codexfeature "github.com/CaliLuke/loom-mcp/v2/features/model/codex"
	gemini "github.com/CaliLuke/loom-mcp/v2/features/model/gemini"
	ollamafeature "github.com/CaliLuke/loom-mcp/v2/features/model/ollama"
	openaifeature "github.com/CaliLuke/loom-mcp/v2/features/model/openai"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/gorilla/websocket"
	openaisdk "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"google.golang.org/genai"
)

// BedrockConfig configures the bedrock-backed model client created by the runtime.
type BedrockConfig struct {
	DefaultModel   string
	HighModel      string
	SmallModel     string
	MaxTokens      int
	ThinkingBudget int
	Temperature    float32
}

// OpenAIConfig configures the OpenAI-backed model client created by the runtime.
type OpenAIConfig struct {
	APIKey       string
	BaseURL      string
	DefaultModel string
	HighModel    string
	SmallModel   string
}

// CodexConfig configures the ChatGPT-backed Codex subscription model client.
// Credentials are resolved through the application-owned source for every
// logical request; the runtime never discovers or persists credentials.
type CodexConfig struct {
	// ReasoningEffort selects a provider effort identifier; empty uses the provider default.
	ReasoningEffort   string
	CredentialSource  codexfeature.CredentialSource
	HTTPClient        *http.Client
	WebSocketDialer   *websocket.Dialer
	Transport         codexfeature.Transport
	ClientVersion     string
	ResponsesLite     bool
	StreamIdleTimeout time.Duration
	DefaultModel      string
	HighModel         string
	SmallModel        string
}

// OllamaConfig configures the local Ollama-backed model client created by the runtime.
type OllamaConfig struct {
	ServerURL    string
	HTTPClient   *http.Client
	DefaultModel string
	HighModel    string
	SmallModel   string
	MaxTokens    int
	Temperature  float32
	Timeout      time.Duration
}

// GeminiConfig configures the Gemini API-backed model client created by the runtime.
type GeminiConfig struct {
	APIKey         string
	DefaultModel   string
	HighModel      string
	SmallModel     string
	MaxTokens      int
	ThinkingBudget int
	Temperature    float32
}

// VertexConfig configures the Vertex AI Gemini model client created by the runtime.
type VertexConfig struct {
	ProjectID      string
	Location       string
	APIKey         string
	Credentials    *auth.Credentials
	HTTPClient     *http.Client
	HTTPOptions    genai.HTTPOptions
	DefaultModel   string
	HighModel      string
	SmallModel     string
	MaxTokens      int
	ThinkingBudget int
	Temperature    float32
}

// RegisterModel registers a ModelClient by identifier for planner lookup.
// Registration fails with ErrRegistrationClosed after the runtime is sealed or
// its first run is submitted.
func (r *Runtime) RegisterModel(id string, client model.Client) error {
	_, err := r.RegisterModelClient(id, client)
	return err
}

// RegisterModelClient registers client under id, replacing any existing client
// with that identifier, and returns the exact stored client, including runtime
// context-limit decoration. Storage and retrieval happen under one registry lock;
// the returned value is not affected by a later registration replacing the entry.
//
// An empty id or nil client returns an error. After Seal or the first submitted
// run closes registration, RegisterModelClient returns ErrRegistrationClosed,
// including for otherwise invalid arguments. Every error returns a nil client
// and leaves the registry unchanged.
//
// Bootstrap code can retain the returned value to verify its registration after
// sealing without a separate, racy ModelClient lookup. As with model.Client
// generally, callers must ensure its concrete value is comparable before using
// ==; this method does not impose comparability on client implementations.
func (r *Runtime) RegisterModelClient(id string, client model.Client) (model.Client, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.registrationClosed {
		return nil, ErrRegistrationClosed
	}
	if id == "" {
		return nil, errors.New("model id is required")
	}
	if client == nil {
		return nil, errors.New("model client is required")
	}
	registered := r.withContextLimits(id, client)
	r.models[id] = registered
	return registered, nil
}

// ModelClient returns a registered model client by ID, if present.
// Unlike PlannerContext.ModelClient, it does not attach model.CallInfo or allocate
// a run store sequence for calls.
func (r *Runtime) ModelClient(id string) (model.Client, bool) {
	if id == "" {
		return nil, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	m, ok := r.models[id]
	return m, ok
}

// ModelIDs returns a sorted owned copy of the registered model identifiers.
func (r *Runtime) ModelIDs() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ids := make([]string, 0, len(r.models))
	for id := range r.models {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

// NewBedrockModelClient constructs a model.Client backed by AWS Bedrock that
// encodes requests from the planner's messages only.
func (r *Runtime) NewBedrockModelClient(awsrt *bedrockruntime.Client, cfg BedrockConfig) (model.Client, error) {
	opts := bedrock.Options{
		Runtime:        awsrt,
		DefaultModel:   cfg.DefaultModel,
		HighModel:      cfg.HighModel,
		SmallModel:     cfg.SmallModel,
		MaxTokens:      cfg.MaxTokens,
		ThinkingBudget: cfg.ThinkingBudget,
		Temperature:    cfg.Temperature,
		Logger:         r.logger,
	}
	provider, err := bedrock.New(awsrt, opts)
	if err != nil {
		return nil, err
	}
	return model.NewClient(provider)
}

// NewOpenAIModelClient constructs a model.Client backed by the OpenAI Responses
// API using the official OpenAI Go SDK. The client is not registered
// automatically; pass it to RegisterModel with the model ID your planners use.
func (r *Runtime) NewOpenAIModelClient(cfg OpenAIConfig) (model.Client, error) {
	apiKey := strings.TrimSpace(cfg.APIKey)
	if apiKey == "" {
		return nil, errors.New("openai: api key is required")
	}
	if strings.TrimSpace(cfg.DefaultModel) == "" {
		return nil, errors.New("openai: default model is required")
	}
	requestOptions := []option.RequestOption{
		option.WithAPIKey(apiKey),
	}
	if baseURL := strings.TrimSpace(cfg.BaseURL); baseURL != "" {
		requestOptions = append(requestOptions, option.WithBaseURL(baseURL))
	}
	client := openaisdk.NewClient(requestOptions...)
	provider, err := openaifeature.New(openaifeature.Options{
		Client:       &client.Responses,
		DefaultModel: cfg.DefaultModel,
		HighModel:    cfg.HighModel,
		SmallModel:   cfg.SmallModel,
	})
	if err != nil {
		return nil, err
	}
	return model.NewClient(provider)
}

// NewCodexModelClient constructs a model.Client backed by a ChatGPT Codex
// subscription. The client is not registered automatically; pass it to
// RegisterModel with the model ID your planners use.
func (r *Runtime) NewCodexModelClient(cfg CodexConfig) (model.Client, error) {
	provider, err := codexfeature.New(codexfeature.Options{
		ReasoningEffort:   cfg.ReasoningEffort,
		CredentialSource:  cfg.CredentialSource,
		HTTPClient:        cfg.HTTPClient,
		WebSocketDialer:   cfg.WebSocketDialer,
		Transport:         cfg.Transport,
		ClientVersion:     cfg.ClientVersion,
		ResponsesLite:     cfg.ResponsesLite,
		StreamIdleTimeout: cfg.StreamIdleTimeout,
		DefaultModel:      cfg.DefaultModel,
		HighModel:         cfg.HighModel,
		SmallModel:        cfg.SmallModel,
	})
	if err != nil {
		return nil, err
	}
	return model.NewClient(provider)
}

// NewOllamaModelClient constructs a model.Client backed by a local Ollama
// server. The client is not registered automatically; pass it to RegisterModel
// with the model ID your planners use.
func (r *Runtime) NewOllamaModelClient(cfg OllamaConfig) (model.Client, error) {
	serverURL := strings.TrimSpace(cfg.ServerURL)
	if serverURL == "" {
		return nil, errors.New("ollama: server URL is required")
	}
	if strings.TrimSpace(cfg.DefaultModel) == "" {
		return nil, errors.New("ollama: default model is required")
	}
	provider, err := ollamafeature.New(ollamafeature.Options{
		HTTPClient:   cfg.HTTPClient,
		ServerURL:    serverURL,
		DefaultModel: cfg.DefaultModel,
		HighModel:    cfg.HighModel,
		SmallModel:   cfg.SmallModel,
		MaxTokens:    cfg.MaxTokens,
		Temperature:  cfg.Temperature,
		Timeout:      cfg.Timeout,
	})
	if err != nil {
		return nil, err
	}
	return model.NewClient(provider)
}

// NewGeminiModelClient constructs a model.Client backed by the Gemini API using
// Google's official Gen AI SDK. The client is not registered automatically; pass
// it to RegisterModel with the model ID your planners use.
func (r *Runtime) NewGeminiModelClient(ctx context.Context, cfg GeminiConfig) (model.Client, error) {
	apiKey := strings.TrimSpace(cfg.APIKey)
	if apiKey == "" {
		return nil, errors.New("gemini: api key is required")
	}
	if strings.TrimSpace(cfg.DefaultModel) == "" {
		return nil, errors.New("gemini: default model is required")
	}
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return nil, fmt.Errorf("gemini client init: %w", err)
	}
	provider, err := gemini.New(gemini.Options{
		Client:         client.Models,
		DefaultModel:   cfg.DefaultModel,
		HighModel:      cfg.HighModel,
		SmallModel:     cfg.SmallModel,
		MaxTokens:      cfg.MaxTokens,
		ThinkingBudget: cfg.ThinkingBudget,
		Temperature:    cfg.Temperature,
	})
	if err != nil {
		return nil, err
	}
	return model.NewClient(provider)
}

// NewVertexGeminiModelClient constructs a model.Client backed by Vertex AI
// Gemini using Google's official Gen AI SDK. Authentication is delegated to the
// SDK's Vertex backend, which uses Application Default Credentials when no
// explicit credentials are supplied by the environment.
func (r *Runtime) NewVertexGeminiModelClient(ctx context.Context, cfg VertexConfig) (model.Client, error) {
	projectID := strings.TrimSpace(cfg.ProjectID)
	apiKey := strings.TrimSpace(cfg.APIKey)
	if apiKey == "" && projectID == "" {
		return nil, errors.New("vertex: project id is required")
	}
	location := strings.TrimSpace(cfg.Location)
	if apiKey == "" && location == "" {
		return nil, errors.New("vertex: location is required")
	}
	if strings.TrimSpace(cfg.DefaultModel) == "" {
		return nil, errors.New("vertex: default model is required")
	}
	provider, err := gemini.NewFromVertex(ctx, gemini.VertexOptions{
		ProjectID:      projectID,
		Location:       location,
		APIKey:         apiKey,
		Credentials:    cfg.Credentials,
		HTTPClient:     cfg.HTTPClient,
		HTTPOptions:    cfg.HTTPOptions,
		DefaultModel:   cfg.DefaultModel,
		HighModel:      cfg.HighModel,
		SmallModel:     cfg.SmallModel,
		MaxTokens:      cfg.MaxTokens,
		ThinkingBudget: cfg.ThinkingBudget,
		Temperature:    cfg.Temperature,
	})
	if err != nil {
		return nil, err
	}
	return model.NewClient(provider)
}
