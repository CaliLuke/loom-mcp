package runtime

import (
	"container/list"
	"context"
	"encoding/json/v2"
	"fmt"
	"slices"
	"sync"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/checkpoint"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
)

type runViewCache struct {
	mu                sync.Mutex
	recency           *list.Element // Protected by Runtime.viewMu, independent of active loads.
	segments          map[runstore.Hash]index.IndexSegment
	head              runstore.Hash
	chain             runstore.Hash
	count             index.MessageID
	renderer          index.Renderer
	outputs           []*planner.ToolOutput
	typed             []planner.TypedInputOutput
	entries           []index.MessageEntry
	rendered          int
	tailKey           tailCacheKey
	tailMessages      []*model.Message
	systemMessages    []*model.Message
	checkpointKey     checkpointCacheKey
	checkpointMessage *model.Message
	checkpointInput   checkpoint.Input
	checkpointHash    runstore.Hash
	pinBudget         index.RenderBudget
	selectedPins      []index.MessageID
}

type loadedView struct {
	View     *api.ViewUpdate
	Head     runstore.Hash
	Messages []*model.Message
	Outputs  []*planner.ToolOutput
	Typed    []planner.TypedInputOutput
}

// loadView commits pending entries before exposing the head. The cache extends
// only an adopted lineage; retries reuse identical segment bytes and hashes.
func (r *Runtime) loadView(ctx context.Context, root, runID string, view index.ViewRef) (*loadedView, error) {
	if root == "" || r.RunStore == nil {
		return nil, fmt.Errorf("view loader requires root scope and run store")
	}
	cache := r.viewCache(root + "\x00" + runID)
	// Eviction removes only the runtime's reference. Active loads keep their
	// cache pointer and serialize its use even if a later load rebuilds cold.
	cache.mu.Lock()
	defer cache.mu.Unlock()
	source := func(ctx context.Context, hash runstore.Hash) ([]byte, error) {
		return r.RunStore.Get(ctx, root, hash)
	}
	head, err := cache.commitPending(ctx, r.RunStore, root, source, view)
	if err != nil {
		return nil, err
	}
	if err := cache.extend(ctx, source, head); err != nil {
		cache.reset()
		return nil, err
	}
	messages, update, err := cache.renderView(ctx, r.RunStore, root, source, view)
	if err != nil {
		return nil, err
	}
	// Reference projections are immutable, shared worker-cache views. Full
	// slice expressions prevent append from overwriting a later cached entry.
	// Deriving these slices does no work proportional to earlier steps.
	return &loadedView{View: update, Head: head, Messages: messages, Outputs: cache.outputs[:len(cache.outputs):len(cache.outputs)], Typed: cache.typed[:len(cache.typed):len(cache.typed)]}, nil
}

// viewCache acquires a run's cache and updates bounded LRU membership under viewMu.
// Eviction drops only map/list references; active loads retain their cache pointer.
func (r *Runtime) viewCache(key string) *runViewCache {
	r.viewMu.Lock()
	defer r.viewMu.Unlock()
	if r.viewCaches == nil {
		r.viewCaches = make(map[string]*runViewCache)
	}
	cache := r.viewCaches[key]
	if cache == nil {
		cache = &runViewCache{segments: make(map[runstore.Hash]index.IndexSegment)}
		cache.recency = r.viewLRU.PushFront(key)
		r.viewCaches[key] = cache
	} else {
		r.viewLRU.MoveToFront(cache.recency)
	}
	if len(r.viewCaches) > r.viewCacheRuns {
		oldest := r.viewLRU.Back()
		delete(r.viewCaches, oldest.Value.(string))
		r.viewLRU.Remove(oldest)
	}
	return cache
}

func (c *runViewCache) segment(ctx context.Context, source index.Source, head runstore.Hash) (index.IndexSegment, error) {
	if seg, ok := c.segments[head]; ok {
		return seg, nil
	}
	data, err := source(ctx, head)
	if err != nil {
		return index.IndexSegment{}, err
	}
	var seg index.IndexSegment
	if err := json.Unmarshal(data, &seg, json.RejectUnknownMembers(true)); err != nil {
		return seg, err
	}
	_, hash, err := index.EncodeSegment(seg)
	if err != nil {
		return seg, err
	}
	if hash != head {
		return seg, fmt.Errorf("index segment hash mismatch")
	}
	c.segments[head] = seg
	return seg, nil
}

func (c *runViewCache) extend(ctx context.Context, source index.Source, head runstore.Hash) error {
	var segments []index.IndexSegment
	cursor := head
	for cursor != "" && cursor != c.head {
		seg, err := c.segment(ctx, source, cursor)
		if err != nil {
			return err
		}
		segments = append(segments, seg)
		cursor = ""
		if seg.Prev != nil {
			cursor = *seg.Prev
		}
	}
	if cursor != c.head {
		c.reset()
	}
	var entries []index.MessageEntry
	for _, seg := range slices.Backward(segments) {
		entries = append(entries, seg.Entries...)
	}
	if err := c.appendEntries(ctx, source, entries); err != nil {
		return err
	}

	c.head = head
	return nil
}

func (c *runViewCache) appendEntries(ctx context.Context, source index.Source, entries []index.MessageEntry) error {
	for _, entry := range entries {
		if entry.ID != c.count+1 {
			return fmt.Errorf("index gap at %d", entry.ID)
		}
		chain, err := index.ComputeChain(c.chain, entry)
		if err != nil {
			return err
		}
		if chain != entry.Chain {
			return fmt.Errorf("index chain mismatch at %d", entry.ID)
		}
		if entry.Output != "" {
			data, err := source(ctx, entry.Output)
			if err != nil {
				return err
			}
			var output planner.ToolOutput
			if err := json.Unmarshal(data, &output); err != nil {
				return err
			}
			if len(output.Result) != 0 || len(output.ServerData) != 0 || len(output.Payload) != 0 {
				return fmt.Errorf("indexed tool output contains inline content")
			}
			if err := validateOutputContent(ctx, source, &output, entry); err != nil {
				return err
			}
			c.outputs = append(c.outputs, &output)
		}
		if entry.Kind == index.TypedInput {
			if _, err := source(ctx, entry.Hash); err != nil {
				return err
			}
			c.typed = append(c.typed, planner.TypedInputOutput{ID: entry.InputID, PayloadRef: entry.Hash})
		}
		c.count, c.chain = entry.ID, chain
	}
	c.entries = append(c.entries, entries...)
	return nil
}

func (c *runViewCache) reset() {
	c.head, c.chain, c.count = "", "", 0
	c.renderer = index.Renderer{}
	c.outputs, c.typed = nil, nil
	c.entries = nil
	c.rendered = 0
	c.tailMessages, c.systemMessages = nil, nil
	c.tailKey = tailCacheKey{}
	c.checkpointKey = checkpointCacheKey{}
	c.checkpointMessage = nil
	c.checkpointHash = ""
	c.checkpointInput = checkpoint.Input{}
	c.pinBudget = index.RenderBudget{}
	c.selectedPins = nil
}

// commitPending validates lineage before publishing any newly stored head.
func (c *runViewCache) commitPending(ctx context.Context, store runstore.Store, root string, source index.Source, view index.ViewRef) (runstore.Hash, error) {
	head := view.IndexHead
	if len(view.Pending) == 0 {
		return head, nil
	}

	var previous runstore.Hash
	var previousID index.MessageID
	if head != "" {
		seg, err := c.segment(ctx, source, head)
		if err != nil {
			return "", err
		}
		previous = seg.Entries[len(seg.Entries)-1].Chain
		previousID = seg.Entries[len(seg.Entries)-1].ID
	}
	chain := previous
	for _, entry := range view.Pending {
		if entry.ID != previousID+1 {
			return "", fmt.Errorf("pending index gap at %d", entry.ID)
		}
		expected, err := index.ComputeChain(chain, entry)
		if err != nil {
			return "", err
		}
		if expected != entry.Chain {
			return "", fmt.Errorf("pending index chain mismatch at %d", entry.ID)
		}
		chain, previousID = expected, entry.ID
	}
	var err error
	head, _, err = storeIndexSegments(ctx, store, root, head, previous, view.Pending)
	if err != nil {
		return "", err
	}
	return head, nil
}

// validateOutputContent validates inline results, server values and payload
// sources before invoking the planner. Omitted result bodies are read only if
// the planner explicitly requests them through ContentLoader.
func validateOutputContent(ctx context.Context, source index.Source, output *planner.ToolOutput, entry index.MessageEntry) error {
	for i, hash := range []runstore.Hash{output.Refs.Result, output.Refs.ServerData, output.Refs.Payload, output.Call} {
		if hash == "" {
			continue
		}
		if i == 0 && entry.OmitsResultContent() && hash == entry.Hash {
			continue
		}
		if _, err := source(ctx, hash); err != nil {
			return err
		}
	}
	return nil
}
