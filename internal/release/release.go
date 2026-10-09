// Package release publishes immutable, CI-verified source commits on GitHub.
package release

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

type (
	// Config selects an alpha source or an existing alpha to promote.
	Config struct {
		// Mode is alpha, daily, or promote.
		Mode string
		// Source is the exact full commit SHA for alpha and daily publication.
		Source string
		// Alpha is the existing prerelease tag to promote.
		Alpha string
		// Version optionally names an alpha explicitly; promotion requires a stable version.
		Version string
		// Root is the full-history checkout containing the publisher.
		Root string
	}
	command   func(context.Context, string, ...string) ([]byte, error)
	publisher struct {
		config Config
		run    command
	}
	tag             struct{ version, sha string }
	releaseMetadata struct {
		Tag        string `json:"tag_name"`
		Body       string `json:"body"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
	}
)

const (
	repository        = "CaliLuke/loom-mcp"
	modeAlpha         = "alpha"
	modePromote       = "promote"
	statusCompleted   = "completed"
	conclusionSuccess = "success"
	branchMain        = "main"
)

// Run verifies eligibility and publishes without modifying source or main.
// The caller must serialize publication using the workflow's repository-wide lock.
func Run(ctx context.Context, config Config) error {
	if config.Root == "" {
		config.Root = "."
	}
	p := publisher{config: config}
	p.run = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		var cmd *exec.Cmd
		switch name {
		case "git":
			// #nosec G204 -- Fixed executable; internal Git argv uses validated refs and canonical remotes, never a shell.
			cmd = exec.CommandContext(ctx, "git", args...)
		case "gh":
			// #nosec G204 -- Fixed executable; internal GitHub argv uses validated refs, a fixed repository and private file paths, never a shell.
			cmd = exec.CommandContext(ctx, "gh", args...)
		default:
			return nil, fmt.Errorf("unsupported release command %q", name)
		}
		cmd.Dir = config.Root
		output, err := cmd.CombinedOutput()
		if err != nil {
			return nil, fmt.Errorf("%s: %w: %s", name, err, output)
		}
		return output, nil
	}
	return p.publish(ctx)
}

// Dispatch starts the trusted main-branch workflow; local callers never publish tags.
func Dispatch(ctx context.Context, config Config) error {
	payload, err := dispatchPayload(config)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "gh", "workflow", "run", "release.yml", "--repo", repository, "--ref", branchMain, "--json")
	cmd.Stdin = bytes.NewReader(payload)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("dispatch release: %w", err)
	}
	return nil
}

func dispatchArgs() []string {
	return []string{"workflow", "run", "release.yml", "--repo", repository, "--ref", branchMain, "--json"}
}

func dispatchPayload(config Config) ([]byte, error) {
	if config.Mode != modeAlpha && config.Mode != modePromote {
		return nil, errors.New("mode must be alpha or promote")
	}
	if config.Mode == modeAlpha && !shaPattern.MatchString(config.Source) {
		return nil, errors.New("alpha requires a full source SHA")
	}
	if config.Mode == modePromote {
		if err := validatePromotion(config.Alpha, config.Version); err != nil {
			return nil, err
		}
	}
	return json.Marshal(struct {
		Mode    string `json:"mode"`
		Source  string `json:"source"`
		Alpha   string `json:"alpha"`
		Version string `json:"version"`
	}{config.Mode, config.Source, config.Alpha, config.Version})
}

func (p publisher) publish(ctx context.Context) error {
	if p.config.Mode != modeAlpha && p.config.Mode != "daily" && p.config.Mode != modePromote {
		return errors.New("invalid release mode")
	}
	source, version, tags, err := p.selectPublication(ctx)
	if err != nil {
		return err
	}
	if version == "" {
		fmt.Println("No alpha: this source is released or the release train is closed.")
		return nil
	}
	existing, err := p.releaseIfExists(ctx, version)
	if err != nil {
		return err
	}
	if existing != nil && !existing.Draft {
		if err := p.verifyPublication(ctx, *existing, version, source); err != nil {
			return err
		}
		fmt.Printf("Release %s already published; no new commits to release.\n", version)
		return nil
	}
	evidence, err := p.waitForCI(ctx, source)
	if err != nil {
		return err
	}
	return p.publishRelease(ctx, source, version, tags, existing, evidence)
}

func (p publisher) selectPublication(ctx context.Context) (string, string, []tag, error) {
	if err := p.validateOrigin(ctx); err != nil {
		return "", "", nil, err
	}
	if _, err := p.git(ctx, "fetch", "origin", branchMain, "--tags"); err != nil {
		return "", "", nil, err
	}
	tags, err := p.tags(ctx)
	if err != nil {
		return "", "", nil, err
	}
	source, base, err := p.selectSource(ctx, tags)
	if err != nil {
		return "", "", nil, err
	}
	version := p.config.Version
	if p.config.Mode == modePromote && base != version {
		return "", "", nil, errors.New("promotion version does not match the source release train")
	}
	if p.config.Mode != modePromote {
		selected, err := chooseAlpha(base, source, tags)
		if err != nil {
			return "", "", nil, err
		}
		if selected == "" {
			return "", "", nil, nil
		}
		if version != "" && version != selected {
			return "", "", nil, fmt.Errorf("alpha version must be %s for this source", selected)
		}
		version = selected
	}
	if err := p.validateTarget(ctx, source, version, tags); err != nil {
		return "", "", nil, err
	}
	return source, version, tags, nil
}

func (p publisher) validateOrigin(ctx context.Context) error {
	fetchURLs, err := p.git(ctx, "remote", "get-url", "--all", "origin")
	if err != nil {
		return err
	}
	if !hasSingleCanonicalURL(fetchURLs) {
		return errors.New("origin fetch destination must be exactly one canonical loom-mcp URL")
	}
	pushURLs, err := p.git(ctx, "remote", "get-url", "--push", "--all", "origin")
	if err != nil {
		return err
	}
	if !hasSingleCanonicalURL(pushURLs) {
		return errors.New("origin push destination must be exactly one canonical loom-mcp URL")
	}
	return nil
}

func hasSingleCanonicalURL(urls string) bool {
	lines := strings.Split(strings.TrimSpace(urls), "\n")
	if len(lines) != 1 {
		return false
	}
	url := strings.TrimSpace(lines[0])
	return url == "https://github.com/"+repository+".git" ||
		url == "https://github.com/"+repository ||
		url == "git@github.com:"+repository+".git"
}

func (p publisher) git(ctx context.Context, args ...string) (string, error) {
	data, err := p.run(ctx, "git", args...)
	return strings.TrimSpace(string(data)), err
}

func (p publisher) release(ctx context.Context, version string) (releaseMetadata, error) {
	data, err := p.run(ctx, "gh", "api", "repos/"+repository+"/releases/tags/"+version)
	if err != nil {
		return releaseMetadata{}, err
	}
	var result releaseMetadata
	if err := json.Unmarshal(data, &result); err != nil {
		return result, err
	}
	return result, nil
}

func (p publisher) releaseIfExists(ctx context.Context, version string) (*releaseMetadata, error) {
	// Enumerate via a successful API response so authentication/network errors are never absence.
	data, err := p.run(ctx, "gh", "api", "repos/"+repository+"/releases?per_page=100", "--paginate", "--slurp")
	if err != nil {
		return nil, err
	}
	var pages [][]struct {
		Tag string `json:"tag_name"`
	}
	if err := json.Unmarshal(data, &pages); err != nil {
		return nil, err
	}
	for _, page := range pages {
		for _, r := range page {
			if r.Tag == version {
				result, err := p.release(ctx, version)
				return &result, err
			}
		}
	}
	return nil, nil
}

func (p publisher) verifyEvidence(ctx context.Context, version, source string) error {
	data, err := p.run(ctx, "gh", "release", "download", version, "--repo", repository, "--pattern", "release-evidence.json", "--output", "-")
	if err != nil {
		return err
	}
	var evidence ciEvidence
	if err := json.Unmarshal(data, &evidence); err != nil {
		return err
	}
	if evidence.Source != source || len(evidence.Runs) != 1 {
		return errors.New("release evidence does not match source and required workflows")
	}
	for _, workflow := range []string{"ci.yml"} {
		run, ok := evidence.Runs[workflow]
		if !ok || run.SHA != source || run.Branch != "main" || run.Event != "push" || run.Status != statusCompleted || run.Conclusion != conclusionSuccess || run.ID <= 0 || run.Attempt <= 0 {
			return fmt.Errorf("invalid %s release evidence", workflow)
		}
	}
	return nil
}

func validatePublished(r releaseMetadata, version string) error {
	if r.Tag != version || r.Draft || r.Prerelease != strings.Contains(version, "-alpha.") || len(strings.Fields(r.Body)) < 20 {
		return errors.New("release metadata is incomplete or inconsistent")
	}
	return nil
}

// pollPause keeps cancellation responsive while CI completes.
func pollPause(ctx context.Context) error {
	timer := time.NewTimer(15 * time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
