package release

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type fakePublication struct {
	tags       map[string]string
	remoteTags map[string]string
	releases   map[string]releaseMetadata
	evidence   map[string][]byte
	commands   []string
	failAt     string
	failAfter  string
	sha        string
}

func (f *fakePublication) run(_ context.Context, name string, args ...string) ([]byte, error) {
	joined := name + " " + strings.Join(args, " ")
	f.commands = append(f.commands, joined)
	if f.failAt != "" && strings.Contains(joined, f.failAt) {
		f.failAt = ""
		return nil, fmt.Errorf("interrupted %s", joined)
	}
	if name == "git" {
		switch args[0] {
		case "cat-file":
			return []byte("tag"), nil
		case "remote":
			return []byte("https://github.com/CaliLuke/loom-mcp.git"), nil
		case "fetch":
			for version, sha := range f.remoteTags {
				f.tags[version] = sha
			}
			return nil, nil
		case "merge-base":
			return nil, nil
		case "show":
			return []byte(`{"version":"v2.1.0"}`), nil
		case "for-each-ref":
			var lines []string
			for version, sha := range f.tags {
				lines = append(lines, version+" tag object "+sha)
			}
			return []byte(strings.Join(lines, "\n")), nil
		case "log":
			return []byte("- fix: preserve request body errors (123abcd)"), nil
		case "tag":
			f.tags[args[2]] = args[3]
			return nil, nil
		case "push":
			version := strings.TrimPrefix(args[2], "refs/tags/")
			f.remoteTags[version] = f.tags[version]
			if f.failAfter == "git push" {
				f.failAfter = ""
				return nil, fmt.Errorf("connection lost after durable push")
			}
			return nil, nil
		case "ls-remote":
			version := strings.TrimSuffix(strings.TrimPrefix(args[2], "refs/tags/"), "^{}")
			if sha := f.remoteTags[version]; sha != "" {
				return []byte(sha + "\t" + args[2]), nil
			}
			return nil, nil
		}
	}
	if name == "gh" && args[0] == "api" {
		if strings.Contains(args[1], "/workflows/") {
			return json.Marshal(map[string]any{"workflow_runs": []ciRun{{ID: 1, Attempt: 1, SHA: f.sha, Branch: "main", Event: "push", Status: "completed", Conclusion: "success"}}})
		}
		if strings.Contains(args[1], "/jobs") {
			return []byte(`[{"jobs":[{"name":"Release eligibility","status":"completed","conclusion":"success"}]}]`), nil
		}
		if strings.Contains(args[1], "/releases/tags/") {
			version := strings.TrimPrefix(args[1], "repos/CaliLuke/loom-mcp/releases/tags/")
			return json.Marshal(f.releases[version])
		}
		var page []map[string]string
		for version := range f.releases {
			page = append(page, map[string]string{"tag_name": version})
		}
		return json.Marshal([]any{page})
	}
	if name == "gh" && args[0] == "release" {
		version := args[2]
		switch args[1] {
		case "view":
			return json.Marshal(f.releases[version])
		case "create":
			f.releases[version] = releaseMetadata{Tag: version, Draft: true}
			return nil, nil
		case "upload":
			data, err := os.ReadFile(args[3])
			f.evidence[version] = data
			return nil, err
		case "download":
			return f.evidence[version], nil
		case "edit":
			r := f.releases[version]
			r.Draft = false
			for i, arg := range args {
				if arg == "--notes-file" {
					data, err := os.ReadFile(args[i+1])
					if err != nil {
						return nil, err
					}
					r.Body = string(data)
				}
				if arg == "--prerelease=true" {
					r.Prerelease = true
				}
			}
			f.releases[version] = r
			return nil, nil
		}
	}
	return nil, fmt.Errorf("unexpected command %s", joined)
}

func TestPublicationRetriesAndNoNewCommits(t *testing.T) {
	for _, failure := range []string{"", "git push", "release create", "release upload", "release edit", "git push after durable"} {
		t.Run(failure, func(t *testing.T) {
			f := fakePublication{tags: map[string]string{}, remoteTags: map[string]string{}, releases: map[string]releaseMetadata{}, evidence: map[string][]byte{}, sha: strings.Repeat("a", 40), failAt: failure}
			if failure == "git push after durable" {
				f.failAt = ""
				f.failAfter = "git push"
			}
			p := publisher{config: Config{Mode: "daily", Source: f.sha}, run: f.run}
			err := p.publish(context.Background())
			if failure != "" {
				require.Error(t, err)
				require.NoError(t, p.publish(context.Background()))
			} else {
				require.NoError(t, err)
			}
			require.Len(t, f.tags, 1)
			require.Equal(t, f.sha, f.tags["v2.1.0-alpha.1"])
			require.False(t, f.releases["v2.1.0-alpha.1"].Draft)
			f.commands = nil
			require.NoError(t, p.publish(context.Background()))
			require.Len(t, f.tags, 1)
			for _, cmd := range f.commands {
				require.NotContains(t, cmd, "git tag ")
				require.NotContains(t, cmd, "release create")
				require.NotContains(t, cmd, "/workflows/")
				require.NotContains(t, cmd, "make")
			}
			// A later publication must not prevent verification of this immutable release.
			f.tags["v2.1.0-alpha.2"] = strings.Repeat("b", 40)
			f.remoteTags["v2.1.0-alpha.2"] = strings.Repeat("b", 40)
			require.NoError(t, p.publish(context.Background()))
			require.Len(t, f.tags, 2)
			f.commands = nil
			promote := publisher{config: Config{Mode: "promote", Alpha: "v2.1.0-alpha.1", Version: "v2.1.0"}, run: f.run}
			require.NoError(t, promote.publish(context.Background()))
			require.Equal(t, f.tags["v2.1.0-alpha.1"], f.tags["v2.1.0"])
			require.False(t, f.releases["v2.1.0"].Prerelease)
			for _, cmd := range f.commands {
				require.NotContains(t, cmd, "git commit")
				require.NotContains(t, cmd, "refs/heads/main")
			}
		})
	}
}

func TestConflictingPromotionTag(t *testing.T) {
	f := fakePublication{sha: strings.Repeat("a", 40), tags: map[string]string{"v2.1.0-alpha.1": strings.Repeat("a", 40), "v2.1.0": strings.Repeat("b", 40)}, remoteTags: map[string]string{"v2.1.0-alpha.1": strings.Repeat("a", 40), "v2.1.0": strings.Repeat("b", 40)}, releases: map[string]releaseMetadata{"v2.1.0-alpha.1": {Tag: "v2.1.0-alpha.1", Prerelease: true}}}
	p := publisher{config: Config{Mode: "promote", Alpha: "v2.1.0-alpha.1", Version: "v2.1.0"}, run: f.run}
	require.ErrorContains(t, p.publish(context.Background()), "another source")
	for _, cmd := range f.commands {
		require.NotContains(t, cmd, "git push")
	}
}

func TestRejectsNoncanonicalPushURLBeforeFetchOrPublication(t *testing.T) {
	var commands []string
	p := publisher{
		config: Config{Mode: "daily", Source: strings.Repeat("a", 40)},
		run: func(_ context.Context, name string, args ...string) ([]byte, error) {
			commands = append(commands, name+" "+strings.Join(args, " "))
			switch strings.Join(args, " ") {
			case "remote get-url --all origin":
				return []byte("https://github.com/CaliLuke/loom-mcp.git"), nil
			case "remote get-url --push --all origin":
				return []byte("https://github.com/attacker/other.git\n"), nil
			case "fetch origin main --tags":
				return nil, fmt.Errorf("fetch must not run after invalid push URL")
			default:
				return nil, fmt.Errorf("unexpected command %s %s", name, strings.Join(args, " "))
			}
		},
	}
	err := p.publish(context.Background())
	require.ErrorContains(t, err, "push destination")
	require.Equal(t, []string{
		"git remote get-url --all origin",
		"git remote get-url --push --all origin",
	}, commands)
}
