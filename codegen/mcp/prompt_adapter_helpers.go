package codegen

import (
	"encoding/json"

	mcpexpr "github.com/CaliLuke/loom-mcp/expr/mcp"
	"github.com/CaliLuke/loom/codegen"
	"github.com/CaliLuke/loom/expr"
)

// buildDynamicPromptAdapters creates adapter data for dynamic prompts.
func (g *adapterGenerator) buildDynamicPromptAdapters() []*DynamicPromptAdapter {
	var adapters []*DynamicPromptAdapter

	if mcpexpr.Root != nil {
		dynamicPrompts := mcpexpr.Root.DynamicPrompts[g.originalService.Name]
		for _, dp := range dynamicPrompts {
			hasRealPayload := dp.Method.Payload != nil && dp.Method.Payload.Type != expr.Empty

			adapter := &DynamicPromptAdapter{
				Name:               dp.Name,
				Description:        dp.Description,
				OriginalMethodName: codegen.Goify(dp.Method.Name, true),
				HasPayload:         hasRealPayload,
			}

			if hasRealPayload {
				adapter.PayloadType = g.getTypeReference(dp.Method.Payload)
				adapter.Arguments = promptArgsFromPayload(dp.Method.Payload)
				adapter.ExampleArguments = buildExampleJSON(dp.Method.Payload)
			} else {
				adapter.ExampleArguments = "{}"
			}

			if dp.Method.Result != nil {
				adapter.ResultType = g.getTypeReference(dp.Method.Result)
			}

			adapters = append(adapters, adapter)
		}
	}

	return adapters
}

// buildStaticPrompts creates data for static prompts.
func (g *adapterGenerator) buildStaticPrompts() []*StaticPromptAdapter {
	prompts := make([]*StaticPromptAdapter, 0, len(g.mcp.Prompts))

	for _, prompt := range g.mcp.Prompts {
		adapter := &StaticPromptAdapter{
			Name:        prompt.Name,
			Description: prompt.Description,
			Messages:    make([]*PromptMessageAdapter, len(prompt.Messages)),
		}

		for i, msg := range prompt.Messages {
			adapter.Messages[i] = &PromptMessageAdapter{
				Role:    msg.Role,
				Content: msg.Content,
			}
		}

		prompts = append(prompts, adapter)
	}

	return prompts
}

// buildExampleJSON produces a minimal valid JSON string for the given payload attribute.
func buildExampleJSON(attr *expr.AttributeExpr) string {
	if attr == nil || attr.Type == nil || attr.Type == expr.Empty {
		return "{}"
	}
	r := &expr.ExampleGenerator{Randomizer: expr.NewDeterministicRandomizer()}
	v := attr.Example(r)
	if v == nil {
		return "{}"
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// promptArgsFromPayload builds a flat list of prompt arguments from a payload attribute (top-level only).
func promptArgsFromPayload(attr *expr.AttributeExpr) []PromptArg {
	if attr == nil || attr.Type == nil || attr.Type == expr.Empty {
		return nil
	}
	if ut, ok := attr.Type.(expr.UserType); ok {
		return promptArgsFromPayload(ut.Attribute())
	}
	obj, ok := attr.Type.(*expr.Object)
	if !ok {
		return nil
	}
	out := make([]PromptArg, 0, len(*obj))
	required := map[string]struct{}{}
	if attr.Validation != nil {
		for _, n := range attr.Validation.Required {
			required[n] = struct{}{}
		}
	}
	for _, nat := range *obj {
		name := nat.Name
		desc := ""
		if nat.Attribute != nil && nat.Attribute.Description != "" {
			desc = nat.Attribute.Description
		}
		_, req := required[name]
		out = append(out, PromptArg{Name: name, Description: desc, Required: req})
	}
	return out
}
