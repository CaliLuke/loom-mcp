package trajectory

import (
	"fmt"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/callrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
)

func (r *runBuilder) readModels() error {
	for _, call := range r.run.Calls {
		r.callSteps[call.Record.Key] = call.Step
		started := call.Started
		step := &ModelStep{
			Key: call.Record.Key, Class: call.Class, Activity: call.Activity, Step: call.Step,
			Model: started.Model, ModelID: started.ModelID,
			SystemHash: started.Request.System, ToolsHash: started.Request.Tools,
			RequestDigest: started.Request.Request, Prompts: started.Prompts,
		}
		if call.Completion != nil {
			completion := call.Completion
			step.Usage, step.Latency, step.Error, step.Facts = completion.Usage, completion.Latency, completion.Error, completion.Facts
			if completion.Response != nil {
				data, err := r.owner.content(*completion.Response)
				if err != nil {
					return err
				}
				response, err := callrecord.DecodeResponse(data)
				if err != nil {
					return err
				}
				if response != nil {
					step.Chosen = response.ToolCalls
				}
			}
		}
		r.group(call.Step).models = append(r.group(call.Step).models, Step{Kind: StepModel, Step: call.Step, Verified: true, Model: step})
		r.totalModel(step)
		if call.Class == runrecord.Adopted {
			r.adopted[call.Record.Key] = true
		}
	}
	return nil
}

func (r *runBuilder) totalModel(step *ModelStep) {
	totals := &r.out.Totals
	totals.ModelCalls++
	totals.ModelCallsByClass[step.Class]++
	if step.Class == runrecord.Adopted {
		totals.AdoptedModelCalls++
	}
	name := step.Usage.Model
	if name == "" {
		name = step.Model
	}
	usage := totals.TokensByModel[name]
	usage.Model = name
	usage.InputTokens += step.Usage.InputTokens
	usage.OutputTokens += step.Usage.OutputTokens
	usage.TotalTokens += step.Usage.TotalTokens
	usage.CacheReadTokens += step.Usage.CacheReadTokens
	usage.CacheWriteTokens += step.Usage.CacheWriteTokens
	totals.TokensByModel[name] = usage
}

func (r *runBuilder) totalTools() error {
	seen := make(map[string]bool, len(r.tools))
	for _, tool := range r.tools {
		r.out.Totals.ToolCalls++
		if tool.Error != nil {
			r.out.Totals.FailedToolCalls++
		}
		arguments, err := callrecord.Encode(tool.Arguments)
		if err != nil {
			return fmt.Errorf("canonical tool arguments: %w", err)
		}
		key := string(tool.Name) + "\x00" + string(arguments)
		if seen[key] {
			r.out.Totals.RepeatedToolCalls++
		}
		seen[key] = true
	}
	return nil
}

// content resolves only self-contained record bytes; a missing address fails closed.
func (b *builder) content(hash runstore.Hash) ([]byte, error) {
	data, ok := b.record.Content[hash]
	if !ok {
		return nil, fmt.Errorf("trajectory: missing content %s", hash)
	}
	return data, nil
}
