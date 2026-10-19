package candidate_test

import (
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/eval/candidate"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCopyLegacyDigests(t *testing.T) {
	s := openStore(t)
	assert.Equal(t, "5c94a77397e8ba8858cf1ca1d788ca8147423fe9e802f99d0e117534ca1ff6dd", s.Discover().BaselineDigest)
	f := freezeBaseline(t, s)
	assert.Equal(t, "caaed80d7138ebc9ffd39794d8484ac90a4fc5d8614d9da87ca2bfa84f7e494a", f.Digest())
	d, err := s.Create(t.Context())
	require.NoError(t, err)
	d, err = s.Edit(t.Context(), d.ID, d.Revision, []candidate.Edit{{Operation: candidate.Delete, Name: "skill.md"}})
	require.NoError(t, err)
	empty, err := s.Freeze(t.Context(), d.ID, d.Revision)
	require.NoError(t, err)
	assert.Equal(t, "42b8f8329efb1b6650c6d472ab21092bd818c9e25c6215b4c16ec2e7451cb400", empty.Digest())
}
