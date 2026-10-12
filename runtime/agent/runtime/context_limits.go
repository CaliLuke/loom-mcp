package runtime

import (
	"context"
	"fmt"
	"maps"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextmeasure"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

type contextLimitsOverride struct {
	inner        model.ContextInfo
	registeredID string
	overrides    map[string]model.ContextLimits
}

// WithContextLimits sets per-model limits overrides. Positive fields replace
// adapter values; zero fields retain them. Invalid values panic at construction.
func WithContextLimits(overrides map[string]model.ContextLimits) RuntimeOption {
	return func(opts *Options) {
		opts.ContextLimits = overrides
	}
}

// ContextLimits resolves provider limits before applying a model-key override,
// falling back to the registered ID when no resolved-model entry exists.
func (c *contextLimitsOverride) ContextLimits(ctx context.Context, req *model.Request) (model.ContextLimits, string, bool) {
	var limits model.ContextLimits
	resolved := req.Model
	if c.inner != nil {
		limits, resolved, _ = c.inner.ContextLimits(ctx, req)
	}
	if resolved == "" {
		resolved = c.registeredID
	}
	override, found := c.overrides[resolved]
	if !found {
		override = c.overrides[c.registeredID]
	}
	if override.ContextWindow > 0 {
		limits.ContextWindow = override.ContextWindow
	}
	if override.MaxOutputTokens > 0 {
		limits.MaxOutputTokens = override.MaxOutputTokens
	}
	if override.DefaultMaxTokens > 0 {
		limits.DefaultMaxTokens = override.DefaultMaxTokens
	}
	if override.ImageTokens > 0 {
		limits.ImageTokens = override.ImageTokens
	}
	limits.InvalidOverride = limits.ContextWindow > 0 && (limits.MaxOutputTokens > limits.ContextWindow || contextmeasure.OutputReserve(req, limits) >= limits.ContextWindow)
	return limits, resolved, limits.ContextWindow > 0 && !limits.InvalidOverride
}

// cloneContextLimits validates configuration and takes ownership before runtime
// registration. A supplied window must accommodate a supplied output cap.
func cloneContextLimits(overrides map[string]model.ContextLimits) map[string]model.ContextLimits {
	for id, limits := range overrides {
		if limits.ContextWindow < 0 || limits.MaxOutputTokens < 0 || limits.DefaultMaxTokens < 0 || limits.ImageTokens < 0 {
			panic(fmt.Sprintf("runtime: context limits for %q must not be negative", id))
		}
		if limits.ContextWindow > 0 && limits.MaxOutputTokens > limits.ContextWindow {
			panic(fmt.Sprintf("runtime: context limits for %q max output tokens exceeds context window", id))
		}
	}
	return maps.Clone(overrides)
}

// withContextLimits decorates registration without changing counting support.
func (r *Runtime) withContextLimits(id string, client model.Client) model.Client {
	if len(r.contextLimits) == 0 {
		return client
	}
	counter, _ := client.(model.TokenCounter)
	info, _ := client.(model.ContextInfo)
	override := &contextLimitsOverride{inner: info, registeredID: id, overrides: r.contextLimits}
	parts, _ := client.(model.ToolResultPartsInfo)
	return model.WithCapabilitySet(client, model.Capabilities{Counter: counter, Info: override, ToolResultParts: parts})
}
