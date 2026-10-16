package localevidence

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
)

// Put commits exact content bytes before a referring call or event is written.
func (s *Store) Put(ctx context.Context, scope string, data []byte) (runstore.Hash, error) {
	if scope == "" {
		return "", errors.New("local evidence: scope is required")
	}
	hash := runstore.Hash(fmt.Sprintf("sha256:%x", sha256.Sum256(data)))
	if data == nil {
		data = []byte{}
	}
	_, err := s.db.ExecContext(ctx, "INSERT INTO content(scope,hash,data) VALUES(?,?,?) ON CONFLICT DO NOTHING", scope, string(hash), data)
	return hash, err
}

// Get returns exact scoped bytes and verifies their content address.
func (s *Store) Get(ctx context.Context, scope string, hash runstore.Hash) ([]byte, error) {
	if scope == "" {
		return nil, errors.New("local evidence: scope is required")
	}
	var data []byte
	err := s.db.QueryRowContext(ctx, "SELECT data FROM content WHERE scope=? AND hash=?", scope, string(hash)).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, runstore.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if runstore.Hash(fmt.Sprintf("sha256:%x", sha256.Sum256(data))) != hash {
		return nil, fmt.Errorf("local evidence: corrupt content %s", hash)
	}
	return data, nil
}

// NextSeq atomically allocates a durable run-local sequence across processes.
func (s *Store) NextSeq(ctx context.Context, scope, runID string) (int, error) {
	if scope == "" {
		return 0, errors.New("local evidence: scope is required")
	}
	var seq int
	err := s.db.QueryRowContext(ctx, "INSERT INTO counters(scope,run_id,seq) VALUES(?,?,1) ON CONFLICT(scope,run_id) DO UPDATE SET seq=seq+1 RETURNING seq", scope, runID).Scan(&seq)
	return seq, err
}

// DeleteScope atomically deletes scoped content, calls and counters.
func (s *Store) DeleteScope(ctx context.Context, scope string) (err error) {
	if scope == "" {
		return errors.New("local evidence: scope is required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx, &err)
	for _, statement := range []string{"DELETE FROM content WHERE scope=?", "DELETE FROM calls WHERE scope=?", "DELETE FROM counters WHERE scope=?", "DELETE FROM roots WHERE scope=?"} {
		if _, err := tx.ExecContext(ctx, statement, scope); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func rollback(tx *sql.Tx, target *error) {
	if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
		*target = errors.Join(*target, err)
	}
}
