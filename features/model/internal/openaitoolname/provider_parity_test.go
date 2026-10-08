package openaitoolname_test

import (
	"strings"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/features/model/bedrock"
	"github.com/CaliLuke/loom-mcp/v2/features/model/internal/openaitoolname"
	"github.com/stretchr/testify/assert"
)

func TestProviderToolNameNormalization(t *testing.T) {
	for _, tt := range []struct{ name, input, want string }{
		{"empty", "", ""},
		{"safe", "ABC_xyz-123", "ABC_xyz-123"},
		{"dotted", "toolset.v2.get", "toolset_v2_get"},
		{"unicode", "a.東京🙂", "a____"},
		{"invalid", "tool:name/with spaces", "tool_name_with_spaces"},
		{"boundary", strings.Repeat("a", 64), strings.Repeat("a", 64)},
		{"long", "toolset." + strings.Repeat("a", 80), "toolset_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa_bc401114"},
		{"long distinct", "toolset." + strings.Repeat("a", 79) + "b", "toolset_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa_6358c8de"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, bedrock.SanitizeToolName(tt.input))
			assert.Equal(t, tt.want, openaitoolname.Sanitize(tt.input))
		})
	}
}
