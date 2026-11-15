package release

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const authoredTestNotes = `Agents can now recover earlier evidence after context compaction.

## Upgrade

Use github.com/CaliLuke/loom-mcp/v2@{{version}}, drain existing workflows, and regenerate with loom gen <module-import-path>/design before starting new workers.
`

func TestReleaseNotesUseAuthoredSourceAndPreviousStable(t *testing.T) {
	for _, version := range []string{"v2.1.0-alpha.44", "v2.1.0"} {
		t.Run(version, func(t *testing.T) {
			p := publisher{run: func(_ context.Context, name string, args ...string) ([]byte, error) {
				assert.Equal(t, "git", name)
				assert.Equal(t, []string{"show", "selected-source:docs/releases/v2.1.0.md"}, args)
				return []byte(authoredTestNotes), nil
			}}
			tags := []tag{{"v2.0.1", "older"}, {"v2.0.0", "oldest"}, {"v2.1.0-alpha.43", "alpha"}, {"v2.2.0", "newer"}}
			notes, err := p.notes(t.Context(), version, "selected-source", tags)
			require.NoError(t, err)
			want := strings.ReplaceAll(strings.TrimSpace(authoredTestNotes), "{{version}}", version) +
				"\n\n[Full changelog](https://github.com/CaliLuke/loom-mcp/compare/v2.0.1..." + version + ")\n"
			assert.Equal(t, want, notes)
		})
	}
}

func TestPublicationRejectsMissingOrInsufficientAuthoredNotesBeforeMutation(t *testing.T) {
	for _, mode := range []string{modeAlpha, "daily", modePromote} {
		for _, failure := range []string{"missing", "empty", "whitespace", "too short"} {
			t.Run(mode+"/"+failure, func(t *testing.T) {
				f := newBoundaryPublication()
				config := Config{Mode: mode, Source: f.sha}
				if mode == modePromote {
					config.Alpha, config.Version = "v2.1.0-alpha.1", "v2.1.0"
					f.tags[config.Alpha] = f.sha
					f.remoteTags[config.Alpha] = f.sha
					f.releases[config.Alpha] = releaseMetadata{Tag: config.Alpha, Prerelease: true}
				}
				fault := errors.New("notes file missing")
				p := publisher{config: config, run: func(ctx context.Context, name string, args ...string) ([]byte, error) {
					if name == "git" && len(args) == 2 && args[0] == "show" && args[1] == f.sha+":docs/releases/v2.1.0.md" {
						switch failure {
						case "missing":
							return nil, fault
						case "whitespace":
							return []byte(" \n\t"), nil
						case "too short":
							return []byte("## Highlights\n\nComing soon."), nil
						default:
							return nil, nil
						}
					}
					return f.run(ctx, name, args...)
				}}
				err := p.publish(t.Context())
				require.Error(t, err)
				require.ErrorContains(t, err, "docs/releases/v2.1.0.md")
				if failure == "missing" {
					require.ErrorIs(t, err, fault)
				}
				for _, command := range f.commands {
					assert.NotContains(t, command, "git tag ")
					assert.NotContains(t, command, "git push ")
					assert.NotContains(t, command, "gh release create ")
					assert.NotContains(t, command, "gh release upload ")
					assert.NotContains(t, command, "gh release edit ")
				}
			})
		}
	}
}
