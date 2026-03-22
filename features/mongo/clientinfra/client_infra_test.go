package clientinfra

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	mongodriver "go.mongodb.org/mongo-driver/mongo"
)

func TestValidateMongoOptions(t *testing.T) {
	t.Parallel()

	assert.EqualError(t, ValidateMongoOptions(nil, "db"), "mongo client is required")
	assert.EqualError(t, ValidateMongoOptions(&mongodriver.Client{}, ""), "database name is required")
}

func TestResolveTimeout(t *testing.T) {
	t.Parallel()

	assert.Equal(t, 5*time.Second, ResolveTimeout(0, 5*time.Second))
	assert.Equal(t, 2*time.Second, ResolveTimeout(2*time.Second, 5*time.Second))
}

func TestEnsureIndexesUsesDeadlineContext(t *testing.T) {
	t.Parallel()

	var hasDeadline bool
	err := EnsureIndexes(time.Second, func(ctx context.Context) error {
		_, hasDeadline = ctx.Deadline()
		return nil
	})

	assert.NoError(t, err)
	assert.True(t, hasDeadline)
}

func TestWithTimeoutNormalizesNilContextWhenRequested(t *testing.T) {
	t.Parallel()

	ctx, cancel := WithTimeout(nil, time.Second, true)
	defer cancel()

	assert.NotNil(t, ctx)
	_, hasDeadline := ctx.Deadline()
	assert.True(t, hasDeadline)
}

func TestWithTimeoutPreservesNilContextBehaviorWhenNormalizationDisabled(t *testing.T) {
	t.Parallel()

	assert.Panics(t, func() {
		_, _ = WithTimeout(nil, time.Second, false)
	})
}
