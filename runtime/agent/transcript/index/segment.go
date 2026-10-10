package index

import (
	"context"
	"crypto/sha256"
	"encoding/json/v2"
	"fmt"
	"slices"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
)

// NewSegment copies entries, computes their running chains, and checks their
// sequence. previousChain is the last committed entry's Chain (empty at start).
// It does not store anything or advance a head; only a successful store write
// authorizes adoption of the hash returned by EncodeSegment.
func NewSegment(prev, previousChain runstore.Hash, entries []MessageEntry) (IndexSegment, error) {
	data, err := json.Marshal(entries, json.Deterministic(true))
	if err != nil {
		return IndexSegment{}, err
	}
	var owned []MessageEntry
	if err := json.Unmarshal(data, &owned); err != nil {
		return IndexSegment{}, err
	}
	seg := IndexSegment{Entries: owned}
	if len(owned) == 0 {
		return seg, fmt.Errorf("index: empty segment")
	}
	seg.First = owned[0].ID
	if prev != "" {
		seg.Prev = &prev
	}
	if (prev == "") != (previousChain == "") {
		return seg, fmt.Errorf("index: predecessor and previous chain must both be set or empty")
	}
	for i := range seg.Entries {
		chain, err := ComputeChain(previousChain, seg.Entries[i])
		if err != nil {
			return IndexSegment{}, err
		}
		seg.Entries[i].Chain = chain
		previousChain = chain
	}
	return seg, validateSegment(seg)
}

// ComputeChain hashes the previous chain and the complete deterministic entry
// encoding, excluding Chain itself. This binds order, content and rendering
// metadata, including later hiding operations, without rewriting earlier entries.
func ComputeChain(previous runstore.Hash, entry MessageEntry) (runstore.Hash, error) {
	entry.Chain = ""
	if err := validateEntry(entry); err != nil {
		return "", err
	}
	data, err := json.Marshal(struct {
		Domain   string        `json:"domain"`
		Previous runstore.Hash `json:"previous"`
		Entry    MessageEntry  `json:"entry"`
	}{"loom.transcript.entry.v1", previous, entry}, json.Deterministic(true))
	if err != nil {
		return "", err
	}
	return digest(data), nil
}

// EncodeSegment returns deterministic JSON v2 bytes and their run-store hash.
// A hash is an address, not proof that the segment has been stored.
func EncodeSegment(seg IndexSegment) ([]byte, runstore.Hash, error) {
	if err := validateSegment(seg); err != nil {
		return nil, "", err
	}
	data, err := json.Marshal(seg, json.Deterministic(true))
	if err != nil {
		return nil, "", err
	}
	return data, digest(data), nil
}

// Walk reconstructs exactly the committed entries reachable from head. It
// rejects missing content, altered bytes, sequence gaps and broken entry chains.
// Pending entries and orphaned segments are intentionally not part of this walk.
func Walk(ctx context.Context, source Source, head runstore.Hash) ([]MessageEntry, error) {
	var segments []IndexSegment
	seen := make(map[runstore.Hash]bool)
	for head != "" {
		if seen[head] {
			return nil, fmt.Errorf("index: cyclic segment chain at %s", head)
		}
		seen[head] = true
		data, err := source(ctx, head)
		if err != nil {
			return nil, fmt.Errorf("index: load segment %s: %w", head, err)
		}
		if digest(data) != head {
			return nil, fmt.Errorf("index: segment hash mismatch at %s", head)
		}
		var seg IndexSegment
		if err := json.Unmarshal(data, &seg, json.RejectUnknownMembers(true)); err != nil {
			return nil, fmt.Errorf("index: decode segment: %w", err)
		}
		if err := validateSegment(seg); err != nil {
			return nil, err
		}
		segments = append(segments, seg)
		head = ""
		if seg.Prev != nil {
			head = *seg.Prev
		}
	}
	return reconstruct(segments)
}

func digest(data []byte) runstore.Hash {
	return runstore.Hash(fmt.Sprintf("sha256:%x", sha256.Sum256(data)))
}

func reconstruct(segments []IndexSegment) ([]MessageEntry, error) {
	var out []MessageEntry
	var previous runstore.Hash
	for _, seg := range slices.Backward(segments) {
		if seg.First != MessageID(len(out))+1 {
			return nil, fmt.Errorf("index: segment gap at %d", seg.First)
		}
		for _, entry := range seg.Entries {
			chain, err := ComputeChain(previous, entry)
			if err != nil {
				return nil, err
			}
			if chain != entry.Chain {
				return nil, fmt.Errorf("index: entry %d chain mismatch", entry.ID)
			}
			previous = chain
			out = append(out, entry)
		}
	}
	return out, nil
}

func validateSegment(seg IndexSegment) error {
	if len(seg.Entries) == 0 || seg.First == 0 {
		return fmt.Errorf("index: segment must contain entries starting at a positive ID")
	}
	if seg.Prev == nil && seg.First != 1 || seg.Prev != nil && *seg.Prev == "" {
		return fmt.Errorf("index: invalid segment predecessor")
	}
	for i, entry := range seg.Entries {
		if entry.ID != seg.First+MessageID(i) || entry.Chain == "" {
			return fmt.Errorf("index: noncontiguous or unchained entry %d", entry.ID)
		}
		if err := validateEntry(entry); err != nil {
			return err
		}
	}
	return nil
}
