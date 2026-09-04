//nolint:lll // types builder constructs long composite literals for clarity
package codegen

import "github.com/CaliLuke/loom/expr"

// buildMCPTypes declares only the adapter-owned catalog and result types. The
// official SDK owns initialize, capability, pagination, subscription, and all
// other wire-level protocol types.
func (b *mcpExprBuilder) buildMCPTypes() {
	// ContentItem is shared by the adapter's unary tool contract and prompt
	// content conversion. It is the only content envelope Loom retains.
	b.getOrCreateType("ContentItem", b.buildContentItemType)
	if b.hasResources() {
		b.getOrCreateType("ResourceContent", b.buildResourceContentType)
	}
	if b.hasPrompts() {
		b.getOrCreateType("PromptMessage", b.buildPromptMessageType)
		b.getOrCreateType("MessageContent", b.buildMessageContentType)
	}
}

// Tool type builders

func (b *mcpExprBuilder) buildToolsCallPayloadType() *expr.AttributeExpr {
	return &expr.AttributeExpr{
		Type: &expr.Object{
			{Name: "name", Attribute: &expr.AttributeExpr{
				Type:        expr.String,
				Description: "Tool name",
			}},
			{Name: "arguments", Attribute: &expr.AttributeExpr{
				Type:        expr.Any,
				Description: "Tool arguments",
			}},
		},
		Validation: &expr.ValidationExpr{
			Required: []string{"name"},
		},
	}
}

func (b *mcpExprBuilder) buildToolsCallResultType() *expr.AttributeExpr {
	return &expr.AttributeExpr{
		Type: &expr.Object{
			{Name: "content", Attribute: &expr.AttributeExpr{
				Type:        &expr.Array{ElemType: &expr.AttributeExpr{Type: b.getOrCreateType("ContentItem", b.buildContentItemType)}},
				Description: "Tool execution results",
			}},
			{Name: "structuredContent", Attribute: &expr.AttributeExpr{
				Type:        expr.Any,
				Description: "Optional structured result for machine consumers",
			}},
			{Name: "isError", Attribute: &expr.AttributeExpr{
				Type:        expr.Boolean,
				Description: "Whether the tool encountered an error",
			}},
		},
		Validation: &expr.ValidationExpr{
			Required: []string{"content"},
		},
	}
}

func (b *mcpExprBuilder) buildContentItemType() *expr.AttributeExpr {
	return b.buildContentLikeType()
}

// Resource type builders

func (b *mcpExprBuilder) buildResourcesReadPayloadType() *expr.AttributeExpr {
	return &expr.AttributeExpr{
		Type: &expr.Object{
			{Name: "uri", Attribute: &expr.AttributeExpr{
				Type:        expr.String,
				Description: "Resource URI",
				Validation: &expr.ValidationExpr{
					Pattern: "^[a-zA-Z][a-zA-Z0-9+.-]*:.*",
				},
			}},
		},
		Validation: &expr.ValidationExpr{
			Required: []string{"uri"},
		},
	}
}

func (b *mcpExprBuilder) buildResourcesReadResultType() *expr.AttributeExpr {
	return &expr.AttributeExpr{
		Type: &expr.Object{
			{Name: "contents", Attribute: &expr.AttributeExpr{
				Type:        &expr.Array{ElemType: &expr.AttributeExpr{Type: b.getOrCreateType("ResourceContent", b.buildResourceContentType)}},
				Description: "Resource contents",
			}},
		},
		Validation: &expr.ValidationExpr{
			Required: []string{"contents"},
		},
	}
}

func (b *mcpExprBuilder) buildResourceContentType() *expr.AttributeExpr {
	return &expr.AttributeExpr{
		Type: &expr.Object{
			{Name: "uri", Attribute: &expr.AttributeExpr{
				Type:        expr.String,
				Description: "Resource URI",
			}},
			{Name: "mimeType", Attribute: &expr.AttributeExpr{
				Type:        expr.String,
				Description: "Content MIME type",
			}},
			{Name: "text", Attribute: &expr.AttributeExpr{
				Type:        expr.String,
				Description: "Text content",
			}},
			{Name: "blob", Attribute: &expr.AttributeExpr{
				Type:        expr.String,
				Description: "Base64 encoded binary content",
			}},
			{Name: "_meta", Attribute: &expr.AttributeExpr{
				Type:        expr.Any,
				Description: "Resource content metadata",
			}},
		},
		Validation: &expr.ValidationExpr{
			Required: []string{"uri"},
		},
	}
}

// Prompt type builders

func (b *mcpExprBuilder) buildPromptsGetPayloadType() *expr.AttributeExpr {
	return &expr.AttributeExpr{
		Type: &expr.Object{
			{Name: "name", Attribute: &expr.AttributeExpr{
				Type:        expr.String,
				Description: "Prompt name",
			}},
			{Name: "arguments", Attribute: &expr.AttributeExpr{
				Type:        expr.Any,
				Description: "Prompt arguments",
			}},
		},
		Validation: &expr.ValidationExpr{
			Required: []string{"name"},
		},
	}
}

func (b *mcpExprBuilder) buildPromptsGetResultType() *expr.AttributeExpr {
	return &expr.AttributeExpr{
		Type: &expr.Object{
			{Name: "description", Attribute: &expr.AttributeExpr{
				Type:        expr.String,
				Description: "Prompt description",
			}},
			{Name: "messages", Attribute: &expr.AttributeExpr{
				Type:        &expr.Array{ElemType: &expr.AttributeExpr{Type: b.getOrCreateType("PromptMessage", b.buildPromptMessageType)}},
				Description: "Prompt messages",
			}},
		},
		Validation: &expr.ValidationExpr{
			Required: []string{"messages"},
		},
	}
}

func (b *mcpExprBuilder) buildPromptMessageType() *expr.AttributeExpr {
	return &expr.AttributeExpr{
		Type: &expr.Object{
			{Name: "role", Attribute: &expr.AttributeExpr{
				Type:        expr.String,
				Description: "Message role",
				Validation: &expr.ValidationExpr{
					Values: []any{"user", "assistant", "system"},
				},
			}},
			{Name: "content", Attribute: &expr.AttributeExpr{
				Type:        b.getOrCreateType("MessageContent", b.buildMessageContentType),
				Description: "Message content",
			}},
		},
		Validation: &expr.ValidationExpr{
			Required: []string{"role", "content"},
		},
	}
}

func (b *mcpExprBuilder) buildMessageContentType() *expr.AttributeExpr {
	return b.buildContentLikeType()
}

// buildContentLikeType defines the shared structure used by ContentItem and MessageContent.
func (b *mcpExprBuilder) buildContentLikeType() *expr.AttributeExpr {
	return &expr.AttributeExpr{
		Type: &expr.Object{
			{Name: "type", Attribute: &expr.AttributeExpr{
				Type:        expr.String,
				Description: "Content type",
				Validation:  &expr.ValidationExpr{Values: []any{"text", "image", "resource"}},
			}},
			{Name: "text", Attribute: &expr.AttributeExpr{Type: expr.String, Description: "Text content"}},
			{Name: "data", Attribute: &expr.AttributeExpr{Type: expr.String, Description: "Base64 encoded data"}},
			{Name: "mimeType", Attribute: &expr.AttributeExpr{Type: expr.String, Description: "MIME type"}},
			{Name: "uri", Attribute: &expr.AttributeExpr{Type: expr.String, Description: "Resource URI"}},
			{Name: "_meta", Attribute: &expr.AttributeExpr{Type: expr.Any, Description: "Content metadata"}},
		},
		Validation: &expr.ValidationExpr{Required: []string{"type"}},
	}
}
