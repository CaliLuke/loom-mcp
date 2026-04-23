package clientinfra

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	mongodriver "go.mongodb.org/mongo-driver/mongo"
)

func TestValidateMongoOptions(t *testing.T) {
	t.Parallel()

	require.EqualError(t, ValidateMongoOptions(nil, "db"), "mongo client is required")
	require.EqualError(t, ValidateMongoOptions(&mongodriver.Client{}, ""), "database name is required")
}

func TestResolveTimeout(t *testing.T) {
	t.Parallel()

	assert.Equal(t, 5*time.Second, ResolveTimeout(0, 5*time.Second))
	assert.Equal(t, 2*time.Second, ResolveTimeout(2*time.Second, 5*time.Second))
}

func TestResolveCollectionName(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "default", ResolveCollectionName("", "default"))
	assert.Equal(t, "custom", ResolveCollectionName("custom", "default"))
}

func TestValidateCollections(t *testing.T) {
	t.Parallel()

	require.NoError(t, ValidateCollections("collection is required", &struct{}{}))
	require.EqualError(t, ValidateCollections("collection is required", nil), "collection is required")

	var ptr *struct{}
	require.EqualError(t, ValidateCollections("collection is required", ptr), "collection is required")
}

func TestEnsureIndexesUsesDeadlineContext(t *testing.T) {
	t.Parallel()

	var hasDeadline bool
	err := EnsureIndexes(time.Second, func(ctx context.Context) error {
		_, hasDeadline = ctx.Deadline()
		return nil
	})

	require.NoError(t, err)
	assert.True(t, hasDeadline)
}

func TestWithTimeoutNormalizesNilContextWhenRequested(t *testing.T) {
	t.Parallel()

	var nilCtx context.Context
	ctx, cancel := WithTimeout(nilCtx, time.Second, true)
	defer cancel()

	assert.NotNil(t, ctx)
	_, hasDeadline := ctx.Deadline()
	assert.True(t, hasDeadline)
}

func TestWithTimeoutPreservesNilContextBehaviorWhenNormalizationDisabled(t *testing.T) {
	t.Parallel()

	var nilCtx context.Context
	assert.Panics(t, func() {
		_, _ = WithTimeout(nilCtx, time.Second, false)
	})
}
