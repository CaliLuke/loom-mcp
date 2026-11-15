package release

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestChooseAlpha(t *testing.T) {
	for _, tc := range []struct {
		name string
		tags []tag
		sha  string
		want string
		skip bool
	}{
		{name: "first", sha: "b", want: "v2.1.0-alpha.1"},
		{name: "new commit", tags: []tag{{"v2.1.0-alpha.5", "a"}}, sha: "b", want: "v2.1.0-alpha.6"},
		{name: "retry same commit", tags: []tag{{"v2.1.0-alpha.5", "a"}}, sha: "a", want: "v2.1.0-alpha.5"},
		{name: "stable already published", tags: []tag{{"v2.1.0", "a"}}, sha: "a", skip: true},
		{name: "same source on another train", tags: []tag{{"v1.9.0-alpha.9", "a"}}, sha: "a", skip: true},
		{name: "closed train", tags: []tag{{"v2.1.0", "a"}}, sha: "b", skip: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := chooseAlpha("v2.1.0", tc.sha, tc.tags)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
			require.Equal(t, tc.skip, got == "")
		})
	}
}

func TestPromotionVersion(t *testing.T) {
	for _, tc := range []struct {
		alpha, stable string
		good          bool
	}{
		{"v2.1.0-alpha.6", "v2.1.0", true},
		{"v2.1.0-alpha.6", "v2.2.0", false},
		{"v2.1.0", "v2.1.0", false},
		{"v2.1.0-beta.1", "v2.1.0", false},
		{"v1.10.0-alpha.6", "v1.10.0", false},
	} {
		t.Run(tc.alpha+tc.stable, func(t *testing.T) {
			err := validatePromotion(tc.alpha, tc.stable)
			require.Equal(t, tc.good, err == nil)
		})
	}
}

func TestReleaseTrainRequiresCanonicalStableV2(t *testing.T) {
	for _, version := range []string{"v2.1.0", "v2.9.1"} {
		require.NoError(t, validateTrainVersion(version))
	}
	for _, version := range []string{"v1.10.0", "v02.1.0", "v2.1.0-alpha.1", "2.1.0"} {
		require.Error(t, validateTrainVersion(version))
	}
	_, err := chooseAlpha("v1.10.0", "source", nil)
	require.Error(t, err)
}
