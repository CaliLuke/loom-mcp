package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	mcpskills "github.com/CaliLuke/loom-mcp/runtime/mcp/skills"

	"github.com/CaliLuke/loom-mcp/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/runtime/agent/tools"
)

type (
	// SkillToolsetConfig configures a model-facing skill toolset.
	SkillToolsetConfig struct {
		// Name is the toolset name used to construct canonical tool IDs.
		Name string
		// Roots are filesystem directories containing child skill directories.
		Roots []string
	}

	skillListItem struct {
		Name        string `json:"name"`
		Description string `json:"description,omitempty"`
		URI         string `json:"uri"`
	}

	skillListResult struct {
		Skills []skillListItem `json:"skills"`
	}

	loadSkillPayload struct {
		Skill string `json:"skill"`
	}

	loadSkillResourcePayload struct {
		Skill string `json:"skill"`
		Path  string `json:"path"`
	}

	loadSkillResult struct {
		URI      string  `json:"uri"`
		MimeType string  `json:"mime_type,omitempty"`
		Text     *string `json:"text,omitempty"`
		Blob     *string `json:"blob,omitempty"`
	}
)

const (
	skillToolList         = "list_skills"
	skillToolLoad         = "load_skill"
	skillToolLoadResource = "load_skill_resource"
)

// NewSkillToolsetRegistration exposes skill directories as ordinary model
// tools. This is complementary to MCP skill:// resources: planners can advertise
// these tools directly to a model, while generated MCP servers can continue to
// expose the same files through resources/list and resources/read.
func NewSkillToolsetRegistration(cfg SkillToolsetConfig) ToolsetRegistration {
	name := strings.TrimSpace(cfg.Name)
	if name == "" {
		name = "skills"
	}
	sources := skillSourcesFromRoots(cfg.Roots)
	return ToolsetRegistration{
		Name:        name,
		Description: "Model-facing tools for discovering and loading local agent skills.",
		Execute: func(ctx context.Context, call *planner.ToolRequest) (*ToolExecutionResult, error) {
			return executeSkillTool(ctx, sources, call)
		},
		Specs: []tools.ToolSpec{
			skillToolSpec(name, skillToolList, "List available skills."),
			skillToolSpec(name, skillToolLoad, "Load a skill's SKILL.md instructions."),
			skillToolSpec(name, skillToolLoadResource, "Load a supporting file from a skill directory."),
		},
	}
}

func executeSkillTool(ctx context.Context, sources []mcpskills.Source, call *planner.ToolRequest) (*ToolExecutionResult, error) {
	switch call.Name.Tool() {
	case skillToolList:
		return executeListSkills(ctx, sources, call)
	case skillToolLoad:
		var payload loadSkillPayload
		if err := decodeSkillPayload(call, &payload); err != nil {
			return nil, err
		}
		return executeLoadSkill(ctx, sources, call, payload.Skill, "SKILL.md")
	case skillToolLoadResource:
		var payload loadSkillResourcePayload
		if err := decodeSkillPayload(call, &payload); err != nil {
			return nil, err
		}
		return executeLoadSkill(ctx, sources, call, payload.Skill, payload.Path)
	default:
		return nil, fmt.Errorf("unknown skill tool %q", call.Name)
	}
}

func executeListSkills(ctx context.Context, sources []mcpskills.Source, call *planner.ToolRequest) (*ToolExecutionResult, error) {
	resources, err := mcpskills.List(ctx, sources)
	if err != nil {
		return nil, err
	}
	result := skillListResult{}
	for _, resource := range resources {
		if !strings.HasSuffix(resource.URI, "/SKILL.md") {
			continue
		}
		result.Skills = append(result.Skills, skillListItem{
			Name:        resource.Name,
			Description: resource.Description,
			URI:         resource.URI,
		})
	}
	return Executed(&planner.ToolResult{Name: call.Name, ToolCallID: call.ToolCallID, Result: result}), nil
}

func executeLoadSkill(ctx context.Context, sources []mcpskills.Source, call *planner.ToolRequest, skillName, rel string) (*ToolExecutionResult, error) {
	if strings.TrimSpace(skillName) == "" {
		return nil, fmt.Errorf("skill is required")
	}
	if strings.TrimSpace(rel) == "" {
		return nil, fmt.Errorf("path is required")
	}
	content, err := mcpskills.Read(ctx, sources, skillURI(skillName, rel))
	if err != nil {
		return nil, err
	}
	result := loadSkillResult{
		URI:      content.URI,
		MimeType: content.MimeType,
		Text:     content.Text,
		Blob:     content.Blob,
	}
	return Executed(&planner.ToolResult{Name: call.Name, ToolCallID: call.ToolCallID, Result: result}), nil
}

func decodeSkillPayload(call *planner.ToolRequest, out any) error {
	if len(call.Payload) == 0 {
		return nil
	}
	return json.Unmarshal(call.Payload.RawMessage(), out)
}

func skillToolSpec(toolset, tool, description string) tools.ToolSpec {
	id := tools.Ident(toolset + "." + tool)
	return tools.ToolSpec{
		Name:        id,
		Toolset:     toolset,
		Description: description,
		Payload:     tools.TypeSpec{Codec: tools.AnyJSONCodec},
		Result:      tools.TypeSpec{Codec: tools.AnyJSONCodec},
	}
}

func skillSourcesFromRoots(roots []string) []mcpskills.Source {
	sources := make([]mcpskills.Source, 0, len(roots))
	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		sources = append(sources, mcpskills.Source{Root: root})
	}
	return sources
}

func skillURI(skillName, rel string) string {
	return "skill://" + skillName + "/" + strings.TrimPrefix(rel, "/")
}
