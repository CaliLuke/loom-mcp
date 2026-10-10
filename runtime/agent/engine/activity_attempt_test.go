package engine

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestActivityAttemptContext(t *testing.T) {
	base := context.Background()
	assert.Zero(t, ActivityAttemptFromContext(base))
	first := WithActivityAttempt(base, 1)
	assert.Equal(t, 1, ActivityAttemptFromContext(first))
	assert.Equal(t, 2, ActivityAttemptFromContext(WithActivityAttempt(first, 2)))
	assert.Equal(t, 1, ActivityAttemptFromContext(first))
	assert.Zero(t, ActivityAttemptFromContext(base))
}
