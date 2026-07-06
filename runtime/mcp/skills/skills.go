// Package skills exposes agent skill directories as MCP resources.
package skills

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"mime"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

type (
	// Source is one filesystem root containing skill subdirectories.
	Source struct {
		Root string
	}

	// Resource describes one skill resource exposed through resources/list.
	Resource struct {
		URI         string
		Name        string
		Description string
		MimeType    string
	}

	// Content is one resources/read content item.
	Content struct {
		URI      string
		MimeType string
		Text     *string
		Blob     *string
	}

	manifestFile struct {
		Path string `json:"path"`
		Size int64  `json:"size"`
		Hash string `json:"hash"`
	}

	manifest struct {
		Skill string         `json:"skill"`
		Files []manifestFile `json:"files"`
	}

	skill struct {
		Name        string
		Description string
		Dir         string
	}
)

var (
	// ErrInvalidURI reports a malformed or unsupported skill resource URI.
	ErrInvalidURI = errors.New("invalid skill URI")
	// ErrNotFound reports a missing skill or skill file.
	ErrNotFound = errors.New("skill resource not found")
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
				URI:         skillURI(skill.Name, "SKILL.md"),
				Name:        skill.Name,
				Description: skill.Description,
				MimeType:    "text/markdown; charset=utf-8",
			},
			Resource{
				URI:         skillURI(skill.Name, "_manifest"),
				Name:        skill.Name + " manifest",
				Description: "Skill file manifest",
				MimeType:    "application/json",
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

func findSkill(sources []Source, skillName string) (skill, error) {
	skills, err := discover(sources)
	if err != nil {
		return skill{}, err
	}
	for _, candidate := range skills {
		if candidate.Name == skillName {
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
		URI:      skillURI(s.Name, "_manifest"),
		MimeType: "application/json",
		Text:     &text,
	}, nil
}

func readFileContent(s skill, rel string) (*Content, error) {
	data, err := readSkillFile(s.Dir, rel)
	if err != nil {
		return nil, err
	}
	content := &Content{
		URI:      skillURI(s.Name, rel),
		MimeType: mimeType(rel, data),
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
	seen := make(map[string]struct{})
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
			name := entry.Name()
			if _, ok := seen[name]; ok {
				continue
			}
			dir := filepath.Join(root, name)
			mainPath := filepath.Join(dir, "SKILL.md")
			if info, err := os.Stat(mainPath); err != nil || info.IsDir() {
				continue
			}
			description, err := readDescription(dir)
			if err != nil {
				return nil, err
			}
			seen[name] = struct{}{}
			skills = append(skills, skill{Name: name, Description: description, Dir: dir})
		}
	}
	sort.Slice(skills, func(i, j int) bool {
		return skills[i].Name < skills[j].Name
	})
	return skills, nil
}

func readDescription(skillDir string) (string, error) {
	data, err := readSkillFile(skillDir, "SKILL.md")
	if err != nil {
		return "", err
	}
	text := string(data)
	if strings.HasPrefix(text, "---\n") {
		if end := strings.Index(text[4:], "\n---"); end >= 0 {
			frontmatter := text[4 : 4+end]
			for _, line := range strings.Split(frontmatter, "\n") {
				key, value, ok := strings.Cut(line, ":")
				if ok && strings.TrimSpace(key) == "description" {
					return strings.Trim(strings.TrimSpace(value), `"`), nil
				}
			}
		}
	}
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(strings.TrimPrefix(line, "#"))
		if trimmed != "" && trimmed != "---" {
			return trimmed, nil
		}
	}
	return "", nil
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
	data, err := json.Marshal(manifest{Skill: s.Name, Files: files})
	if err != nil {
		return "", err
	}
	return string(data), nil
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
	if ext := filepath.Ext(rel); ext != "" {
		if detected := mime.TypeByExtension(ext); detected != "" {
			return detected
		}
	}
	if utf8.Valid(data) {
		return "text/plain; charset=utf-8"
	}
	return "application/octet-stream"
}
