// Package skills exposes agent skill directories as MCP resources.
package skills

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

type (
	// PreloadMode controls when model-facing skill tools should preload SKILL.md.
	PreloadMode string

	// ReloadMode controls when model-facing skill tools should refresh disk content.
	ReloadMode string

	// Source is one filesystem root containing skill subdirectories.
	Source struct {
		Root string
	}

	// Metadata describes structured skill frontmatter.
	Metadata struct {
		ID           string      `json:"id"`
		Name         string      `json:"name,omitempty"`
		Description  string      `json:"description,omitempty"`
		AllowedTools []string    `json:"allowed_tools,omitempty"`
		Preload      PreloadMode `json:"preload,omitempty"`
		Reload       ReloadMode  `json:"reload,omitempty"`
	}

	// Resource describes one skill resource exposed through resources/list.
	Resource struct {
		URI         string
		Name        string
		Description string
		MimeType    string
		Metadata    *Metadata
	}

	// Content is one resources/read content item.
	Content struct {
		URI      string
		MimeType string
		Text     *string
		Blob     *string
		Metadata *Metadata
	}

	manifestFile struct {
		Path string `json:"path"`
		Size int64  `json:"size"`
		Hash string `json:"hash"`
	}

	manifest struct {
		Skill    string         `json:"skill"`
		Metadata *Metadata      `json:"metadata,omitempty"`
		Files    []manifestFile `json:"files"`
	}

	skill struct {
		ID          string
		Name        string
		Description string
		Dir         string
		Metadata    Metadata
	}

	frontmatter struct {
		ID           string      `yaml:"id"`
		Name         string      `yaml:"name"`
		Description  string      `yaml:"description"`
		AllowedTools []string    `yaml:"allowed_tools"`
		Preload      PreloadMode `yaml:"preload"`
		Reload       ReloadMode  `yaml:"reload"`
	}
)

const (
	// PreloadNone does not preload skill instructions.
	PreloadNone PreloadMode = "none"
	// PreloadOnStart preloads SKILL.md when the toolset is constructed.
	PreloadOnStart PreloadMode = "on_start"

	// ReloadNever reuses loaded content until the process rebuilds the toolset.
	ReloadNever ReloadMode = "never"
	// ReloadPerCall reloads skill files from disk for every load request.
	ReloadPerCall ReloadMode = "per_call"

	mimeJSON     = "application/json"
	mimeMarkdown = "text/markdown; charset=utf-8"
	mimeText     = "text/plain; charset=utf-8"
)

var (
	// ErrInvalidURI reports a malformed or unsupported skill resource URI.
	ErrInvalidURI = errors.New("invalid skill URI")
	// ErrNotFound reports a missing skill or skill file.
	ErrNotFound = errors.New("skill resource not found")
	// ErrInvalidMetadata reports malformed skill frontmatter.
	ErrInvalidMetadata = errors.New("invalid skill metadata")
)

// List scans sources and returns list-visible skill resources.
func List(ctx context.Context, sources []Source) ([]Resource, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	skills, err := discover(sources)
	if err != nil {
		return nil, err
	}
	resources := make([]Resource, 0, len(skills)*2)
	for _, skill := range skills {
		resources = append(resources,
			Resource{
				URI:         skillURI(skill.ID, "SKILL.md"),
				Name:        skill.Name,
				Description: skill.Description,
				MimeType:    mimeMarkdown,
				Metadata:    cloneMetadata(&skill.Metadata),
			},
			Resource{
				URI:         skillURI(skill.ID, "_manifest"),
				Name:        skill.Name + " manifest",
				Description: "Skill file manifest",
				MimeType:    mimeJSON,
				Metadata:    cloneMetadata(&skill.Metadata),
			},
		)
	}
	return resources, nil
}

// Read reads one skill resource.
func Read(ctx context.Context, sources []Source, rawURI string) (*Content, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	skillName, rel, err := parseURI(rawURI)
	if err != nil {
		return nil, err
	}
	selected, err := findSkill(sources, skillName)
	if err != nil {
		return nil, err
	}
	if rel == "_manifest" {
		return readManifestContent(selected)
	}
	return readFileContent(selected, rel)
}

// MetadataMeta converts skill metadata to MCP protocol _meta content.
func MetadataMeta(metadata *Metadata) map[string]any {
	if metadata == nil {
		return nil
	}
	skill := map[string]any{
		"id":   metadata.ID,
		"name": metadata.Name,
	}
	if metadata.Description != "" {
		skill["description"] = metadata.Description
	}
	if len(metadata.AllowedTools) > 0 {
		skill["allowed_tools"] = cloneStrings(metadata.AllowedTools)
	}
	if metadata.Preload != "" {
		skill["preload"] = metadata.Preload
	}
	if metadata.Reload != "" {
		skill["reload"] = metadata.Reload
	}
	return map[string]any{"skill": skill}
}

func findSkill(sources []Source, skillName string) (skill, error) {
	skills, err := discover(sources)
	if err != nil {
		return skill{}, err
	}
	for _, candidate := range skills {
		if candidate.ID == skillName {
			return candidate, nil
		}
	}
	return skill{}, fmt.Errorf("%w: %s", ErrNotFound, skillName)
}

func readManifestContent(s skill) (*Content, error) {
	text, err := manifestJSON(s)
	if err != nil {
		return nil, err
	}
	return &Content{
		URI:      skillURI(s.ID, "_manifest"),
		MimeType: mimeJSON,
		Text:     &text,
		Metadata: cloneMetadata(&s.Metadata),
	}, nil
}

func readFileContent(s skill, rel string) (*Content, error) {
	data, err := readSkillFile(s.Dir, rel)
	if err != nil {
		return nil, err
	}
	content := &Content{
		URI:      skillURI(s.ID, rel),
		MimeType: mimeType(rel, data),
		Metadata: cloneMetadata(&s.Metadata),
	}
	if utf8.Valid(data) {
		text := string(data)
		content.Text = &text
	} else {
		blob := base64.StdEncoding.EncodeToString(data)
		content.Blob = &blob
	}
	return content, nil
}

func discover(sources []Source) ([]skill, error) {
	seen := make(map[string]string)
	var skills []skill
	for _, source := range sources {
		root := strings.TrimSpace(source.Root)
		if root == "" {
			continue
		}
		entries, err := os.ReadDir(root)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, err
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			dirName := entry.Name()
			dir := filepath.Join(root, dirName)
			mainPath := filepath.Join(dir, "SKILL.md")
			if info, err := os.Stat(mainPath); err != nil || info.IsDir() {
				continue
			}
			metadata, err := readMetadata(dirName, dir)
			if err != nil {
				return nil, err
			}
			if prev, ok := seen[metadata.ID]; ok {
				return nil, fmt.Errorf("%w: duplicate skill id %q in %s and %s", ErrInvalidMetadata, metadata.ID, prev, dir)
			}
			seen[metadata.ID] = dir
			skills = append(skills, skill{
				ID:          metadata.ID,
				Name:        metadata.Name,
				Description: metadata.Description,
				Dir:         dir,
				Metadata:    metadata,
			})
		}
	}
	sort.Slice(skills, func(i, j int) bool {
		return skills[i].ID < skills[j].ID
	})
	return skills, nil
}

func readMetadata(dirName string, skillDir string) (Metadata, error) {
	data, err := readSkillFile(skillDir, "SKILL.md")
	if err != nil {
		return Metadata{}, err
	}
	text := string(data)
	metadata := Metadata{
		ID:      dirName,
		Name:    dirName,
		Preload: PreloadNone,
		Reload:  ReloadNever,
	}
	if strings.HasPrefix(text, "---\n") {
		if err := applyFrontmatter(&metadata, skillDir, text); err != nil {
			return Metadata{}, err
		}
	}
	if metadata.Description == "" {
		metadata.Description = fallbackDescription(text)
	}
	if err := validateMetadata(metadata); err != nil {
		return Metadata{}, err
	}
	return metadata, nil
}

func applyFrontmatter(metadata *Metadata, skillDir string, text string) error {
	front, _, ok := splitFrontmatter(text)
	if !ok {
		return fmt.Errorf("%w: unterminated frontmatter in %s", ErrInvalidMetadata, skillDir)
	}
	parsed, err := parseFrontmatter(front)
	if err != nil {
		return err
	}
	mergeFrontmatter(metadata, parsed)
	return nil
}

func parseFrontmatter(front string) (frontmatter, error) {
	var parsed frontmatter
	dec := yaml.NewDecoder(bytes.NewReader([]byte(front)))
	dec.KnownFields(true)
	if err := dec.Decode(&parsed); err != nil {
		return frontmatter{}, fmt.Errorf("%w: %w", ErrInvalidMetadata, err)
	}
	return parsed, nil
}

func mergeFrontmatter(metadata *Metadata, parsed frontmatter) {
	if strings.TrimSpace(parsed.ID) != "" {
		metadata.ID = strings.TrimSpace(parsed.ID)
	}
	if strings.TrimSpace(parsed.Name) != "" {
		metadata.Name = strings.TrimSpace(parsed.Name)
	}
	if strings.TrimSpace(parsed.Description) != "" {
		metadata.Description = strings.TrimSpace(parsed.Description)
	}
	metadata.AllowedTools = cloneStrings(parsed.AllowedTools)
	if parsed.Preload != "" {
		metadata.Preload = parsed.Preload
	}
	if parsed.Reload != "" {
		metadata.Reload = parsed.Reload
	}
}

func splitFrontmatter(text string) (string, string, bool) {
	if !strings.HasPrefix(text, "---\n") {
		return "", text, false
	}
	end := strings.Index(text[4:], "\n---")
	if end < 0 {
		return "", "", false
	}
	bodyStart := 4 + end + len("\n---")
	if len(text) > bodyStart && text[bodyStart] == '\n' {
		bodyStart++
	}
	return text[4 : 4+end], text[bodyStart:], true
}

func fallbackDescription(text string) string {
	if strings.HasPrefix(text, "---\n") {
		if _, body, ok := splitFrontmatter(text); ok {
			text = body
		}
	}
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(strings.TrimPrefix(line, "#"))
		if trimmed != "" && trimmed != "---" {
			return trimmed
		}
	}
	return ""
}

func validateMetadata(metadata Metadata) error {
	if metadata.ID == "" {
		return fmt.Errorf("%w: id is required", ErrInvalidMetadata)
	}
	for _, tool := range metadata.AllowedTools {
		if strings.TrimSpace(tool) == "" {
			return fmt.Errorf("%w: allowed_tools contains an empty value", ErrInvalidMetadata)
		}
	}
	switch metadata.Preload {
	case "", PreloadNone, PreloadOnStart:
	default:
		return fmt.Errorf("%w: unknown preload mode %q", ErrInvalidMetadata, metadata.Preload)
	}
	switch metadata.Reload {
	case "", ReloadNever, ReloadPerCall:
	default:
		return fmt.Errorf("%w: unknown reload mode %q", ErrInvalidMetadata, metadata.Reload)
	}
	return nil
}

func manifestJSON(s skill) (string, error) {
	var files []manifestFile
	err := filepath.WalkDir(s.Dir, func(filePath string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		rel, err := filepath.Rel(s.Dir, filePath)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		data, err := readSkillFile(s.Dir, rel)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		files = append(files, manifestFile{
			Path: rel,
			Size: info.Size(),
			Hash: "sha256:" + hex.EncodeToString(sum[:]),
		})
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Slice(files, func(i, j int) bool {
		return files[i].Path < files[j].Path
	})
	data, err := json.Marshal(manifest{Skill: s.ID, Metadata: cloneMetadata(&s.Metadata), Files: files})
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func cloneMetadata(metadata *Metadata) *Metadata {
	if metadata == nil {
		return nil
	}
	out := *metadata
	out.AllowedTools = cloneStrings(metadata.AllowedTools)
	return &out
}

func cloneStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, len(values))
	copy(out, values)
	return out
}

func parseURI(rawURI string) (string, string, error) {
	u, err := url.Parse(rawURI)
	if err != nil {
		return "", "", err
	}
	if u.Scheme != "skill" || u.Host == "" {
		return "", "", fmt.Errorf("%w: %s", ErrInvalidURI, rawURI)
	}
	rel := strings.TrimPrefix(u.EscapedPath(), "/")
	if rel == "" {
		return "", "", fmt.Errorf("%w: missing path", ErrInvalidURI)
	}
	unescaped, err := url.PathUnescape(rel)
	if err != nil {
		return "", "", err
	}
	for _, part := range strings.Split(unescaped, "/") {
		if part == ".." {
			return "", "", fmt.Errorf("%w: unsafe path", ErrInvalidURI)
		}
	}
	cleaned := path.Clean("/" + unescaped)
	if cleaned == "/" {
		return "", "", fmt.Errorf("%w: unsafe path", ErrInvalidURI)
	}
	return u.Host, strings.TrimPrefix(cleaned, "/"), nil
}

func readSkillFile(root, rel string) ([]byte, error) {
	if err := validateSkillPath(rel); err != nil {
		return nil, err
	}
	if err := rejectSymlinkPath(root, rel); err != nil {
		return nil, err
	}
	data, err := fs.ReadFile(os.DirFS(root), rel)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s", ErrNotFound, rel)
		}
		return nil, err
	}
	return data, nil
}

func rejectSymlinkPath(root, rel string) error {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	parts := strings.Split(rel, "/")
	current := rootAbs
	for _, part := range parts {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("%w: %s", ErrNotFound, rel)
			}
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: unsafe symlink", ErrInvalidURI)
		}
	}
	return nil
}

func validateSkillPath(rel string) error {
	if rel == "" || rel == "." || path.IsAbs(rel) || !fs.ValidPath(rel) {
		return fmt.Errorf("%w: unsafe path", ErrInvalidURI)
	}
	return nil
}

func skillURI(skillName, rel string) string {
	escapedParts := strings.Split(rel, "/")
	for i, part := range escapedParts {
		escapedParts[i] = url.PathEscape(part)
	}
	return "skill://" + skillName + "/" + strings.Join(escapedParts, "/")
}

func mimeType(rel string, data []byte) string {
	switch strings.ToLower(filepath.Ext(rel)) {
	case ".md", ".markdown":
		return mimeMarkdown
	case ".txt":
		return mimeText
	case ".json":
		return mimeJSON
	case ".yaml", ".yml":
		return "application/yaml"
	case ".csv":
		return "text/csv; charset=utf-8"
	case ".html", ".htm":
		return "text/html; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".js", ".mjs":
		return "text/javascript; charset=utf-8"
	case ".svg":
		return "image/svg+xml"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".pdf":
		return "application/pdf"
	case ".zip":
		return "application/zip"
	case ".gz", ".gzip":
		return "application/gzip"
	case ".toml":
		return "application/toml"
	}
	if utf8.Valid(data) {
		return mimeText
	}
	return "application/octet-stream"
}
