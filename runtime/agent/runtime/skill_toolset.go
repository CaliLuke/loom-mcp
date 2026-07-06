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
		// Preload controls global skill preloading when skill metadata does not opt in.
		Preload mcpskills.PreloadMode
		// Reload controls global skill reload behavior when skill metadata does not override it.
		Reload mcpskills.ReloadMode
	}

	skillListItem struct {
		ID           string                `json:"id,omitempty"`
		Name         string                `json:"name"`
		Description  string                `json:"description,omitempty"`
		URI          string                `json:"uri"`
		AllowedTools []string              `json:"allowed_tools,omitempty"`
		Preload      mcpskills.PreloadMode `json:"preload,omitempty"`
		Reload       mcpskills.ReloadMode  `json:"reload,omitempty"`
		Metadata     *mcpskills.Metadata   `json:"metadata,omitempty"`
		Preloaded    bool                  `json:"preloaded,omitempty"`
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
		URI      string              `json:"uri"`
		MimeType string              `json:"mime_type,omitempty"`
		Text     *string             `json:"text,omitempty"`
		Blob     *string             `json:"blob,omitempty"`
		Metadata *mcpskills.Metadata `json:"metadata,omitempty"`
		Reloaded bool                `json:"reloaded,omitempty"`
	}

	skillToolExecutor struct {
		sources []mcpskills.Source
		preload mcpskills.PreloadMode
		reload  mcpskills.ReloadMode
		cache   map[string]*mcpskills.Content
	}
)

const (
	skillToolList         = "list_skills"
	skillToolLoad         = "load_skill"
	skillToolLoadResource = "load_skill_resource"

	// SkillPreloadNone leaves skill instructions unloaded until requested.
	SkillPreloadNone = mcpskills.PreloadNone
	// SkillPreloadOnStart preloads SKILL.md when the toolset registration is built.
	SkillPreloadOnStart = mcpskills.PreloadOnStart
	// SkillReloadNever reuses loaded content until the toolset registration is rebuilt.
	SkillReloadNever = mcpskills.ReloadNever
	// SkillReloadPerCall reloads skill files for each load call.
	SkillReloadPerCall = mcpskills.ReloadPerCall
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
	exec := newSkillToolExecutor(cfg)
	return ToolsetRegistration{
		Name:        name,
		Description: "Model-facing tools for discovering and loading local agent skills.",
		Execute: func(ctx context.Context, call *planner.ToolRequest) (*ToolExecutionResult, error) {
			return exec.execute(ctx, call)
		},
		Specs: []tools.ToolSpec{
			skillToolSpec(name, skillToolList, "List available skills."),
			skillToolSpec(name, skillToolLoad, "Load a skill's SKILL.md instructions."),
			skillToolSpec(name, skillToolLoadResource, "Load a supporting file from a skill directory."),
		},
	}
}

func newSkillToolExecutor(cfg SkillToolsetConfig) *skillToolExecutor {
	exec := &skillToolExecutor{
		sources: skillSourcesFromRoots(cfg.Roots),
		preload: cfg.Preload,
		reload:  cfg.Reload,
		cache:   make(map[string]*mcpskills.Content),
	}
	if exec.reload == "" {
		exec.reload = mcpskills.ReloadNever
	}
	exec.preloadSkills(context.Background())
	return exec
}

func (e *skillToolExecutor) preloadSkills(ctx context.Context) {
	resources, err := mcpskills.List(ctx, e.sources)
	if err != nil {
		return
	}
	for _, resource := range resources {
		if !strings.HasSuffix(resource.URI, "/SKILL.md") {
			continue
		}
		if e.preload != mcpskills.PreloadOnStart && (resource.Metadata == nil || resource.Metadata.Preload != mcpskills.PreloadOnStart) {
			continue
		}
		content, err := mcpskills.Read(ctx, e.sources, resource.URI)
		if err == nil {
			e.cache[resource.URI] = content
		}
	}
}

func (e *skillToolExecutor) execute(ctx context.Context, call *planner.ToolRequest) (*ToolExecutionResult, error) {
	switch call.Name.Tool() {
	case skillToolList:
		return e.executeListSkills(ctx, call)
	case skillToolLoad:
		var payload loadSkillPayload
		if err := decodeSkillPayload(call, &payload); err != nil {
			return nil, err
		}
		return e.executeLoadSkill(ctx, call, payload.Skill, "SKILL.md")
	case skillToolLoadResource:
		var payload loadSkillResourcePayload
		if err := decodeSkillPayload(call, &payload); err != nil {
			return nil, err
		}
		return e.executeLoadSkill(ctx, call, payload.Skill, payload.Path)
	default:
		return nil, fmt.Errorf("unknown skill tool %q", call.Name)
	}
}

func (e *skillToolExecutor) executeListSkills(ctx context.Context, call *planner.ToolRequest) (*ToolExecutionResult, error) {
	resources, err := mcpskills.List(ctx, e.sources)
	if err != nil {
		return nil, err
	}
	result := skillListResult{}
	for _, resource := range resources {
		if !strings.HasSuffix(resource.URI, "/SKILL.md") {
			continue
		}
		item := skillListItem{
			Name:        resource.Name,
			Description: resource.Description,
			URI:         resource.URI,
			Metadata:    resource.Metadata,
			Preloaded:   e.cache[resource.URI] != nil,
		}
		if resource.Metadata != nil {
			item.ID = resource.Metadata.ID
			item.AllowedTools = append([]string(nil), resource.Metadata.AllowedTools...)
			item.Preload = resource.Metadata.Preload
			item.Reload = resource.Metadata.Reload
		}
		result.Skills = append(result.Skills, item)
	}
	return Executed(&planner.ToolResult{Name: call.Name, ToolCallID: call.ToolCallID, Result: result}), nil
}

func (e *skillToolExecutor) executeLoadSkill(ctx context.Context, call *planner.ToolRequest, skillName, rel string) (*ToolExecutionResult, error) {
	if strings.TrimSpace(skillName) == "" {
		return nil, fmt.Errorf("skill is required")
	}
	if strings.TrimSpace(rel) == "" {
		return nil, fmt.Errorf("path is required")
	}
	uri := skillURI(skillName, rel)
	content, reloaded, err := e.readSkillContent(ctx, uri)
	if err != nil {
		return nil, err
	}
	result := loadSkillResult{
		URI:      content.URI,
		MimeType: content.MimeType,
		Text:     content.Text,
		Blob:     content.Blob,
		Metadata: content.Metadata,
		Reloaded: reloaded,
	}
	return Executed(&planner.ToolResult{Name: call.Name, ToolCallID: call.ToolCallID, Result: result}), nil
}

func (e *skillToolExecutor) readSkillContent(ctx context.Context, uri string) (*mcpskills.Content, bool, error) {
	if content := e.cache[uri]; content != nil && e.reload != mcpskills.ReloadPerCall {
		if content.Metadata == nil || content.Metadata.Reload != mcpskills.ReloadPerCall {
			return content, false, nil
		}
	}
	content, err := mcpskills.Read(ctx, e.sources, uri)
	if err != nil {
		return nil, false, err
	}
	perCall := e.reload == mcpskills.ReloadPerCall || (content.Metadata != nil && content.Metadata.Reload == mcpskills.ReloadPerCall)
	if !perCall {
		e.cache[uri] = content
	}
	return content, perCall, nil
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
