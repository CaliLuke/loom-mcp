package scenarios

import (
	"encoding/json/v2"
	"fmt"
	"strings"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
)

// CheckStructure verifies recorded threshold events and baseline isolation.
func CheckStructure(scenario, variant string, rec *runrecord.RunRecord) error {
	if rec == nil {
		return fmt.Errorf("missing record")
	}
	for _, r := range rec.Runs {
		compactions := 0
		residentEvents := 0
		exhausted := false
		unsupported := false
		attachments := 0
		refilled := false
		previousDiscovery, previousReads := 3, 3
		for _, event := range r.Events {
			if event.Type == hooks.ContextNonTextResident {
				residentEvents++
			}
			if event.Type == hooks.ContextRecalled {
				var data hooks.ContextRecalledEvent
				if err := json.Unmarshal(event.Payload, &data); err != nil {
					return err
				}
				exhausted = exhausted || data.Outcome == "exhausted"
				unsupported = unsupported || data.Outcome == "unsupported"
				attachments += data.Parts
				refilled = refilled || data.RemainingDiscovery > previousDiscovery || data.RemainingReads > previousReads
				previousDiscovery, previousReads = data.RemainingDiscovery, data.RemainingReads
			}
			if event.Type != hooks.ContextCompacted {
				continue
			}
			var data struct {
				Reason string `json:"reason"`
			}
			if err := json.Unmarshal(event.Payload, &data); err != nil {
				return err
			}
			if data.Reason == "threshold" {
				compactions++
			}
		}
		if r.WorkflowProtocolVersion != api.WorkflowProtocolVersion {
			return fmt.Errorf("workflow protocol is %d", r.WorkflowProtocolVersion)
		}
		switch {
		case variant == "baseline":
			for _, call := range r.Calls {
				if call.Completion != nil {
					usage := call.Completion.Usage
					if usage.InputTokens+usage.CacheReadTokens+usage.CacheWriteTokens >= 128000 {
						return fmt.Errorf("baseline request exceeds the strict 128000 input-token limit")
					}
				}
			}
			if compactions != 0 {
				return fmt.Errorf("baseline recorded %d threshold compactions", compactions)
			}
			if r.ContextSettings != nil && r.ContextSettings.ContextWindow != 0 {
				return fmt.Errorf("baseline has a pretend window")
			}
		case strings.HasPrefix(scenario, "multimodal_") && r.NonTextResident:
			if compactions != 0 || residentEvents != 1 || r.ContextSettings == nil || !r.ContextSettings.KeepNonTextResident {
				return fmt.Errorf("resident multimodal run requires zero threshold compactions and one residency event: compactions=%d events=%d", compactions, residentEvents)
			}
		case compactions < 3:
			return fmt.Errorf("%s/%s: only %d threshold compactions", scenario, variant, compactions)
		}
		if scenario == "recall_pressure" && (variant == "receipt_only" || variant == "hybrid") && (!exhausted || !refilled) {
			return fmt.Errorf("pressure must exhaust and refill: exhausted=%v refilled=%v", exhausted, refilled)
		}
		if strings.HasPrefix(scenario, "multimodal_") && (variant == "receipt_only" || variant == "hybrid") && !r.NonTextResident && (unsupported || attachments < 2) {
			return fmt.Errorf("multimodal recall requires both attachments: unsupported=%v parts=%d", unsupported, attachments)
		}
		if scenario == "summariser_rejected" && variant == "hybrid" && r.Summary.Rejections == 0 {
			return fmt.Errorf("hybrid did not reject the forced summary")
		}
		if scenario == "model_change" {
			small := false
			for _, c := range r.Calls {
				if c.Started.ModelID == "planner_small" {
					small = true
				}
			}
			if !small {
				return fmt.Errorf("planner did not switch models")
			}
		}
	}
	return nil
}
