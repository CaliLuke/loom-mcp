package index

import "github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript"

// OmitsResultContent reports whether the stored successful result is projected
// as a size-limit omission using only the entry's preview and bounds.
func (e MessageEntry) OmitsResultContent() bool {
	return e.Hash != "" && e.ContentBytes() > transcript.MaxToolResultContentBytes &&
		(e.Result == nil || e.Result.Error == nil)
}

// ContentBytes reports canonical result length. Earlier entries store that
// length in Bytes; measured entries retain it in their result projection.
func (e MessageEntry) ContentBytes() int {
	if e.Result != nil && e.Result.ContentBytes > 0 {
		return e.Result.ContentBytes
	}
	return e.Bytes
}
