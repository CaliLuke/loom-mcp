package runtime

import (
	"context"
	"fmt"
	"slices"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextmeasure"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/checkpoint"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
)

type tailCacheKey struct {
	head                           runstore.Hash
	from, cleared, preview, forced index.MessageID
	previewTokens                  int
}
type checkpointCacheKey struct {
	hash   runstore.Hash
	budget index.RenderBudget
	pins   string
}

// renderView isolates compacted projections from the full append-only index.
// Cache misses are deterministic reconstructions; returned messages are owned.
func (c *runViewCache) renderView(ctx context.Context, store runstore.Store, scope string, source index.Source, view index.ViewRef, purpose viewLoadPurpose) ([]*model.Message, *api.ViewUpdate, error) {
	compacted := view.TailFrom > 1 || view.ClearedThrough > 0 || view.Checkpoint != nil || view.PreviewThrough > 0 || view.ForceClearThrough > 0
	if !compacted {
		if err := c.renderer.Append(ctx, c.entries[c.rendered:], source); err != nil {
			c.renderer = index.Renderer{}
			c.rendered = 0
			return nil, nil, err
		}
		c.rendered = len(c.entries)
		messages, err := c.renderer.Messages()
		return messages, nil, err
	}
	from := max(index.MessageID(1), view.TailFrom)
	if view.Checkpoint != nil && from <= 1 {
		return nil, nil, fmt.Errorf("checkpoint requires a tail cut point")
	}
	if from > 1 {
		if err := validTail(c.entries, from); err != nil {
			return nil, nil, err
		}
	}
	var update *api.ViewUpdate
	if from > 1 {
		var err error
		update, err = c.renderCheckpoint(ctx, store, scope, source, view, from)
		if err != nil {
			return nil, nil, err
		}
	}

	if err := c.renderTail(ctx, source, view, from); err != nil {
		return nil, nil, err
	}

	messages, err := c.assembleView(from, purpose)
	if err != nil {
		return nil, nil, err
	}

	return cloneMessages(messages), update, nil
}

// assembleView validates checkpoint projections against both provider grammars.
// Child admission permits only the trailing unanswered assistant tool turn.
// Clear-only and full projections retain the existing resume-time validator.
func (c *runViewCache) assembleView(from index.MessageID, purpose viewLoadPurpose) ([]*model.Message, error) {
	var message *model.Message
	if from > 1 {
		message = c.checkpointMessage
	}
	messages := transcript.AssembleView(c.systemMessages, message, c.tailMessages)
	if message != nil {
		validate := transcript.ValidateView
		if purpose == childAdmissionView {
			validate = transcript.ValidateChildAdmissionView
		}
		if err := validate(messages, transcript.GrammarAnthropic{}); err != nil {
			return nil, fmt.Errorf("compacted Anthropic grammar: %w", err)
		}
		if err := validate(messages, transcript.GrammarBedrock{}); err != nil {
			return nil, fmt.Errorf("compacted Bedrock grammar: %w", err)
		}
	}
	return messages, nil
}

// renderCheckpoint caches record inputs independently of their bounded rendering.
// Pins are the canonical SelectPins(record users, budget) projection. Cached
// recomputation equals the pins returned in ViewUpdate and recorded in CallView;
// RunRecord renders those stored pins. Incoming pins do not override selection.
func (c *runViewCache) renderCheckpoint(ctx context.Context, store runstore.Store, scope string, source index.Source, view index.ViewRef, from index.MessageID) (*api.ViewUpdate, error) {
	hash := runstore.Hash("")
	if view.Checkpoint != nil {
		hash = *view.Checkpoint
	}
	if hash == "" || hash != c.checkpointHash || c.checkpointInput.Through != from-1 {
		var err error
		hash, err = receiptRecord(ctx, store, scope, source, c.entries, view)
		if err != nil {
			return nil, err
		}
	}
	changed := hash != c.checkpointHash
	budgetChanged := c.checkpointHash != "" && c.pinBudget != view.Budget
	if changed {
		input, err := checkpoint.FromRecord(ctx, source, hash, c.entries, 0)
		if err != nil {
			return nil, err
		}
		c.checkpointInput = input
		c.checkpointHash = hash
	}
	if changed || budgetChanged {
		c.selectedPins = checkpoint.SelectPins(c.checkpointInput.Users, view.Budget, checkpoint.DefaultLimits)
	}
	c.pinBudget = view.Budget
	pins := slices.Clone(c.selectedPins)
	message, err := c.checkpointFor(hash, view.Budget, pins)
	if err != nil {
		return nil, err
	}
	size, err := contextmeasure.MeasureMessages([]*model.Message{message})
	if err != nil {
		return nil, err
	}
	return &api.ViewUpdate{Checkpoint: hash, Pinned: pins, CheckpointBytes: size.Bytes}, nil
}

// checkpointFor caches exactly the effective ViewRef rendering inputs.
func (c *runViewCache) checkpointFor(hash runstore.Hash, budget index.RenderBudget, pins []index.MessageID) (*model.Message, error) {
	key := checkpointCacheKey{hash: hash, budget: budget, pins: fmt.Sprint(pins)}
	if c.checkpointMessage == nil || key != c.checkpointKey {
		input := c.checkpointInput
		input.Pinned = pins
		message, err := checkpoint.Render(input, budget, checkpoint.DefaultLimits, nil)
		if err != nil {
			return nil, err
		}
		c.checkpointMessage = message
		c.checkpointKey = key
	}
	return c.checkpointMessage, nil
}

// renderTail resolves only the selected suffix and preceding system entries.
func (c *runViewCache) renderTail(ctx context.Context, source index.Source, view index.ViewRef, from index.MessageID) error {
	key := tailCacheKey{head: c.head, from: from, cleared: view.ClearedThrough, preview: view.PreviewThrough, forced: view.ForceClearThrough, previewTokens: view.Budget.PreviewTokens}
	if c.tailMessages == nil || key != c.tailKey {
		prefix := c.entries[:from-1]
		tail := c.entries[from-1:]
		cleared, err := checkpoint.ResultText(ctx, source, tail, view)
		if err != nil {
			return err
		}
		messages, err := index.RenderTail(ctx, tail, source, prefixHidden(prefix), cleared)
		if err != nil {
			return err
		}
		var systems []index.MessageEntry
		for _, entry := range prefix {
			if entry.Role == model.ConversationRoleSystem {
				systems = append(systems, entry)
			}
		}
		systemMessages, err := index.Render(ctx, systems, source)
		if err != nil {
			return err
		}
		c.tailMessages, c.systemMessages, c.tailKey = messages, systemMessages, key
	}
	return nil
}
