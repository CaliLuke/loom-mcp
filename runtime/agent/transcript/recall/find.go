package recall

import (
	"context"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
)

type scanText struct {
	id, run, text string
	part          Part
}

type findScan struct {
	selector                 Cursor
	position, scanned, limit int
	seeking                  bool
	response                 Response
}

// findCursor binds an untrusted continuation to its literal search selector.
func findCursor(in FindInput) (Cursor, error) {
	if in.Cursor == "" {
		return Cursor{Kind: Find, Scan: in.Text}, nil
	}
	cursor, err := DecodeCursor(in.Cursor)
	if err != nil {
		return Cursor{}, err
	}
	if cursor.Kind != Find || cursor.Scan != in.Text {
		return Cursor{}, invalidInput()
	}
	return cursor, nil
}

func findContinuation(response Response, selector Cursor, position int) (Response, error) {
	selector.Position = position
	token, err := EncodeCursor(selector)
	response.Cursor = token
	return response, err
}

func scanLimit(limit int) int {
	if limit <= 0 {
		return 8_000_000
	}
	return limit
}

func (r *callReader) find(ctx context.Context, in FindInput) (Response, error) {
	selector, err := findCursor(in)
	if err != nil {
		return Response{}, err
	}
	runs, err := r.scanRuns(ctx)
	if err != nil {
		return Response{}, err
	}

	limit := scanLimit(r.req.ScanBytes)
	scan := findScan{selector: selector, position: selector.Position, seeking: selector.Anchor != nil, limit: limit}

	for _, node := range runs {
		complete, err := r.scanRun(ctx, node, &scan)
		if err != nil {
			return Response{}, err
		}
		if complete {
			return findContinuation(scan.response, scan.selector, scan.position)
		}
	}
	if scan.seeking {
		return Response{}, invalidInput()
	}
	return scan.response, nil
}

func (r *callReader) scanRuns(ctx context.Context) ([]recordNode, error) {
	runs := []recordNode{{run: r.req.RunID, scope: r.req.Scope}}
	nodes, err := r.records(ctx)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{r.req.RunID: true}
	for _, node := range nodes {
		if !seen[node.run] {
			runs = append(runs, node)
			seen[node.run] = true
		}
	}
	return runs, nil
}

// scanRun scans eligible entries newest first, seeking the continuation anchor.
func (r *callReader) scanRun(ctx context.Context, node recordNode, scan *findScan) (bool, error) {
	entries, err := r.loadEntries(ctx, node.run, node.scope)
	if err != nil {
		return false, err
	}
	for i := len(entries) - 1; i >= 0; i-- {
		entry := entries[i]
		if !r.findEligible(node, entry) {
			continue
		}
		if scan.seeking && (node.run != scan.selector.Anchor.Run || entry.ID != scan.selector.Anchor.Entry) {
			continue
		}
		complete, err := r.scanEntry(ctx, node, entry, scan)
		if err != nil {
			return false, err
		}
		if complete {
			return true, nil
		}
	}
	return false, nil
}

// findEligible excludes typed inputs and limits current-run entries to hidden content.
func (r *callReader) findEligible(node recordNode, entry index.MessageEntry) bool {
	if entry.Kind == index.TypedInput {
		return false
	}
	if node.run != r.req.RunID {
		return true
	}
	return entry.ID < r.req.View.TailFrom || entry.Kind == index.ToolResult && entry.ID <= max(r.req.View.ClearedThrough, r.req.View.PreviewThrough, r.req.View.ForceClearThrough)
}

// scanEntry resumes at the anchored chunk before scanning the rest of an entry.
func (r *callReader) scanEntry(ctx context.Context, node recordNode, entry index.MessageEntry, scan *findScan) (bool, error) {
	chunks, err := r.scanChunks(ctx, node, entry)
	if err != nil {
		return false, err
	}
	for at, chunk := range chunks {
		anchor := FindAnchor{Run: node.run, Entry: entry.ID, Chunk: at}
		if scan.seeking {
			if anchor != *scan.selector.Anchor {
				continue
			}
			if scan.position > len(chunk.text) {
				return false, invalidInput()
			}
			scan.seeking = false
		}
		scan.selector.Anchor = &anchor
		if scan.append(chunk, scan.selector.Scan) {
			return true, nil
		}
	}
	return false, nil
}

// append scans immutable chunk text within the remaining byte and match budgets.
func (scan *findScan) append(chunk scanText, needle string) bool {
	offset := scan.position
	for offset < len(chunk.text) {
		available := min(len(chunk.text)-offset, scan.limit-scan.scanned)
		if available <= 0 {
			scan.position = offset
			return true
		}
		end := offset + available
		hit := strings.Index(chunk.text[offset:end], needle)
		if hit >= 0 {
			hit += offset
			scan.response.Matches = append(scan.response.Matches, contextualMatch(chunk, hit, len(needle)))
			scan.response.ReadHint = "Snippets are partial excerpts. offset locates the exact match; snippet_offset locates the excerpt. For surrounding evidence, read id/run/part with offset=snippet_offset, then follow next_offset; read earlier offsets if needed. A fact missing from a snippet may still be in the source."
			consumed := hit + len(needle) - offset
			scan.scanned += consumed
			offset += consumed
			if len(scan.response.Matches) == 10 {
				scan.position = offset
				return true
			}
		} else {
			// Preserve a possible match straddling a scan boundary without making
			// the cursor authority or repeating an already emitted match.
			consumed := available
			if end < len(chunk.text) && available >= len(needle) {
				consumed -= len(needle) - 1
			}
			scan.scanned += available
			offset += consumed
			if end < len(chunk.text) {
				scan.position = offset
				return true
			}
		}
	}
	scan.position = 0
	return false
}

// contextualMatch retains preceding evidence without displacing a literal that
// fits the snippet. Match and scan offsets remain independent of excerpt bounds.
func contextualMatch(chunk scanText, hit, literalBytes int) Match {
	before := min(64, 160-min(literalBytes, 160))
	start := max(0, hit-before)
	for start < hit && !utf8.RuneStart(chunk.text[start]) {
		start++
	}
	end := snap(chunk.text, min(start+160, len(chunk.text)))
	return Match{ID: chunk.id, Run: chunk.run, Part: chunk.part, Offset: hit, SnippetOffset: start, Snippet: chunk.text[start:end]}
}

func (r *callReader) scanChunks(ctx context.Context, node recordNode, entry index.MessageEntry) ([]scanText, error) {
	text, err := r.text(ctx, node.scope, entry)
	if err != nil {
		return nil, err
	}
	id := strconv.FormatUint(uint64(entry.ID), 10)
	if entry.Kind == index.ToolResult && len(entry.ToolUseIDs) > 0 {
		id = entry.ToolUseIDs[0]
	}
	chunks := []scanText{{id: id, run: node.run, part: Result, text: text.text}}
	calls, err := r.calls(ctx, node.scope, entry)
	if err != nil {
		return nil, err
	}
	for _, call := range calls {
		chunks = append(chunks, scanText{id: call.ToolCallID, run: node.run, part: Arguments, text: string(call.Payload)})
	}

	return chunks, nil
}
