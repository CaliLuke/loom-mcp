package mcp

import (
	"runtime/debug"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMCPLibraryVersionUsesOnlyLibraryMetadata(t *testing.T) {
	const path = "github.com/CaliLuke/loom-mcp/v2"
	for _, tc := range []struct {
		name string
		info *debug.BuildInfo
		want string
	}{
		{"unavailable", nil, "dev"},
		{"host only", &debug.BuildInfo{Main: debug.Module{Path: "private/host", Version: "v9.9.9"}, Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "secret"}}}, "dev"},
		{"library main", &debug.BuildInfo{Main: debug.Module{Path: path, Version: "v2.1.0"}}, "v2.1.0"},
		{"library dependency", &debug.BuildInfo{Main: debug.Module{Path: "private/host", Version: "v9.9.9"}, Deps: []*debug.Module{{Path: path, Version: "v2.1.1-alpha.1"}}}, "v2.1.1-alpha.1"},
		{"local replacement", &debug.BuildInfo{Deps: []*debug.Module{{Path: path, Version: "v2.1.0", Replace: &debug.Module{Path: "/private/home/checkout"}}}}, "dev"},
		{"versioned replacement", &debug.BuildInfo{Deps: []*debug.Module{{Path: path, Version: "v2.1.0", Replace: &debug.Module{Path: "private/fork", Version: "v2.2.0"}}}}, "v2.2.0"},
		{"development", &debug.BuildInfo{Main: debug.Module{Path: path, Version: "(devel)"}}, "dev"},
		{"empty", &debug.BuildInfo{Main: debug.Module{Path: path}}, "dev"},
		{"invalid token", &debug.BuildInfo{Main: debug.Module{Path: path, Version: "bad/value\r\n"}}, "dev"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, mcpLibraryVersion(tc.info))
		})
	}
}
