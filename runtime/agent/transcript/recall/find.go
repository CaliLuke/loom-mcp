package recall

import (
	"context"
	"strconv"
	"strings"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
)

type scanText struct{ id, run, text string }

type findScan struct {
	selector                          Cursor
	position, skipped, scanned, limit int
	response                          Response
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
	selector := Cursor{Kind: Find, Scan: in.Text}
	position, err := cursorPosition(in.Cursor, selector)
	if err != nil {
		return Response{}, err
	}
	runs, err := r.scanRuns(ctx)
	if err != nil {
		return Response{}, err
	}

	limit := scanLimit(r.req.ScanBytes)
	scan := findScan{selector: selector, position: position, limit: limit}

	for _, node := range runs {
		entries, err := r.loadEntries(ctx, node.run, node.scope)
		if err != nil {
			return Response{}, err
		}
		for i := len(entries) - 1; i >= 0; i-- {
			entry := entries[i]
			if node.run == r.req.RunID && !(entry.ID < r.req.View.TailFrom || entry.Kind == index.ToolResult && entry.ID <= r.req.View.ClearedThrough) {
				continue
			}
			if entry.Kind == index.TypedInput {
				continue
			}
			chunks, err := r.scanChunks(ctx, node, entry)
			if err != nil {
				return Response{}, err
			}
			for _, chunk := range chunks {
				if complete := scan.append(chunk, in.Text); complete {
					return findContinuation(scan.response, selector, scan.skipped)
				}
			}
		}
	}
	if position > scan.skipped {
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

func (scan *findScan) append(chunk scanText, needle string) bool {
	if scan.skipped+len(chunk.text) <= scan.position {
		scan.skipped += len(chunk.text)
		return false
	}
	offset := max(0, scan.position-scan.skipped)
	for offset < len(chunk.text) {
		available := min(len(chunk.text)-offset, scan.limit-scan.scanned)
		if available <= 0 {
			scan.skipped += offset
			return true
		}
		end := offset + available
		hit := strings.Index(chunk.text[offset:end], needle)
		if hit >= 0 {
			hit += offset
			scan.response.Matches = append(scan.response.Matches, Match{ID: chunk.id, Run: chunk.run, Offset: hit, Snippet: chunk.text[hit:snap(chunk.text, min(hit+160, len(chunk.text)))]})
			consumed := hit + len(needle) - offset
			scan.scanned += consumed
			offset += consumed
			if len(scan.response.Matches) == 10 {
				scan.skipped += offset
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
				scan.skipped += offset
				return true
			}
		}
	}
	scan.skipped += len(chunk.text)
	return false
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
	chunks := []scanText{{id: id, run: node.run, text: text.text}}
	calls, err := r.calls(ctx, node.scope, entry)
	if err != nil {
		return nil, err
	}
	for _, call := range calls {
		chunks = append(chunks, scanText{id: call.ToolCallID, run: node.run, text: string(call.Payload)})
	}

	return chunks, nil
}
