package localevidence

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
)

func TestInterruptedTransaction(t *testing.T) {
	if dir := os.Getenv("LOOM_SQL_TRANSACTION_DIR"); dir != "" {
		store, err := Open(t.Context(), dir)
		require.NoError(t, err)
		tx, err := store.db.BeginTx(t.Context(), nil)
		require.NoError(t, err)
		_, err = tx.ExecContext(t.Context(), "UPDATE calls SET state='completed',completion=?", []byte("partial write"))
		require.NoError(t, err)
		_, err = fmt.Fprintln(os.Stdout, "transaction written")
		require.NoError(t, err)
		<-t.Context().Done()
		t.Fatal("transaction child was not killed")
	}
	dir := t.TempDir()
	store, err := Create(t.Context(), dir)
	require.NoError(t, err)
	started := runstore.CallRecord{Key: runstore.CallKey{RunID: "run", Seq: 1, Attempt: 1}, State: runstore.CallStarted, Started: []byte("request")}
	require.NoError(t, store.PutCall(t.Context(), "root", started))
	require.NoError(t, store.Close())
	executable, err := os.Executable()
	require.NoError(t, err)
	require.NoError(t, os.Symlink(executable, filepath.Join(dir, "evidence-test-helper")))
	command := exec.CommandContext(t.Context(), "./evidence-test-helper", "-test.run=^TestInterruptedTransaction$")
	command.Dir = dir
	command.Env = append(os.Environ(), "LOOM_SQL_TRANSACTION_DIR="+dir)
	stdout, err := command.StdoutPipe()
	require.NoError(t, err)
	command.Stderr = os.Stderr
	require.NoError(t, command.Start())
	scanner := bufio.NewScanner(stdout)
	require.True(t, scanner.Scan())
	assert.Equal(t, "transaction written", scanner.Text())
	require.NoError(t, command.Process.Kill())
	require.Error(t, command.Wait())
	store, err = Open(t.Context(), dir)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, store.Close())
	})
	calls, err := store.ListCalls(t.Context(), "root", "run")
	require.NoError(t, err)
	assert.Equal(t, []runstore.CallRecord{started}, calls)
}
