package runtime

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"cloud.google.com/go/auth"
	bedrock "github.com/CaliLuke/loom-mcp/features/model/bedrock"
	gemini "github.com/CaliLuke/loom-mcp/features/model/gemini"
	ollamafeature "github.com/CaliLuke/loom-mcp/features/model/ollama"
	openaifeature "github.com/CaliLuke/loom-mcp/features/model/openai"
	"github.com/CaliLuke/loom-mcp/runtime/agent/model"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	openaisdk "github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
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
func (r *Runtime) RegisterModel(id string, client model.Client) error {
	if id == "" {
		return errors.New("model id is required")
	}
	if client == nil {
		return errors.New("model client is required")
	}
	r.mu.Lock()
	r.models[id] = client
	r.mu.Unlock()
	return nil
}

// ModelClient returns a registered model client by ID, if present.
func (r *Runtime) ModelClient(id string) (model.Client, bool) {
	if id == "" {
		return nil, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	m, ok := r.models[id]
	return m, ok
}

// NewBedrockModelClient constructs a model.Client backed by AWS Bedrock using the
// runtime's own ledger access.
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
	if querier, ok := r.Engine.(bedrock.WorkflowQuerier); ok {
		return bedrock.New(awsrt, opts, bedrock.NewTemporalLedgerSource(querier))
	}
	return bedrock.New(awsrt, opts, nil)
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
	return openaifeature.New(openaifeature.Options{
		Client:       &client.Responses,
		DefaultModel: cfg.DefaultModel,
		HighModel:    cfg.HighModel,
		SmallModel:   cfg.SmallModel,
	})
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
	return ollamafeature.New(ollamafeature.Options{
		HTTPClient:   cfg.HTTPClient,
		ServerURL:    serverURL,
		DefaultModel: cfg.DefaultModel,
		HighModel:    cfg.HighModel,
		SmallModel:   cfg.SmallModel,
		MaxTokens:    cfg.MaxTokens,
		Temperature:  cfg.Temperature,
		Timeout:      cfg.Timeout,
	})
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
	return gemini.New(gemini.Options{
		Client:         client.Models,
		DefaultModel:   cfg.DefaultModel,
		HighModel:      cfg.HighModel,
		SmallModel:     cfg.SmallModel,
		MaxTokens:      cfg.MaxTokens,
		ThinkingBudget: cfg.ThinkingBudget,
		Temperature:    cfg.Temperature,
	})
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
	return gemini.NewFromVertex(ctx, gemini.VertexOptions{
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
}
