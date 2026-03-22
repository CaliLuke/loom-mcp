package bedrock

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	brtypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"

	"github.com/CaliLuke/loom-mcp/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/runtime/agent/telemetry"
	"github.com/CaliLuke/loom-mcp/runtime/agent/tools"
)

type messageEncodeState struct {
	ctx           context.Context
	nameMap       map[string]string
	logger        telemetry.Logger
	toolUseIDMap  map[string]string
	nextToolUseID int
	docNameMap    map[string]string
	usedDocNames  map[string]struct{}
	nextDocNameID int
}

func newMessageEncodeState(ctx context.Context, nameMap map[string]string, logger telemetry.Logger) *messageEncodeState {
	return &messageEncodeState{
		ctx:          ctx,
		nameMap:      nameMap,
		logger:       logger,
		toolUseIDMap: make(map[string]string),
		docNameMap:   make(map[string]string),
		usedDocNames: make(map[string]struct{}),
	}
}

func (s *messageEncodeState) encodeSystemParts(parts []model.Part) ([]brtypes.SystemContentBlock, error) {
	system := make([]brtypes.SystemContentBlock, 0, len(parts))
	for _, p := range parts {
		switch v := p.(type) {
		case model.TextPart:
			if v.Text != "" {
				system = append(system, &brtypes.SystemContentBlockMemberText{Value: v.Text})
			}
		case model.CacheCheckpointPart:
			system = append(system, &brtypes.SystemContentBlockMemberCachePoint{
				Value: brtypes.CachePointBlock{Type: brtypes.CachePointTypeDefault},
			})
		case model.DocumentPart:
			return nil, errors.New("bedrock: document parts are not supported in system messages")
		}
	}
	return system, nil
}

func (s *messageEncodeState) encodeMessageParts(role model.ConversationRole, parts []model.Part) ([]brtypes.ContentBlock, error) {
	blocks := make([]brtypes.ContentBlock, 0, 1+len(parts))
	for _, part := range parts {
		block, err := s.encodePart(role, part)
		if err != nil {
			return nil, err
		}
		if block != nil {
			blocks = append(blocks, block)
		}
	}
	return blocks, nil
}

func (s *messageEncodeState) encodePart(role model.ConversationRole, part model.Part) (brtypes.ContentBlock, error) {
	switch v := part.(type) {
	case model.ThinkingPart:
		return encodeThinkingPart(v), nil
	case model.TextPart:
		if v.Text == "" {
			return nil, nil
		}
		return &brtypes.ContentBlockMemberText{Value: v.Text}, nil
	case model.ImagePart:
		return encodeImagePart(role, v)
	case model.DocumentPart:
		return s.encodeDocumentPart(role, v)
	case model.ToolUsePart:
		return s.encodeToolUsePart(v)
	case model.ToolResultPart:
		return s.encodeToolResultPart(v), nil
	case model.CacheCheckpointPart:
		return &brtypes.ContentBlockMemberCachePoint{
			Value: brtypes.CachePointBlock{Type: brtypes.CachePointTypeDefault},
		}, nil
	default:
		return nil, nil
	}
}

func encodeThinkingPart(v model.ThinkingPart) brtypes.ContentBlock {
	if v.Signature != "" && v.Text != "" {
		return &brtypes.ContentBlockMemberReasoningContent{
			Value: &brtypes.ReasoningContentBlockMemberReasoningText{
				Value: brtypes.ReasoningTextBlock{
					Text:      aws.String(v.Text),
					Signature: aws.String(v.Signature),
				},
			},
		}
	}
	if len(v.Redacted) > 0 {
		return &brtypes.ContentBlockMemberReasoningContent{
			Value: &brtypes.ReasoningContentBlockMemberRedactedContent{
				Value: v.Redacted,
			},
		}
	}
	return nil
}

func encodeImagePart(role model.ConversationRole, v model.ImagePart) (brtypes.ContentBlock, error) {
	if role != model.ConversationRoleUser {
		return nil, fmt.Errorf("bedrock: image parts are only supported in user messages (role=%s)", role)
	}
	var format brtypes.ImageFormat
	switch v.Format {
	case model.ImageFormatPNG:
		format = brtypes.ImageFormatPng
	case model.ImageFormatJPEG:
		format = brtypes.ImageFormatJpeg
	case model.ImageFormatGIF:
		format = brtypes.ImageFormatGif
	case model.ImageFormatWEBP:
		format = brtypes.ImageFormatWebp
	default:
		return nil, fmt.Errorf("bedrock: unsupported image format %q", v.Format)
	}
	return &brtypes.ContentBlockMemberImage{
		Value: brtypes.ImageBlock{
			Format: format,
			Source: &brtypes.ImageSourceMemberBytes{Value: v.Bytes},
		},
	}, nil
}

func (s *messageEncodeState) encodeDocumentPart(role model.ConversationRole, v model.DocumentPart) (brtypes.ContentBlock, error) {
	if role != model.ConversationRoleUser {
		return nil, fmt.Errorf("bedrock: document parts are only supported in user messages (role=%s)", role)
	}
	if v.Name == "" {
		return nil, errors.New("bedrock: document part requires Name")
	}
	source, isS3Source, err := buildDocumentSource(v)
	if err != nil {
		return nil, err
	}
	doc := brtypes.DocumentBlock{
		Name:   aws.String(docNameFor(v.Name, s.docNameMap, s.usedDocNames, &s.nextDocNameID)),
		Source: source,
	}
	if v.Format != "" {
		doc.Format = brtypes.DocumentFormat(v.Format)
	}
	if v.Cite && !isS3Source {
		doc.Citations = &brtypes.CitationsConfig{Enabled: aws.Bool(true)}
	}
	if v.Context != "" {
		doc.Context = aws.String(v.Context)
	}
	return &brtypes.ContentBlockMemberDocument{Value: doc}, nil
}

func buildDocumentSource(v model.DocumentPart) (brtypes.DocumentSource, bool, error) {
	switch {
	case len(v.Bytes) > 0:
		return &brtypes.DocumentSourceMemberBytes{Value: v.Bytes}, false, nil
	case len(v.Chunks) > 0:
		chunks := make([]brtypes.DocumentContentBlock, 0, len(v.Chunks))
		for i, chunk := range v.Chunks {
			if chunk == "" {
				return nil, false, fmt.Errorf("bedrock: document part requires non-empty Chunks[%d]", i)
			}
			chunks = append(chunks, &brtypes.DocumentContentBlockMemberText{Value: chunk})
		}
		return &brtypes.DocumentSourceMemberContent{Value: chunks}, false, nil
	case v.URI != "":
		if !strings.HasPrefix(v.URI, "s3://") {
			return nil, false, fmt.Errorf("bedrock: document URI scheme not supported: %q", v.URI)
		}
		s3 := brtypes.S3Location{Uri: aws.String(v.URI)}
		return &brtypes.DocumentSourceMemberS3Location{Value: s3}, true, nil
	case v.Text != "":
		return &brtypes.DocumentSourceMemberText{Value: v.Text}, false, nil
	default:
		return nil, false, errors.New("bedrock: document part requires one of Bytes, Text, Chunks, or URI")
	}
}

func (s *messageEncodeState) encodeToolUsePart(v model.ToolUsePart) (brtypes.ContentBlock, error) {
	tb := brtypes.ToolUseBlock{}
	if v.Name != "" {
		if sanitized, ok := s.nameMap[v.Name]; ok && sanitized != "" {
			tb.Name = aws.String(sanitized)
		} else {
			unavailable := tools.ToolUnavailable.String()
			sanitized, ok := s.nameMap[unavailable]
			if !ok || sanitized == "" {
				return nil, fmt.Errorf(
					"bedrock: tool_use in messages references %q which is not in the current tool configuration and tool_unavailable is not available",
					v.Name,
				)
			}
			tb.Name = aws.String(sanitized)
			tb.Input = toDocument(s.ctx, map[string]any{
				"requested_tool":    v.Name,
				"requested_payload": v.Input,
			}, s.logger)
		}
	}
	if v.ID != "" {
		if id := toolUseIDFor(v.ID, s.toolUseIDMap, &s.nextToolUseID); id != "" {
			tb.ToolUseId = aws.String(id)
		}
	}
	if tb.Input == nil {
		tb.Input = toDocument(s.ctx, v.Input, s.logger)
	}
	return &brtypes.ContentBlockMemberToolUse{Value: tb}, nil
}

func (s *messageEncodeState) encodeToolResultPart(v model.ToolResultPart) brtypes.ContentBlock {
	tr := brtypes.ToolResultBlock{}
	if id := toolUseIDFor(v.ToolUseID, s.toolUseIDMap, &s.nextToolUseID); id != "" {
		tr.ToolUseId = aws.String(id)
	}
	if text, ok := v.Content.(string); ok {
		tr.Content = []brtypes.ToolResultContentBlock{
			&brtypes.ToolResultContentBlockMemberText{Value: text},
		}
	} else {
		doc := toDocument(s.ctx, v.Content, s.logger)
		tr.Content = []brtypes.ToolResultContentBlock{
			&brtypes.ToolResultContentBlockMemberJson{Value: doc},
		}
	}
	return &brtypes.ContentBlockMemberToolResult{Value: tr}
}

func conversationRole(role model.ConversationRole) brtypes.ConversationRole {
	if role == model.ConversationRoleUser {
		return brtypes.ConversationRoleUser
	}
	return brtypes.ConversationRoleAssistant
}

func appendToolCacheCheckpoint(toolList []brtypes.Tool) []brtypes.Tool {
	return append(toolList, &brtypes.ToolMemberCachePoint{
		Value: brtypes.CachePointBlock{Type: brtypes.CachePointTypeDefault},
	})
}

func buildToolConfiguration(choice *model.ToolChoice, defs []*model.ToolDefinition, toolList []brtypes.Tool, sanToCanon map[string]string) (*brtypes.ToolConfiguration, error) {
	if choice == nil {
		return &brtypes.ToolConfiguration{Tools: toolList}, nil
	}
	cfg := brtypes.ToolConfiguration{Tools: toolList}
	switch choice.Mode {
	case "", model.ToolChoiceModeAuto:
	case model.ToolChoiceModeNone:
	case model.ToolChoiceModeAny:
		cfg.ToolChoice = &brtypes.ToolChoiceMemberAny{Value: brtypes.AnyToolChoice{}}
	case model.ToolChoiceModeTool:
		if choice.Name == "" {
			return nil, fmt.Errorf("bedrock: tool choice mode %q requires a tool name", choice.Mode)
		}
		if !hasToolDefinition(defs, choice.Name) {
			return nil, fmt.Errorf("bedrock: tool choice name %q does not match any tool", choice.Name)
		}
		sanitized := SanitizeToolName(choice.Name)
		if canonical, ok := sanToCanon[sanitized]; !ok || canonical != choice.Name {
			return nil, fmt.Errorf("bedrock: tool choice name %q does not match any tool", choice.Name)
		}
		cfg.ToolChoice = &brtypes.ToolChoiceMemberTool{
			Value: brtypes.SpecificToolChoice{Name: aws.String(sanitized)},
		}
	default:
		return nil, fmt.Errorf("bedrock: unsupported tool choice mode %q", choice.Mode)
	}
	return &cfg, nil
}

func buildToolSpecifications(ctx context.Context, defs []*model.ToolDefinition, logger telemetry.Logger) ([]brtypes.Tool, map[string]string, map[string]string, error) {
	toolList := make([]brtypes.Tool, 0, len(defs))
	canonToSan := make(map[string]string, len(defs))
	sanToCanon := make(map[string]string, len(defs))
	for _, def := range defs {
		if def == nil || def.Name == "" {
			continue
		}
		sanitized := SanitizeToolName(def.Name)
		if prev, ok := sanToCanon[sanitized]; ok && prev != def.Name {
			return nil, nil, nil, fmt.Errorf(
				"bedrock: tool name %q sanitizes to %q which collides with %q",
				def.Name, sanitized, prev,
			)
		}
		if def.Description == "" {
			return nil, nil, nil, fmt.Errorf("bedrock: tool %q is missing description", def.Name)
		}
		sanToCanon[sanitized] = def.Name
		canonToSan[def.Name] = sanitized
		spec := brtypes.ToolSpecification{
			Name:        aws.String(sanitized),
			Description: aws.String(def.Description),
			InputSchema: &brtypes.ToolInputSchemaMemberJson{Value: toDocument(ctx, def.InputSchema, logger)},
		}
		toolList = append(toolList, &brtypes.ToolMemberToolSpec{Value: spec})
	}
	return toolList, canonToSan, sanToCanon, nil
}
