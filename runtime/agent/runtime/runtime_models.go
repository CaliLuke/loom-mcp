package runtime

import (
	"errors"

	bedrock "github.com/CaliLuke/loom-mcp/features/model/bedrock"
	"github.com/CaliLuke/loom-mcp/runtime/agent/model"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
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
