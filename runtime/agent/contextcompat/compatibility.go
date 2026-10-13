// Package contextcompat verifies original attachment compatibility from durable
// transcript and model-call evidence. It does not consult live providers.
package contextcompat

import (
	"context"
	"crypto/sha256"
	"encoding/json/v2"
	"fmt"
	"maps"
	"slices"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/callrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/compaction"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextmeasure"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
)

// Request names the durable original content and first adopted model calls.
type Request struct {
	Scope     string
	RunID     string
	IndexHead runstore.Hash
	Pending   []index.MessageEntry
	Calls     []runstore.CallKey
	Coverage  string
	Support   string
}

// Result addresses a compact fact that must be adopted before the decision.
type Result struct {
	Ref      runstore.Hash
	Evidence compaction.Compatibility
}

// Fact binds the compact result to its durable request provenance.
type Fact struct {
	Request  Request
	Evidence compaction.Compatibility
}

// BuildRequest snapshots coverage and support identities without content reads.
func BuildRequest(scope, runID string, head runstore.Hash, pending, entries []index.MessageEntry, state compaction.State) Request {
	calls := make([]runstore.CallKey, 0, len(state.Models))
	for _, name := range slices.Sorted(maps.Keys(state.Models)) {
		calls = append(calls, state.Models[name].FirstCall)
	}
	return Request{Scope: scope, RunID: runID, IndexHead: head, Pending: slices.Clone(pending), Calls: calls, Coverage: compaction.NonTextCoverage(entries), Support: compaction.SupportFingerprint(state)}
}

// Check verifies durable inputs and stores their compatibility fact.
func Check(ctx context.Context, store runstore.Store, request Request) (Result, error) {
	evidence, err := inspect(ctx, store, request)
	if err != nil {
		return Result{}, err
	}
	data, err := json.Marshal(Fact{Request: request, Evidence: evidence}, json.Deterministic(true))
	if err != nil {
		return Result{}, fmt.Errorf("encode compatibility fact: %w", err)
	}
	ref, err := store.Put(ctx, request.Scope, data)
	if err != nil {
		return Result{}, fmt.Errorf("store compatibility fact: %w", err)
	}
	return Result{Ref: ref, Evidence: evidence}, nil
}

// ReadFact resolves and validates the addressed fact's encoding and content hash.
func ReadFact(ctx context.Context, store runstore.Store, scope string, result Result) (Fact, error) {
	data, err := source(store, scope)(ctx, result.Ref)
	if err != nil {
		return Fact{}, err
	}
	var fact Fact
	if err := json.Unmarshal(data, &fact, json.RejectUnknownMembers(true)); err != nil {
		return Fact{}, fmt.Errorf("decode compatibility fact: %w", err)
	}
	if fact.Request.Scope != scope || fact.Evidence != result.Evidence || !fact.Evidence.Checked {
		return Fact{}, fmt.Errorf("compatibility fact identity mismatch")
	}
	return fact, nil
}

// Validate independently verifies an adopted result and its addressed fact.
func Validate(ctx context.Context, store runstore.Store, request Request, result Result) error {
	fact, err := ReadFact(ctx, store, request.Scope, result)
	if err != nil {
		return err
	}
	if fact.Request.RunID != request.RunID || fact.Request.Coverage != request.Coverage || fact.Request.Support != request.Support || !slices.Equal(fact.Request.Calls, request.Calls) {
		return fmt.Errorf("stale compatibility provenance")
	}
	verified, err := inspect(ctx, store, fact.Request)
	if err != nil {
		return err
	}
	if verified != result.Evidence {
		return fmt.Errorf("compatibility result differs from durable evidence")
	}
	return nil
}

func inspect(ctx context.Context, store runstore.Store, request Request) (compaction.Compatibility, error) {
	entries, state, err := Resolve(ctx, store, request)
	if err != nil {
		return compaction.Compatibility{}, err
	}
	evidence := compaction.Compatibility{Coverage: request.Coverage, Support: request.Support, Checked: true, Compatible: true}
	support := compaction.ToolResultParts(state)
	names := slices.Sorted(maps.Keys(state.Models))
	for _, entry := range entries {
		if entry.NonText <= 0 {
			continue
		}
		messages, err := index.Render(ctx, []index.MessageEntry{entry}, source(store, request.Scope))
		if err != nil {
			return compaction.Compatibility{}, fmt.Errorf("read original attachment entry %d: %w", entry.ID, err)
		}
		parts := originalParts(messages)
		if len(parts) == 0 {
			return compaction.Compatibility{}, fmt.Errorf("non-text entry %d contains no original attachment", entry.ID)
		}
		evidence = inspectParts(evidence, state, support, names, parts)
	}
	return evidence, nil
}

// Resolve verifies committed-plus-pending coverage and terminal call provenance.
// Adoption order is checked separately by the canonical run-record reader.
func Resolve(ctx context.Context, store runstore.Store, request Request) ([]index.MessageEntry, compaction.State, error) {
	if store == nil || request.Scope == "" || request.RunID == "" || len(request.Calls) == 0 || request.Coverage == "" || request.Support == "" {
		return nil, compaction.State{}, fmt.Errorf("incomplete compatibility request")
	}
	entries, err := resolveEntries(ctx, store, request)
	if err != nil {
		return nil, compaction.State{}, err
	}
	records, err := store.ListCalls(ctx, request.Scope, request.RunID)
	if err != nil {
		return nil, compaction.State{}, fmt.Errorf("list compatibility calls: %w", err)
	}
	byKey := make(map[runstore.CallKey]runstore.CallRecord, len(records))
	for _, record := range records {
		byKey[record.Key] = record
	}
	state := compaction.State{}
	seen := make(map[string]bool)
	for _, key := range request.Calls {
		record, ok := byKey[key]
		if !ok || key.RunID != request.RunID || (record.State != runstore.CallCompleted && record.State != runstore.CallFailed) {
			return nil, state, fmt.Errorf("compatibility call is not terminal: %+v", key)
		}
		facts, err := terminalFacts(record, key)
		if err != nil {
			return nil, state, err
		}
		if seen[facts.Model] {
			return nil, state, fmt.Errorf("duplicate first compatibility model: %s", facts.Model)
		}

		seen[facts.Model] = true
		state = compaction.Observe(state, []contextmeasure.RequestFacts{*facts})
	}
	if compaction.SupportFingerprint(state) != request.Support {
		return nil, state, fmt.Errorf("compatibility support mismatch")
	}
	return entries, state, nil
}

func source(store runstore.Store, scope string) index.Source {
	return func(ctx context.Context, hash runstore.Hash) ([]byte, error) {
		if hash == "" {
			return nil, fmt.Errorf("missing compatibility content reference")
		}
		data, err := store.Get(ctx, scope, hash)
		if err != nil {
			return nil, err
		}
		if runstore.Hash(fmt.Sprintf("sha256:%x", sha256.Sum256(data))) != hash {
			return nil, fmt.Errorf("compatibility content hash mismatch")
		}
		return data, nil
	}
}

func originalParts(messages []*model.Message) []model.ToolResultContentPart {
	var parts []model.ToolResultContentPart
	var appendPart func(model.Part)
	appendPart = func(part model.Part) {
		switch value := part.(type) {
		case model.ImagePart:
			parts = append(parts, value)
		case model.DocumentPart:
			if len(value.Bytes) > 0 || value.URI != "" {
				parts = append(parts, value)
			}
		case model.ToolResultPart:
			for _, nested := range value.Parts {
				appendPart(nested)
			}
		}
	}
	for _, message := range messages {
		if message != nil {
			for _, part := range message.Parts {
				appendPart(part)
			}
		}
	}
	return parts
}

func resolveEntries(ctx context.Context, store runstore.Store, request Request) ([]index.MessageEntry, error) {
	entries, err := index.Walk(ctx, source(store, request.Scope), request.IndexHead)
	if err != nil {
		return nil, err
	}
	var chain runstore.Hash
	if len(entries) > 0 {
		chain = entries[len(entries)-1].Chain
	}
	for _, entry := range request.Pending {
		if entry.ID != index.MessageID(len(entries)+1) {
			return nil, fmt.Errorf("compatibility pending entry gap")
		}
		computed, err := index.ComputeChain(chain, entry)
		if err != nil {
			return nil, err
		}
		if entry.Chain != "" && entry.Chain != computed {
			return nil, fmt.Errorf("compatibility pending chain mismatch")
		}
		chain = computed
		entries = append(entries, entry)
	}
	if compaction.NonTextCoverage(entries) != request.Coverage {
		return nil, fmt.Errorf("compatibility coverage mismatch")
	}

	return entries, nil
}

func terminalFacts(record runstore.CallRecord, key runstore.CallKey) (*contextmeasure.RequestFacts, error) {
	if err := record.Validate(); err != nil {
		return nil, err
	}
	started, err := callrecord.DecodeStarted(record.Started)
	if err != nil {
		return nil, err
	}
	completion, err := callrecord.DecodeCompletion(record.Completion)
	if err != nil {
		return nil, err
	}
	facts := completion.Facts
	if started.Key != key || facts == nil || facts.Call != key || !facts.ViewBearing || facts.Model == "" {
		return nil, fmt.Errorf("invalid first compatibility call facts: %+v", key)
	}
	return facts, nil
}

func inspectParts(evidence compaction.Compatibility, state compaction.State, support model.ToolResultPartSupport, names []string, parts []model.ToolResultContentPart) compaction.Compatibility {
	for _, part := range parts {
		if accepted, _ := support.Accepts(part); accepted {
			continue
		}
		evidence.Compatible = false
		evidence.Reason = "too_large"
		if len(support.ImageFormats) == 0 && len(support.DocumentFormats) == 0 && len(support.DocumentURISchemes) == 0 {
			evidence.Reason = "provider"
		}
		for _, name := range names {
			if accepted, _ := state.Models[name].ToolResultParts.Accepts(part); !accepted {
				if evidence.Model == "" || name < evidence.Model {
					evidence.Model = name
				}
				break
			}
		}
		if evidence.Model == "" && len(names) > 0 {
			evidence.Model = names[0]
		}
	}
	return evidence
}
