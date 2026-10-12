package runrecord

import (
	"fmt"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/compaction"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
)

// recordContextFingerprint resolves the immutable configuration into the export.
func (r *runReader) recordContextFingerprint(fingerprint string) error {
	if fingerprint == "" {
		return r.checkContextFingerprint(fingerprint)
	}
	if r.run.ContextFingerprint != "" {
		return r.checkContextFingerprint(fingerprint)
	}
	data, err := r.owner.source(r.owner.record.RootRunID)(r.owner.ctx, runstore.Hash(fingerprint))
	if err != nil {
		return fmt.Errorf("context fingerprint %s: %w", fingerprint, err)
	}
	var settings compaction.Settings
	if err := compaction.DecodeCanonical(data, &settings); err != nil {
		return err
	}
	if err := r.recordContextSettings(&settings); err != nil {
		return err
	}
	if settings.Prompt.Template != "" {
		if _, err := r.owner.source(r.owner.record.RootRunID)(r.owner.ctx, settings.Prompt.Template); err != nil {
			return err
		}
	}
	r.run.ContextFingerprint = fingerprint
	return nil
}

func (r *runReader) checkContextFingerprint(fingerprint string) error {
	if fingerprint != r.run.ContextFingerprint {
		return fmt.Errorf("context fingerprint %q differs from run fingerprint %q", fingerprint, r.run.ContextFingerprint)
	}
	return nil
}
