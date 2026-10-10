package index

import "github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript"

// OmitsResultContent reports whether the stored successful result is projected
// as a size-limit omission using only the entry's preview and bounds.
func (e MessageEntry) OmitsResultContent() bool {
	return e.Hash != "" && e.Bytes > transcript.MaxToolResultContentBytes &&
		(e.Result == nil || e.Result.Error == nil)
}
