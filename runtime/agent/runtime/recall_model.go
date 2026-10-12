package runtime

import (
	"slices"
	"strings"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextmeasure"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/recall"
)

// recoveryToolBytes measures only definitions missing from the failed request,
// using the same insertion rules and measurement as the retried request.
func recoveryToolBytes(req *model.Request, defs []*model.ToolDefinition) (int, error) {
	retry := &model.Request{Tools: slices.Clone(req.Tools), ToolChoice: req.ToolChoice}
	applyExtraTools(retry, defs)
	added := retry.Tools[len(req.Tools):]
	if len(added) == 0 {
		return 0, nil
	}
	facts, err := contextmeasure.Measure(&model.Request{Tools: added}, model.ContextLimits{}, nil)
	return facts.TextBytes, err
}

func applyExtraTools(req *model.Request, defs []*model.ToolDefinition) {
	if req == nil || req.ToolChoice != nil && req.ToolChoice.Mode == model.ToolChoiceModeNone {
		return
	}
	for _, def := range defs {
		found := false
		for _, existing := range req.Tools {
			if existing != nil && existing.Name == def.Name {
				found = true
				break
			}
		}
		if !found {
			req.Tools = append(req.Tools, def)
		}
	}
}

// validateRecallNames rejects both canonical and ASCII-provider name exposure.
func validateRecallNames(specs []tools.ToolSpec) error {
	for _, spec := range specs {
		if err := recall.ValidateToolName(spec.Name, providerRecallName(spec.Name.String())); err != nil {
			return err
		}
	}
	return nil
}

func providerRecallName(name string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' {
			return r
		}
		return '_'
	}, name)
}
