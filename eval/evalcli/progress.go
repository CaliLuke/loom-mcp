package evalcli

import (
	"context"
	"fmt"

	"github.com/CaliLuke/loom-mcp/v2/eval"
)

// progress runs serially through Writer.OnAttempt after the durable append.
func progress(cfg Config, f flags, writer *eval.Writer, planned int, cancel context.CancelCauseFunc) func(*eval.AttemptRecord) {
	if f.quiet {
		return nil
	}
	done := 0
	return func(a *eval.AttemptRecord) {
		// Writer invokes its next callback even on failure; never report unwritten work.
		if writer.Err() != nil {
			return
		}
		done++
		if _, err := fmt.Fprintf(cfg.Stderr, "[%d/%d] %s %s %s %d tok\n", done, planned, a.Verdict(), a.AttemptKey, a.Duration, a.Tokens); err != nil {
			cancel(err)
		}
	}
}
