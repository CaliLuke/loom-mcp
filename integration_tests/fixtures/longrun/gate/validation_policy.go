package suite

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"example.com/longrun"
	"example.com/longrun/scenarios"
	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/compaction"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/prompt"
)

// This content identity pins the runtime's embedded summary_prompt.tmpl. A
// template change must update this contract and invalidate validation evidence.
const validationSummaryTemplate = "sha256:c213a63837087dad5fb990d74a4fea84f0072c70f4455cce27e9a646268aef95"

// expectedValidationPolicy resolves budget defaults through their public owner.
// Registration-owned summary semantics remain explicit fixture requirements.
func expectedValidationPolicy(scenario, variant, agent string) json.RawMessage {
	registered := compaction.Settings{}
	if !strings.HasSuffix(agent, "_nosum") {
		registered.Summary = compaction.SummaryConfig{Enabled: true, Model: compaction.ContextModel{ID: "summariser"}, Settings: compaction.SummarySettings{Instructions: "Keep planted key = value lines verbatim in order. Later corrections override earlier values."}}
		if strings.HasSuffix(agent, "_reject") {
			registered.Summary.Settings.Instructions += " [[force-summary-rejection]]"
		}
	}
	registered.Prompt = compaction.SummaryPrompt{Ref: prompt.PromptRef{ID: prompt.Ident(agent + ".context.summary"), Version: validationSummaryTemplate}, Template: validationSummaryTemplate}
	for _, candidate := range scenarios.Variants() {
		if candidate.ID != variant {
			continue
		}
		input := &api.RunInput{Policy: candidate.Policy}
		scenarios.ContextFor(scenario).Apply()(input)
		return compaction.Canonical(compaction.Merge(registered, input.Policy.Context))
	}
	panic("unknown validation variant: " + variant)
}

func verifyValidationPolicy(key string, raw json.RawMessage) error {
	parts := strings.Split(key, "/")
	if len(parts) != 3 {
		return fmt.Errorf("invalid effective policy key %s", key)
	}
	var settings compaction.Settings
	if err := compaction.DecodeCanonical(raw, &settings); err != nil {
		return fmt.Errorf("validation effective policy %s: %w", key, err)
	}
	if !bytes.Equal(compaction.Canonical(settings), expectedValidationPolicy(parts[0], parts[1], parts[2])) {
		return fmt.Errorf("validation effective policy differs from required defaults and fixture overrides: %s", key)
	}
	return nil
}

// The full manifest derives this obligation from the validated profile, never
// from an optional marker supplied by an individual observation.
func validationResident(profile longrun.ConfigurationProfile, key eval.AttemptKey) bool {
	support := profile.Supports["planner"]
	return strings.HasPrefix(key.Scenario, "multimodal_") && key.Variant != "baseline" && support.MaxImageBytes == 0 && support.MaxDocumentBytes == 0
}
