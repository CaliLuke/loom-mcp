package agent

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestToolsetExprValidateArtifactsProvider(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		ts := &ToolsetExpr{
			Name: "artifacts",
			Provider: &ProviderExpr{
				Kind:             ProviderArtifacts,
				ArtifactMaxBytes: 65536,
				ArtifactMaxCount: 50,
			},
		}
		require.NoError(t, ts.Validate())
	})

	t.Run("negative max bytes", func(t *testing.T) {
		ts := &ToolsetExpr{
			Name: "artifacts",
			Provider: &ProviderExpr{
				Kind:             ProviderArtifacts,
				ArtifactMaxBytes: -1,
			},
		}
		require.ErrorContains(t, ts.Validate(), "MaxArtifactBytes must be non-negative")
	})

	t.Run("negative max artifacts", func(t *testing.T) {
		ts := &ToolsetExpr{
			Name: "artifacts",
			Provider: &ProviderExpr{
				Kind:             ProviderArtifacts,
				ArtifactMaxCount: -1,
			},
		}
		require.ErrorContains(t, ts.Validate(), "MaxArtifacts must be non-negative")
	})

	t.Run("inline tools rejected", func(t *testing.T) {
		ts := &ToolsetExpr{
			Name: "artifacts",
			Provider: &ProviderExpr{
				Kind: ProviderArtifacts,
			},
			Tools: []*ToolExpr{{Name: "list_artifacts"}},
		}
		require.ErrorContains(t, ts.Validate(), "FromArtifacts toolsets cannot declare inline tools")
	})
}
