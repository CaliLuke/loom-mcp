// Package evidence provides opt-in durable local storage for canonical agent
// run evidence. It retains raw records; callers must apply a sharing projection
// before exposing them publicly.
package evidence

import (
	"context"
	"errors"
	"fmt"

	"github.com/CaliLuke/loom-mcp/v2/internal/localevidence"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
)

// Store is the local SQLite backend for both runtime RunStore and RunEventStore.
// The caller owns it and must stop evidence producers before Close. Pass the same
// store to runtime.WithRunStore and runtime.WithRunEventStore explicitly.
type Store = localevidence.Store

// Snapshot contains raw canonical records from a local capture. Unlinked holds
// call runs without a committed link in any root tree. These records are replay
// evidence and are not filtered for sharing.
type Snapshot struct {
	Records  []*runrecord.RunRecord
	Unlinked []*runrecord.RunRecord
}

// Create exclusively creates a fresh capture. An existing evidence database
// returns os.ErrExist; Create never overwrites or reuses one.
func Create(ctx context.Context, dir string) (*Store, error) {
	return localevidence.Create(ctx, dir)
}

// Open reopens an existing capture for access to its records. It does not
// resume interrupted agent execution.
func Open(ctx context.Context, dir string) (*Store, error) {
	return localevidence.Open(ctx, dir)
}

// Read reconstructs raw evidence from a capture independently of a completed
// run. Incomplete calls remain incomplete. Read returns any readable records
// alongside explicit reconstruction errors.
func Read(ctx context.Context, dir string) (snapshot *Snapshot, err error) {
	store, err := Open(ctx, dir)
	if err != nil {
		return nil, fmt.Errorf("evidence: open: %w", err)
	}
	defer func() {
		err = errors.Join(err, store.Close())
	}()
	snapshot = &Snapshot{}
	roots, err := store.Roots(ctx)
	if err != nil {
		return snapshot, err
	}
	for _, root := range roots {
		if readErr := readRoot(ctx, store, root, snapshot); readErr != nil {
			err = errors.Join(err, fmt.Errorf("read evidence root %s: %w", root, readErr))
		}
	}
	return snapshot, err
}

func readRoot(ctx context.Context, store *Store, root string, snapshot *Snapshot) error {
	record, err := runrecord.Read(ctx, store, store, root, root)
	if err != nil {
		return err
	}
	snapshot.Records = append(snapshot.Records, record)
	seen := make(map[string]bool)
	for _, run := range record.Runs {
		seen[run.RunID] = true
	}
	runs, err := store.RunIDs(ctx, root)
	if err != nil {
		return err
	}
	for _, id := range runs {
		if seen[id] {
			continue
		}
		unlinked, err := runrecord.Read(ctx, store, store, root, id)
		if err != nil {
			return err
		}
		snapshot.Unlinked = append(snapshot.Unlinked, unlinked)
		for _, run := range unlinked.Runs {
			seen[run.RunID] = true
		}
	}
	return nil
}
