//go:build integration

package registry

// These live-Redis tests replay the counterexamples found by the TLA+ model of
// call admission: Pulse may redeliver any unacknowledged request event, so an
// overload-superseded or retention-orphaned request must never corrupt the
// retained call record or return a provider-fatal error.

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	genregistry "github.com/CaliLuke/loom-mcp/v2/registry/gen/registry"
	"github.com/CaliLuke/loom-mcp/v2/runtime/toolregistry"
)

func TestRedeliveredSupersededRequestCannotClaimAfterOverloadRetry(t *testing.T) {
	ctx := context.Background()
	f := newCallFencingFixture(t, "superseded")
	firstEventID := f.publicationEventID(t)
	require.NoError(t, f.svc.ReportToolCallOverload(ctx, f.claimPayload(firstEventID)))
	f.retry(t)
	retryEventID := f.publicationEventID(t)
	require.NotEqual(t, firstEventID, retryEventID)

	stale, err := f.svc.ClaimToolCall(ctx, f.claimPayload(firstEventID))
	require.NoError(t, err)
	assert.Equal(t, string(callClaimClaimed), stale.Disposition)
	current, err := f.svc.ClaimToolCall(ctx, f.claimPayload(retryEventID))
	require.NoError(t, err)
	assert.Equal(t, string(callClaimExecute), current.Disposition)

	replayed, err := f.svc.CallTool(ctx, f.call)
	require.NoError(t, err)
	assert.Equal(t, f.admitted, replayed)
}

func TestOverloadRetryAfterDispatchDoesNotRepublish(t *testing.T) {
	ctx := context.Background()
	f := newCallFencingFixture(t, "dispatched")
	eventID := f.publicationEventID(t)
	require.NoError(t, f.svc.ReportToolCallOverload(ctx, f.claimPayload(eventID)))
	claim, err := f.svc.ClaimToolCall(ctx, f.claimPayload(eventID))
	require.NoError(t, err)
	require.Equal(t, string(callClaimExecute), claim.Disposition)
	queued := f.rdb.XLen(ctx, pulseStreamKeyPrefix+toolregistry.ToolsetStreamID(f.toolset)).Val()

	f.retry(t)
	assert.Equal(t, eventID, f.publicationEventID(t))
	assert.Equal(t, queued, f.rdb.XLen(ctx, pulseStreamKeyPrefix+toolregistry.ToolsetStreamID(f.toolset)).Val())
	replayed, err := f.svc.CallTool(ctx, f.call)
	require.NoError(t, err)
	assert.Equal(t, f.admitted, replayed)
}

func TestRequestFromExpiredAdmissionIsNotProviderFatal(t *testing.T) {
	ctx := context.Background()
	f := newCallFencingFixture(t, "orphaned")
	orphanEventID := f.publicationEventID(t)
	callKey := f.store.callKey(f.admitted.ToolUseID)
	require.EqualValues(t, 1, f.rdb.Del(ctx, callKey).Val())
	readmitted, err := f.svc.CallTool(ctx, f.call)
	require.NoError(t, err)
	require.NotEqual(t, orphanEventID, f.publicationEventID(t))
	resultStreamKey := pulseStreamKeyPrefix + toolregistry.ResultStreamID(readmitted.ToolUseID)

	cases := []struct {
		name      string
		callToken string
	}{
		{name: "same admission token", callToken: f.token},
		{name: "older admission token", callToken: strings.Repeat("a", 64)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			payload := f.claimPayload(orphanEventID)
			payload.CallRegistrationToken = tc.callToken
			results := f.rdb.XLen(ctx, resultStreamKey).Val()
			require.NoError(t, f.svc.ReportToolCallOverload(ctx, payload))
			assert.Equal(t, results, f.rdb.XLen(ctx, resultStreamKey).Val())
			claim, err := f.svc.ClaimToolCall(ctx, payload)
			require.NoError(t, err)
			assert.Equal(t, string(callClaimExpired), claim.Disposition)
		})
	}
	current, err := f.svc.ClaimToolCall(ctx, f.claimPayload(f.publicationEventID(t)))
	require.NoError(t, err)
	assert.Equal(t, string(callClaimExecute), current.Disposition)
}

type callFencingFixture struct {
	svc      *Service
	store    *callAdmissionStore
	rdb      *redis.Client
	toolset  string
	token    string
	provider *genregistry.RegisterPayload
	call     *genregistry.CallToolPayload
	admitted *genregistry.CallToolResult
}

// newCallFencingFixture admits and publishes one call on a fresh registry.
func newCallFencingFixture(t *testing.T, callID string) *callFencingFixture {
	t.Helper()
	rdb := getRedis(t)
	ctx := context.Background()
	name := fmt.Sprintf("call-fencing-%s-%d", callID, time.Now().UnixNano())
	reg, err := New(ctx, Config{Redis: rdb, Name: name})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, reg.Close(context.Background())) })
	svc := reg.Service()
	svc.healthTracker = newMockHealthTracker()
	toolset := "call-fencing-toolset"
	provider := validRegisterPayloadForSchemaAdmission(toolset)
	registered, err := svc.Register(ctx, provider)
	require.NoError(t, err)
	call := transitionCallPayload(toolset, callID)
	admitted, err := svc.CallTool(ctx, call)
	require.NoError(t, err)
	return &callFencingFixture{
		svc:      svc,
		store:    svc.callAdmissions.(*callAdmissionStore),
		rdb:      rdb,
		toolset:  toolset,
		token:    registered.RegistrationToken,
		provider: provider,
		call:     call,
		admitted: admitted,
	}
}

// publicationEventID returns the request event the call record currently owns.
func (f *callFencingFixture) publicationEventID(t *testing.T) string {
	t.Helper()
	return retainedPublicationEventID(t, context.Background(), f.rdb, f.store, f.admitted.ToolUseID)
}

// claimPayload builds the provider claim for one delivered request event.
func (f *callFencingFixture) claimPayload(requestEventID string) *genregistry.ProviderToolCallClaimPayload {
	return &genregistry.ProviderToolCallClaimPayload{
		Toolset:                   f.toolset,
		ProviderID:                f.provider.ProviderID,
		ProviderIncarnationID:     f.provider.ProviderIncarnationID,
		ProviderRegistrationToken: f.token,
		CallRegistrationToken:     f.token,
		ToolUseID:                 f.admitted.ToolUseID,
		RequestEventID:            requestEventID,
	}
}

// retry performs the caller's RetryTool after observing overload control.
func (f *callFencingFixture) retry(t *testing.T) {
	t.Helper()
	retried, err := f.svc.RetryTool(context.Background(), &genregistry.RetryToolPayload{
		Toolset:                   f.call.Toolset,
		Tool:                      f.call.Tool,
		PayloadJSON:               f.call.PayloadJSON,
		Meta:                      f.call.Meta,
		WireProtocolVersion:       toolregistry.WireProtocolVersion,
		ExpectedRegistrationToken: f.token,
	})
	require.NoError(t, err)
	require.Equal(t, f.admitted, retried)
}
