package checkpoint

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"slices"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
)

// FromRecord reads a checkpoint and its Prev chain without writes. Receipts are
// repeatable and newest-first. It loads only eligible users and summary text,
// never entry.Record. currentPos is reserved; CurrentPos is zero until K18.
func FromRecord(ctx context.Context, source index.Source, hash runstore.Hash, entries []index.MessageEntry, _ int) (Input, error) {
	records, hashes, err := readRecords(ctx, source, hash, entries)
	if err != nil {
		return Input{}, err
	}
	latest := records[0]
	in := Input{ID: CheckpointID(string(hash)), Through: latest.Through, RunPos: map[string]int{latest.SummaryRun: 0}}
	summary, err := recordSummary(ctx, source, records, hashes)
	if err != nil {
		return Input{}, err
	}
	in.Summary = summary
	users, err := recordUsers(ctx, source, entries, latest.Through)
	if err != nil {
		return Input{}, err
	}
	in.Users = users
	in.CitedUsers = recordCitedUsers(latest.CitedUsers, entries)
	in.Receipt = func(yield func(ReceiptItem) bool) {
		for _, record := range records {
			for i := len(record.Receipt) - 1; i >= 0; i-- {
				line := record.Receipt[i]
				item := ReceiptItem{Origin: line.Origin, Unsummarised: line.Unsummarised}
				switch line.Kind {
				case "part":
					item.PartID, item.Part, item.Format, item.Bytes, item.PartText = line.MessageID, line.Part, line.Format, line.Bytes, line.Text
				case "call":
					item.ToolUseID, item.Tool, item.Args = line.ToolUseID, line.Tool, line.Text
					item.Lookup = tools.Ident(line.Tool) == tools.RecallContext
				default:
					item.AssistantID, item.Text = line.From, line.Text
				}
				if !yield(item) {
					return
				}
			}
		}
	}
	return in, nil
}

func recordSummary(ctx context.Context, source index.Source, records []index.CheckpointRecord, hashes []runstore.Hash) (*Summary, error) {
	latest := records[0]
	if latest.Summary == nil {
		return nil, nil
	}
	data, err := source(ctx, *latest.Summary)
	if err != nil {
		return nil, fmt.Errorf("checkpoint summary: %w", err)
	}
	summaryID := hashes[0]
	for i, record := range records {
		if record.Summary == nil || *record.Summary != *latest.Summary {
			break
		}
		summaryID = hashes[i]
	}
	return &Summary{Text: string(data), Run: latest.SummaryRun, ID: string(summaryID)}, nil
}

func recordCitedUsers(lines []index.IndexLine, entries []index.MessageEntry) []UserLine {
	users := make([]UserLine, 0, len(lines))
	for _, line := range lines {
		size := len(line.Text)
		for _, entry := range entries {
			if entry.ID == line.ID {
				size = entry.Bytes
				break
			}
		}
		users = append(users, UserLine{ID: line.ID, Origin: line.Origin, Text: line.Text, Bytes: size})
	}
	return users
}

// readRecords resolves errors before creating the error-free receipt iterator.
func readRecords(ctx context.Context, source index.Source, hash runstore.Hash, entries []index.MessageEntry) ([]index.CheckpointRecord, []runstore.Hash, error) {
	var records []index.CheckpointRecord
	var hashes []runstore.Hash
	seen := map[runstore.Hash]bool{}
	for {
		if seen[hash] {
			return nil, nil, fmt.Errorf("checkpoint cyclic Prev at %s", hash)
		}
		seen[hash] = true
		data, err := source(ctx, hash)
		if err != nil {
			return nil, nil, fmt.Errorf("checkpoint record %s: %w", hash, err)
		}
		var record index.CheckpointRecord
		if err := json.Unmarshal(data, &record, json.RejectUnknownMembers(true)); err != nil {
			return nil, nil, fmt.Errorf("checkpoint record %s: %w", hash, err)
		}
		if len(records) > 0 {
			successor := records[len(records)-1]
			if record.Through >= successor.Through {
				return nil, nil, fmt.Errorf("checkpoint Prev does not advance Through")
			}
			if !preservesSummary(successor, record) {
				return nil, nil, fmt.Errorf("receipt-only checkpoint drops previous summary evidence")
			}
		}
		if err := validateRecord(record, entries); err != nil {
			return nil, nil, err
		}
		records = append(records, record)
		hashes = append(hashes, hash)
		if record.Prev == nil {
			return records, hashes, nil
		}
		hash = *record.Prev
	}
}

func validateRecord(record index.CheckpointRecord, entries []index.MessageEntry) error {
	if record.Through > 0 && (uint64(record.Through) > uint64(len(entries)) || entries[record.Through-1].ID != record.Through || entries[record.Through-1].Chain != record.Chain) {
		return fmt.Errorf("checkpoint Through/Chain does not match index")
	}
	for _, line := range record.Receipt {
		if line.Kind != "call" && line.Kind != "assistant" && line.Kind != "part" {
			return fmt.Errorf("checkpoint invalid receipt kind %q", line.Kind)
		}
	}
	return nil
}

func recordUsers(ctx context.Context, source index.Source, entries []index.MessageEntry, through index.MessageID) ([]UserMessage, error) {
	var users []UserMessage
	for _, entry := range entries {
		if entry.ID > through {
			break
		}
		if entry.Kind != index.Clarification && (entry.Kind != index.Message || entry.Role != model.ConversationRoleUser) {
			continue
		}
		data, err := source(ctx, entry.Hash)
		if err != nil {
			return nil, fmt.Errorf("checkpoint user %d: %w", entry.ID, err)
		}
		var message *model.Message
		if entry.Kind == index.Clarification {
			message = &model.Message{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: string(data)}}}
		} else if err := json.Unmarshal(data, &message); err != nil {
			return nil, fmt.Errorf("checkpoint user %d: %w", entry.ID, err)
		}
		if message == nil || message.Role != model.ConversationRoleUser {
			return nil, fmt.Errorf("checkpoint user %d has no user content", entry.ID)
		}
		if onlyToolResults(message) {
			continue
		}
		users = append(users, UserMessage{ID: entry.ID, Origin: entry.Origin, Message: message})
	}
	return users, nil
}

// preservesSummary rejects the UnsafeEvictDropsSummary fault for receipt-only
// successors. Summary-producing records identify their calls; receipt records
// have none and must copy all three summary-evidence fields from Prev.
func preservesSummary(successor, previous index.CheckpointRecord) bool {
	if len(successor.Calls) > 0 {
		return true
	}
	if successor.SummaryRun != previous.SummaryRun || !sameHash(successor.Summary, previous.Summary) {
		return false
	}
	return slices.EqualFunc(successor.CitedUsers, previous.CitedUsers, func(a, b index.IndexLine) bool {
		if a.ID != b.ID || a.Text != b.Text {
			return false
		}
		if a.Origin == nil || b.Origin == nil {
			return a.Origin == b.Origin
		}
		return *a.Origin == *b.Origin
	})
}

func sameHash(a, b *runstore.Hash) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}
