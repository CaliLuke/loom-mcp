package clientinfra_test

import (
	"context"
	"testing"
	"time"

	runstoremongo "github.com/CaliLuke/loom-mcp/v2/features/runstore/mongo"
	runstoreclient "github.com/CaliLuke/loom-mcp/v2/features/runstore/mongo/clients/mongo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A new worker wrapper must observe earlier durable reservations, including
// reservations that never acquired a Started call record.
func TestMongoDriverV2RunstoreReservationsRestart(t *testing.T) {
	driver, database := newMongoIntegrationClient(t)
	options := runstoreclient.Options{Client: driver, Database: database, Timeout: 30 * time.Second, ContentCollection: "model_budget_content", CallCollection: "model_budget_calls", CounterCollection: "model_budget_sequences"}
	firstClient, err := runstoreclient.New(options)
	require.NoError(t, err)
	first, err := runstoremongo.NewStore(firstClient)
	require.NoError(t, err)
	firstSequence, err := first.NextSeq(t.Context(), "root", "parent")
	require.NoError(t, err)
	require.Equal(t, 1, firstSequence)
	replacementClient, err := runstoreclient.New(options)
	require.NoError(t, err)
	replacement, err := runstoremongo.NewStore(replacementClient)
	require.NoError(t, err)
	next, err := replacement.NextSeq(t.Context(), "root", "parent")
	require.NoError(t, err)
	assert.Equal(t, 2, next, "a one-call cap rejects this fresh invocation after worker replacement")
	child, err := replacement.NextSeq(t.Context(), "root", "child")
	require.NoError(t, err)
	assert.Equal(t, 1, child, "child counter is independent within the shared root scope")
	calls, err := replacement.ListCalls(context.Background(), "root", "parent")
	require.NoError(t, err)
	assert.Empty(t, calls, "a conservative reservation is not a fabricated provider call")
}
