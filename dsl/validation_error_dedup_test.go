package dsl_test

import (
	"strings"
	"testing"

	. "github.com/CaliLuke/loom-mcp/v2/dsl"
	. "github.com/CaliLuke/loom/dsl"
	"github.com/CaliLuke/loom/eval"
	"github.com/stretchr/testify/require"
)

func TestMCPChildValidationErrorsReportedOnce(t *testing.T) {
	tests := []struct {
		name   string
		want   string
		design func()
	}{
		{
			name: "tool",
			want: "tool description is required",
			design: func() {
				Method("broken_tool", func() {
					Result(String)
					Tool("broken_tool", "")
				})
			},
		},
		{
			name: "resource",
			want: "resource URI is required",
			design: func() {
				Method("broken_resource", func() {
					Result(String)
					Resource("broken_resource", "", "text/plain")
				})
			},
		},
		{
			name: "prompt",
			want: "prompt must have at least one message",
			design: func() {
				StaticPrompt("broken_prompt", "Broken prompt")
			},
		},
		{
			name: "message",
			want: "prompt message role must be user or assistant",
			design: func() {
				StaticPrompt("broken_message", "Broken message", "system", "System text")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setupMCPDSL(t)
			design := func() {
				API("test", func() {})
				Service("invalid", func() {
					MCP("invalid", "1.0")
					tt.design()
				})
			}
			require.True(t, eval.Execute(design, nil), eval.Context.Error())
			require.Error(t, eval.RunDSL())

			err := eval.Context.Error()
			require.Equal(t, 1, strings.Count(err, tt.want), err)
		})
	}
}

func TestReferencedToolsetProviderValidationErrorReportedOnce(t *testing.T) {
	resetDSLRoots(t)

	design := func() {
		API("test", func() {})
		shared := Toolset(FromMCP("ghost", "server"))
		Service("consumer", func() {
			Agent("consumer", "Consumer", func() {
				Use(shared)
			})
		})
	}
	require.True(t, eval.Execute(design, nil), eval.Context.Error())
	require.Error(t, eval.RunDSL())

	const want = `FromMCP could not resolve service "ghost"`
	err := eval.Context.Error()
	require.Equal(t, 1, strings.Count(err, want), err)
}

func TestReferencedProviderUsageValidationErrorsReportedOnce(t *testing.T) {
	tests := []struct {
		name   string
		want   string
		design func()
	}{
		{
			name: "artifact clone inline tool",
			want: "FromArtifacts toolsets cannot declare inline tools",
			design: func() {
				shared := Toolset(FromArtifacts())
				Service("consumer", func() {
					Agent("consumer", "Consumer", func() {
						Use(shared, func() {
							Tool("custom", "Custom tool", func() {})
						})
					})
				})
			},
		},
		{
			name: "memory clone inline tool",
			want: "FromMemory toolsets cannot declare inline tools",
			design: func() {
				shared := Toolset(FromMemory())
				Service("consumer", func() {
					Agent("consumer", "Consumer", func() {
						Use(shared, func() {
							Tool("custom", "Custom tool", func() {})
						})
					})
				})
			},
		},
		{
			name: "artifact origin inline tool",
			want: "FromArtifacts toolsets cannot declare inline tools",
			design: func() {
				shared := Toolset(FromArtifacts(), func() {
					Tool("custom", "Custom tool", func() {})
				})
				Service("consumer", func() {
					Agent("consumer", "Consumer", func() {
						Use(shared)
					})
				})
			},
		},
		{
			name: "memory origin inline tool",
			want: "FromMemory toolsets cannot declare inline tools",
			design: func() {
				shared := Toolset(FromMemory(), func() {
					Tool("custom", "Custom tool", func() {})
				})
				Service("consumer", func() {
					Agent("consumer", "Consumer", func() {
						Use(shared)
					})
				})
			},
		},
		{
			name: "origin version",
			want: "Version is only valid for FromRegistry toolsets",
			design: func() {
				shared := Toolset(FromArtifacts(), func() {
					Version("1.0")
				})
				Service("consumer", func() {
					Agent("consumer", "Consumer", func() {
						Use(shared)
					})
				})
			},
		},
		{
			name: "clone version",
			want: "Version is only valid for FromRegistry toolsets",
			design: func() {
				shared := Toolset(FromArtifacts())
				Service("consumer", func() {
					Agent("consumer", "Consumer", func() {
						Use(shared, func() {
							Version("1.0")
						})
					})
				})
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetDSLRoots(t)
			design := func() {
				API("test", func() {})
				tt.design()
			}
			require.True(t, eval.Execute(design, nil), eval.Context.Error())
			require.Error(t, eval.RunDSL())

			err := eval.Context.Error()
			require.Equal(t, 1, strings.Count(err, tt.want), err)
		})
	}
}

func TestMCPParentOwnedValidationErrorsStillReported(t *testing.T) {
	tests := []struct {
		name   string
		want   string
		design func()
	}{
		{
			name: "server icon",
			want: "icon source is required",
			design: func() {
				API("test", func() {})
				Service("invalid", func() {
					MCP("invalid", "1.0", ServerIcons(Icon("")))
				})
			},
		},
		{
			name: "skill directory",
			want: "skill directory root is required",
			design: func() {
				API("test", func() {})
				Service("invalid", func() {
					MCP("invalid", "1.0", SkillDirectory(""))
				})
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setupMCPDSL(t)
			require.True(t, eval.Execute(tt.design, nil), eval.Context.Error())
			require.Error(t, eval.RunDSL())

			err := eval.Context.Error()
			require.Equal(t, 1, strings.Count(err, tt.want), err)
		})
	}
}
