// Package runstoretest provides reusable behavioral tests for run stores.
package runstoretest

import (
	"bytes"
	"slices"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
)

const (
	testRun  = "run"
	otherRun = "other"
)

// RunConformance checks the run store contract. newStore must return an empty,
// independent store for each invocation; it can register cleanup with t.Cleanup.
func RunConformance(t *testing.T, newStore func(t *testing.T) runstore.Store) {
	t.Helper()
	for _, tc := range []struct {
		name string
		run  func(*testing.T, runstore.Store)
	}{
		{"content", testContent},
		{"sequences", testSequences},
		{"concurrent_sequences", testConcurrentSequences},
		{"call_order_and_isolation", testCallOrder},
		{"copies", testCopies},
		{"delete_scope", testDeleteScope},
		{"required_scope", testRequiredScope},
		{"concurrent_completion", testConcurrentCompletion},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.run(t, newStore(t))
		})
	}
	runCallTransitions(t, newStore)
	runInvalidCalls(t, newStore)
}

func testContent(t *testing.T, s runstore.Store) {
	ctx := t.Context()
	for _, tc := range []struct {
		data []byte
		hash runstore.Hash
	}{
		{[]byte("abc"), "sha256:ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"},
		{nil, "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"},
	} {
		hash, err := s.Put(ctx, "a", tc.data)
		require.NoError(t, err)
		assert.Equal(t, tc.hash, hash)
		again, err := s.Put(ctx, "a", bytes.Clone(tc.data))
		require.NoError(t, err)
		assert.Equal(t, hash, again)
		got, err := s.Get(ctx, "a", hash)
		require.NoError(t, err)
		assert.True(t, bytes.Equal(tc.data, got))
		_, err = s.Get(ctx, "b", hash)
		require.ErrorIs(t, err, runstore.ErrNotFound)
		other, err := s.Put(ctx, "b", tc.data)
		require.NoError(t, err)
		assert.Equal(t, hash, other)
	}
	_, err := s.Get(ctx, "a", "sha256:unknown")
	require.ErrorIs(t, err, runstore.ErrNotFound)
}

func testSequences(t *testing.T, s runstore.Store) {
	for _, tc := range []struct {
		scope, run string
		want       int
	}{
		{"a", "r", 1}, {"a", "r", 2}, {"a", "r", 3},
		{"a", otherRun, 1}, {"b", "r", 1},
	} {
		seq, err := s.NextSeq(t.Context(), tc.scope, tc.run)
		require.NoError(t, err)
		assert.Equal(t, tc.want, seq)
	}
}

func testConcurrentSequences(t *testing.T, s runstore.Store) {
	type result struct {
		seq int
		err error
	}
	results := make(chan result, 800)
	start := make(chan struct{})
	var done sync.WaitGroup
	for range 16 {
		done.Go(func() {
			<-start
			for range 50 {
				seq, err := s.NextSeq(t.Context(), "a", testRun)
				results <- result{seq, err}
			}
		})
	}
	close(start)
	done.Wait()
	close(results)
	values := make([]int, 0, 800)
	for r := range results {
		require.NoError(t, r.err)
		values = append(values, r.seq)
	}
	require.Len(t, values, 800)
	slices.Sort(values)
	for i, seq := range values {
		assert.Equal(t, i+1, seq)
	}
}

func testCallOrder(t *testing.T, s runstore.Store) {
	records := []runstore.CallRecord{
		call(runstore.CallStarted, "request", ""),
		call(runstore.CallStarted, "request", ""),
		call(runstore.CallStarted, "request", ""),
	}
	records[0].Key.Seq = 2
	records[1].Key.Attempt = 2
	for _, r := range records {
		require.NoError(t, s.PutCall(t.Context(), "a", r))
	}
	require.NoError(t, s.PutCall(t.Context(), "b", call(runstore.CallStarted, "other scope", "")))
	other := call(runstore.CallStarted, "other run", "")
	other.Key.RunID = otherRun
	require.NoError(t, s.PutCall(t.Context(), "a", other))
	got, err := s.ListCalls(t.Context(), "a", testRun)
	require.NoError(t, err)
	assert.Equal(t, []runstore.CallRecord{records[2], records[1], records[0]}, got)
	got, err = s.ListCalls(t.Context(), "b", testRun)
	require.NoError(t, err)
	assert.Equal(t, []runstore.CallRecord{call(runstore.CallStarted, "other scope", "")}, got)
	got, err = s.ListCalls(t.Context(), "missing", testRun)
	require.NoError(t, err)
	assert.Empty(t, got)
}

func testCopies(t *testing.T, s runstore.Store) {
	data := []byte("content")
	hash, err := s.Put(t.Context(), "a", data)
	require.NoError(t, err)
	data[0] = '!'
	got, err := s.Get(t.Context(), "a", hash)
	require.NoError(t, err)
	require.Equal(t, []byte("content"), got)
	got[0] = '!'
	got, err = s.Get(t.Context(), "a", hash)
	require.NoError(t, err)
	assert.Equal(t, []byte("content"), got)
	r := call(runstore.CallStarted, "request", "")
	require.NoError(t, s.PutCall(t.Context(), "a", r))
	r.Started[0] = '!'
	final := call(runstore.CallCompleted, "request", "response")
	require.NoError(t, s.PutCall(t.Context(), "a", final))
	final.Started[0], final.Completion[0] = '!', '!'
	listed, err := s.ListCalls(t.Context(), "a", testRun)
	require.NoError(t, err)
	require.Equal(t, []runstore.CallRecord{call(runstore.CallCompleted, "request", "response")}, listed)
	listed[0].Started[0], listed[0].Completion[0] = '!', '!'
	listed[0].Key.RunID = "mutated"
	listed, err = s.ListCalls(t.Context(), "a", testRun)
	require.NoError(t, err)
	assert.Equal(t, []runstore.CallRecord{call(runstore.CallCompleted, "request", "response")}, listed)
}

func testDeleteScope(t *testing.T, s runstore.Store) {
	for _, scope := range []string{"a", "b"} {
		_, err := s.Put(t.Context(), scope, []byte("abc"))
		require.NoError(t, err)
		for _, run := range []string{testRun, otherRun} {
			_, err = s.NextSeq(t.Context(), scope, run)
			require.NoError(t, err)
			r := call(runstore.CallStarted, "request", "")
			r.Key.RunID = run
			require.NoError(t, s.PutCall(t.Context(), scope, r))
		}
	}
	require.NoError(t, s.DeleteScope(t.Context(), "a"))
	require.NoError(t, s.DeleteScope(t.Context(), "a"))
	hash := runstore.Hash("sha256:ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad")
	_, err := s.Get(t.Context(), "a", hash)
	require.ErrorIs(t, err, runstore.ErrNotFound)
	got, err := s.Get(t.Context(), "b", hash)
	require.NoError(t, err)
	assert.Equal(t, []byte("abc"), got)
	for _, run := range []string{testRun, otherRun} {
		calls, err := s.ListCalls(t.Context(), "a", run)
		require.NoError(t, err)
		assert.Empty(t, calls)
		seq, err := s.NextSeq(t.Context(), "a", run)
		require.NoError(t, err)
		assert.Equal(t, 1, seq)
		calls, err = s.ListCalls(t.Context(), "b", run)
		require.NoError(t, err)
		assert.Len(t, calls, 1)
		seq, err = s.NextSeq(t.Context(), "b", run)
		require.NoError(t, err)
		assert.Equal(t, 2, seq)
	}
}

func testRequiredScope(t *testing.T, s runstore.Store) {
	_, err := s.Put(t.Context(), "", []byte("content"))
	require.Error(t, err)
	_, err = s.Get(t.Context(), "", "hash")
	require.Error(t, err)
	_, err = s.NextSeq(t.Context(), "", testRun)
	require.Error(t, err)
	require.Error(t, s.PutCall(t.Context(), "", call(runstore.CallStarted, "request", "")))
	_, err = s.ListCalls(t.Context(), "", testRun)
	require.Error(t, err)
	require.Error(t, s.DeleteScope(t.Context(), ""))
}

func testConcurrentCompletion(t *testing.T, s runstore.Store) {
	require.NoError(t, s.PutCall(t.Context(), "a", call(runstore.CallStarted, "request", "")))
	start := make(chan struct{})
	results := make(chan error, 2)
	var done sync.WaitGroup
	for _, state := range []runstore.CallState{runstore.CallCompleted, runstore.CallFailed} {
		done.Go(func() {
			<-start
			results <- s.PutCall(t.Context(), "a", call(state, "request", "response"))
		})
	}
	close(start)
	done.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		} else {
			require.ErrorIs(t, err, runstore.ErrCallConflict)
		}
	}
	assert.Equal(t, 1, successes)
	got, err := s.ListCalls(t.Context(), "a", testRun)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Contains(t, []runstore.CallState{runstore.CallCompleted, runstore.CallFailed}, got[0].State)
}

func call(state runstore.CallState, started, completion string) runstore.CallRecord {
	r := runstore.CallRecord{Key: runstore.CallKey{RunID: testRun, Seq: 1, Attempt: 1}, State: state, Started: []byte(started)}
	if completion != "" {
		r.Completion = []byte(completion)
	}
	return r
}
