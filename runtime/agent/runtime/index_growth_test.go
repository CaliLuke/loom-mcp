package runtime

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	goruntime "runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/run"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	storeinmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type measuredViewStore struct {
	runstore.Store
	reads, bytes atomic.Int64
}

func (s *measuredViewStore) Get(ctx context.Context, root string, hash runstore.Hash) ([]byte, error) {
	data, err := s.Store.Get(ctx, root, hash)
	s.reads.Add(1)
	s.bytes.Add(int64(len(data)))
	return data, err
}

// TestIndexLoaderGrowth measures the last step of a run with one ordinary
// 100 KB result and one 10 KB typed answer per step (three index entries).
// Warm means the preceding head is already loaded; cold has no decoded segments.
// Heap measurements exclude the prebuilt store and workflow index. Retain the
// planner view and release the loader cache to measure planner-only heap as well
// as their combined retained heap. Allocations measure the final load and message
// snapshot. The in-memory engine has no durable history; input bytes are reported.
func TestIndexLoaderGrowth(t *testing.T) {
	childCase := os.Getenv("LOOM_INDEX_GROWTH_CHILD")
	if childCase != "" {
		require.Contains(t, []string{
			"TestIndexLoaderGrowth/1000/cold", "TestIndexLoaderGrowth/1000/warm",
			"TestIndexLoaderGrowth/5000/cold", "TestIndexLoaderGrowth/5000/warm",
		}, childCase, "invalid isolated growth case")
	}
	for _, steps := range []int{1000, 5000} {
		t.Run(fmt.Sprint(steps), func(t *testing.T) {
			if testing.Short() && steps == 5000 {
				t.Skip("full growth measurements: make test-payload-growth")
			}
			for _, warm := range []bool{false, true} {
				mode := "cold"
				if warm {
					mode = "warm"
				}
				t.Run(mode, func(t *testing.T) {
					if childCase == "" {
						runIndexGrowthChild(t)
						return
					}
					require.Equal(t, childCase, t.Name(), "child must run only its selected measurement")
					t.Logf("isolated growth pid=%d case=%s", os.Getpid(), t.Name())
					measureIndexLoaderGrowth(t, steps, warm, mode)
					if !t.Failed() {
						t.Logf("isolated growth completed: %s", t.Name())
					}
				})
			}
		})
	}
}

// runIndexGrowthChild isolates process-global heap counters from preceding tests
// and cases while retaining the current binary's race and build instrumentation.
func runIndexGrowthChild(t *testing.T) {
	t.Helper()
	executable, err := os.Executable()
	require.NoError(t, err)
	parts := strings.Split(t.Name(), "/")
	for i, part := range parts {
		parts[i] = "^" + regexp.QuoteMeta(part) + "$"
	}
	timeout := 5 * time.Minute
	if deadline, ok := t.Deadline(); ok {
		timeout = min(timeout, time.Until(deadline))
	}
	require.Positive(t, timeout, "parent deadline must permit the isolated measurement")
	ctx, cancel := context.WithTimeout(t.Context(), timeout)
	defer cancel()
	// #nosec G204 -- Re-executes this test binary with fixed flags and an anchored test name.
	cmd := exec.CommandContext(ctx, executable,
		"-test.run="+strings.Join(parts, "/"), "-test.count=1", "-test.v=true",
		fmt.Sprintf("-test.short=%t", testing.Short()), "-test.timeout="+timeout.String())
	cmd.Env = append(os.Environ(), "LOOM_INDEX_GROWTH_CHILD="+t.Name())
	output, err := cmd.CombinedOutput()
	t.Logf("%s", output)
	require.NoError(t, err, "isolated growth %s failed (context: %v):\n%s", t.Name(), ctx.Err(), output)
	require.Contains(t, string(output), "isolated growth completed: "+t.Name(), "child exited without completing its selected measurement")
}

func measureIndexLoaderGrowth(t *testing.T, steps int, warm bool, mode string) {
	t.Helper()
	store := &measuredViewStore{Store: storeinmem.New()}
	base := buildGrowthIndex(t, store, steps)
	require.Len(t, base.Entries, steps*3)
	stepCount := uint64(len(base.Entries)) / 3
	input := api.PlanActivityInput{AgentID: "growth.agent", RunID: "run", RunContext: base.RunContext, View: base.view()}
	wire, err := json.Marshal(input)
	require.NoError(t, err)
	indexJSON, err := json.Marshal(base.Entries)
	require.NoError(t, err)
	// Resident index estimate: backing arrays plus referenced strings/structs.
	indexMemory := growthIndexMemory(base)
	inputBytes, indexBytes := len(wire), len(indexJSON)
	rt := New(WithRunStore(store))
	goruntime.GC()
	goruntime.GC() // Drain sync.Pool victims before sampling retained heap.
	var before goruntime.MemStats
	goruntime.ReadMemStats(&before)
	if warm {
		_, err := rt.loadTestView(t.Context(), "root", "run", index.ViewRef{IndexHead: base.IndexHead})
		require.NoError(t, err)
	}
	store.reads.Store(0)
	store.bytes.Store(0)
	var start goruntime.MemStats
	goruntime.ReadMemStats(&start)
	view, err := rt.loadTestView(t.Context(), "root", "run", input.View)
	require.NoError(t, err)
	var allocated goruntime.MemStats
	goruntime.ReadMemStats(&allocated)
	require.Len(t, view.Outputs, steps)
	require.Len(t, view.Typed, steps)
	require.Len(t, view.Messages, 2*steps)
	goruntime.GC()
	goruntime.GC() // Drain sync.Pool victims before sampling retained heap.
	var after goruntime.MemStats
	goruntime.ReadMemStats(&after)
	require.Greater(t, after.HeapAlloc, before.HeapAlloc)
	retained := after.HeapAlloc - before.HeapAlloc
	rt.viewMu.Lock()
	rt.viewCaches = nil
	rt.viewLRU.Init()
	rt.viewMu.Unlock()
	goruntime.GC()
	goruntime.GC() // Drain sync.Pool victims before sampling retained heap.
	var plannerOnly goruntime.MemStats
	goruntime.ReadMemStats(&plannerOnly)
	require.Greater(t, plannerOnly.HeapAlloc, before.HeapAlloc)
	plannerHeap := plannerOnly.HeapAlloc - before.HeapAlloc
	alloc := allocated.TotalAlloc - start.TotalAlloc
	reads, loaded := store.reads.Load(), store.bytes.Load()
	t.Logf("growth steps=%d cache=%s reads=%d loaded=%d planner_heap=%d planner_and_cache_heap=%d final_load_alloc=%d workflow_index_heap=%d workflow_index_json=%d input=%d history=N/A(inmem)", steps, mode, reads, loaded, plannerHeap, retained, alloc, indexMemory, indexBytes, inputBytes)
	// Measured baseline: 5 reads/step, ~12 KB/step cold; warm 5 reads,
	// ~12 KB. Retained heap ~3.8 KB/step, cold allocation ~50 KB/step under race,
	// warm snapshot ~18 KB/step under race. Ceilings include allocator headroom.
	if warm {
		assert.LessOrEqual(t, reads, int64(6))
		assert.LessOrEqual(t, loaded, int64(16*1024))
	} else {
		assert.LessOrEqual(t, reads, int64(6*steps))
		assert.LessOrEqual(t, loaded, int64(16*1024*steps))
	}
	assert.LessOrEqual(t, plannerHeap, 4000*stepCount)
	assert.Positive(t, retained)
	assert.LessOrEqual(t, retained, 7000*stepCount)
	if warm {
		assert.LessOrEqual(t, alloc, 30_000*stepCount+512_000)
	} else {
		assert.LessOrEqual(t, alloc, 75_000*stepCount)
	}
	assert.LessOrEqual(t, indexMemory, 2000*stepCount)
	assert.LessOrEqual(t, inputBytes, 4096+400*3+100)
	goruntime.KeepAlive(rt)
	goruntime.KeepAlive(view)
	goruntime.KeepAlive(base)
	goruntime.KeepAlive(store)
}

func buildGrowthIndex(t *testing.T, store runstore.Store, steps int) *workflowPlanInput {
	t.Helper()
	base := &workflowPlanInput{AgentID: "growth.agent", RunContext: run.Context{RunID: "run", RootRunID: "root"}}
	put := func(value any) runstore.Hash {
		data, err := json.Marshal(value)
		require.NoError(t, err)
		hash, err := store.Put(t.Context(), "root", data)
		require.NoError(t, err)
		return hash
	}
	result := put(strings.Repeat("x", 100*1024))
	typed := put(strings.Repeat("a", 10*1024-2))
	for i := range steps {
		id := fmt.Sprintf("call-%d", i)
		call := put(planner.ToolRequest{Name: "test.result", ToolCallID: id, Payload: rawjson.Message(`{}`)})
		output := put(planner.ToolOutput{Name: "test.result", ToolCallID: id, Call: call, Refs: tools.ResultRefs{Result: result}, Preview: "large result"})
		first := len(base.Entries)
		require.NoError(t, base.appendEntry(index.MessageEntry{Kind: index.Assistant, Calls: []runstore.Hash{call}}))
		require.NoError(t, base.appendEntry(index.MessageEntry{Kind: index.ToolResult, Hash: result, Output: output, ToolUseIDs: []string{id}, Batch: index.MessageID(first + 2), Bytes: 100*1024 + 2, Result: &index.ResultProjection{Preview: index.TextParameter{Text: "large result"}}}))
		require.NoError(t, base.appendEntry(index.MessageEntry{Kind: index.TypedInput, Hash: typed, InputID: fmt.Sprintf("answer-%d", i)}))
		if i == steps-1 {
			break
		}
		var prev runstore.Hash
		if first > 0 {
			prev = base.Entries[first-1].Chain
		}
		head, _, err := storeIndexSegments(t.Context(), store, "root", base.IndexHead, prev, base.Entries[first:])
		require.NoError(t, err)
		base.IndexHead = head
		base.Committed = len(base.Entries)
	}
	return base
}

// growthIndexMemory estimates resident workflow index storage for this shape.
// Content in the store is excluded. It conservatively counts shared strings
// once per reference; the JSON-size measurement is exact.
func growthIndexMemory(base *workflowPlanInput) uint64 {
	size := uint64(unsafe.Sizeof(*base)) + uint64(cap(base.Entries))*uint64(unsafe.Sizeof(index.MessageEntry{}))
	for _, e := range base.Entries {
		size += uint64(len(e.Chain) + len(e.Hash) + len(e.Output) + len(e.InputID))
		size += uint64(cap(e.Calls)) * uint64(unsafe.Sizeof(runstore.Hash("")))
		for _, h := range e.Calls {
			size += uint64(len(h))
		}
		size += uint64(cap(e.ToolUseIDs)) * uint64(unsafe.Sizeof(""))
		for _, id := range e.ToolUseIDs {
			size += uint64(len(id))
		}
		if e.Result != nil {
			size += uint64(unsafe.Sizeof(*e.Result) + uintptr(len(e.Result.Preview.Text)))
		}
	}
	return size
}

// Caller-configured fields can exceed P2's default fixed allowance. Keep the
// accepted K7 measurements as ceilings so growth in that exception is visible.
func TestPlanActivityInputFixedFields(t *testing.T) {
	for _, tc := range []struct {
		field    string
		maxBytes int
	}{
		{"baseline", 556},
		{"ToolArgs_10KB", 10792},
		{"Labels_10KB", 10809},
		{"AllowedTools_256", 8235},
		{"Recovery_ToolCatalog_256", 8533},
	} {
		t.Run(tc.field, func(t *testing.T) {
			in := api.PlanActivityInput{AgentID: "test.agent", RunID: "root", RunContext: run.Context{RunID: "root", RootRunID: "root"}}
			catalog := make([]tools.Ident, 256)
			for i := range catalog {
				catalog[i] = tools.Ident(fmt.Sprintf("service.tools.operation_%03d", i))
			}
			switch tc.field {
			case "ToolArgs_10KB":
				in.RunContext.ToolArgs = rawjson.Message(`"` + strings.Repeat("x", 10*1024-2) + `"`)
			case "Labels_10KB":
				in.RunContext.Labels = map[string]string{"metadata": strings.Repeat("x", 10*1024)}
			case "AllowedTools_256":
				in.AllowedTools = catalog
			case "Recovery_ToolCatalog_256":
				in.Recovery = &api.ModelRecovery{ToolCatalog: catalog}
			}
			data, err := json.Marshal(in)
			require.NoError(t, err)
			t.Logf("fixed field=%s bytes=%d exceeds_4KB=%t", tc.field, len(data), len(data) > 4096)
			assert.LessOrEqual(t, len(data), tc.maxBytes)
		})
	}
}
