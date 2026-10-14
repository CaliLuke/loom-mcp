package fake

import (
	"context"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

// ModelProfile is the exact offline model identity and capability declaration.
// Nil profiles retain the small legacy Reader used by isolated unit fixtures.
type ModelProfile struct {
	Provider string
	Model    string
	Support  model.ToolResultPartSupport
	Limits   model.ContextLimits
}

// ProviderConfiguration reports the declared provider route without provider I/O.
func (r *Reader) ProviderConfiguration(context.Context, *model.Request) string {
	if r.Profile == nil {
		return "offline"
	}
	return r.Profile.Provider
}

// ProviderConfiguration preserves the summary model's declared provider route.
func (s *Summariser) ProviderConfiguration(context.Context, *model.Request) string {
	if s.Profile == nil {
		return "offline"
	}
	return s.Profile.Provider
}

// ContextLimits reports the profile's exact model ID and structural test limits.
func (r *Reader) ContextLimits(_ context.Context, req *model.Request) (model.ContextLimits, string, bool) {
	return profileLimits(r.Profile, req)
}

// ContextLimits reports the summary profile's exact model ID and test limits.
func (s *Summariser) ContextLimits(_ context.Context, req *model.Request) (model.ContextLimits, string, bool) {
	return profileLimits(s.Profile, req)
}

func profileLimits(profile *ModelProfile, req *model.Request) (model.ContextLimits, string, bool) {
	if profile != nil {
		return profile.Limits, profile.Model, profile.Limits.ContextWindow > 0
	}
	id := ""
	if req != nil {
		id = req.Model
	}
	return model.ContextLimits{}, id, false
}
