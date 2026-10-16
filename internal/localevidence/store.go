// Package localevidence persists harness-owned canonical evidence in SQLite.
package localevidence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite" // Register the existing pure-Go SQLite driver.
)

// Store implements the canonical runstore and runlog interfaces.
type Store struct {
	db *sql.DB
}

// FileName is the private capture database's fixed file name.
const FileName = "evidence.sqlite"

const schema = `
CREATE TABLE roots(scope TEXT PRIMARY KEY);
CREATE TABLE content(scope TEXT NOT NULL, hash TEXT NOT NULL, data BLOB NOT NULL, PRIMARY KEY(scope,hash));
CREATE TABLE counters(scope TEXT NOT NULL, run_id TEXT NOT NULL, seq INTEGER NOT NULL, PRIMARY KEY(scope,run_id));
CREATE TABLE calls(scope TEXT NOT NULL, run_id TEXT NOT NULL, seq INTEGER NOT NULL, attempt INTEGER NOT NULL, state TEXT NOT NULL, started BLOB NOT NULL, completion BLOB, PRIMARY KEY(scope,run_id,seq,attempt));
CREATE TABLE events(id INTEGER PRIMARY KEY AUTOINCREMENT, run_id TEXT NOT NULL, event_key TEXT NOT NULL, body BLOB NOT NULL, UNIQUE(run_id,event_key));
PRAGMA user_version=1;
`

// Create exclusively creates a new private capture. Existing captures are never reused.
func Create(ctx context.Context, dir string) (*Store, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := createDirectory(filepath.Clean(dir)); err != nil {
		return nil, err
	}
	if err := createDatabaseFile(dir); err != nil {
		return nil, err
	}
	s, err := connect(ctx, dir)
	if err != nil {
		return nil, err
	}
	if _, err := s.db.ExecContext(ctx, schema); err != nil {
		return nil, errors.Join(err, s.Close())
	}
	if err := syncDir(dir); err != nil {
		return nil, errors.Join(err, s.Close())
	}
	return s, nil
}

// Open reopens an existing capture and rejects unsupported or corrupt databases.
func Open(ctx context.Context, dir string) (*Store, error) {
	s, err := connect(ctx, dir)
	if err != nil {
		return nil, err
	}
	var version int
	var integrity string
	if err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return nil, errors.Join(err, s.Close())
	}
	if version != 1 {
		return nil, errors.Join(fmt.Errorf("local evidence: unsupported database version %d", version), s.Close())
	}
	if err := s.db.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&integrity); err != nil {
		return nil, errors.Join(err, s.Close())
	}
	if integrity != "ok" {
		return nil, errors.Join(fmt.Errorf("local evidence: corrupt database: %s", integrity), s.Close())
	}
	return s, nil
}

// Close releases the database after its owner has stopped evidence producers.
func (s *Store) Close() error {
	return s.db.Close()
}

// Roots lists every recorded root scope in deterministic order.
func (s *Store) Roots(ctx context.Context) ([]string, error) {
	return s.strings(ctx, "SELECT scope FROM roots UNION SELECT scope FROM content UNION SELECT scope FROM calls UNION SELECT scope FROM counters ORDER BY scope")
}

// RegisterRoot records discovery identity before starting a harness workflow.
func (s *Store) RegisterRoot(ctx context.Context, scope string) error {
	if scope == "" {
		return errors.New("local evidence: scope is required")
	}
	_, err := s.db.ExecContext(ctx, "INSERT INTO roots(scope) VALUES(?) ON CONFLICT DO NOTHING", scope)
	return err
}

// RunIDs lists call and counter runs, including runs without a committed child link.
func (s *Store) RunIDs(ctx context.Context, scope string) ([]string, error) {
	return s.strings(ctx, "SELECT run_id FROM calls WHERE scope=? UNION SELECT run_id FROM counters WHERE scope=? ORDER BY run_id", scope, scope)
}

// createDirectory syncs each newly created path entry before capture can start.
func createDirectory(dir string) (err error) {
	info, err := os.Stat(dir)
	if err == nil {
		if !info.IsDir() {
			return fmt.Errorf("local evidence: %s is not a directory", dir)
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	parent := filepath.Dir(dir)
	if err := createDirectory(parent); err != nil {
		return err
	}
	root, err := os.OpenRoot(parent)
	if err != nil {
		return err
	}
	defer func() {
		err = errors.Join(err, root.Close())
	}()
	if err := root.Mkdir(filepath.Base(dir), 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	return syncDir(parent)
}

// createDatabaseFile makes ownership exclusive before SQLite opens the capture.
func createDatabaseFile(dir string) (err error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer func() {
		err = errors.Join(err, root.Close())
	}()
	f, err := root.OpenFile(FileName, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	return errors.Join(f.Sync(), f.Close(), syncDir(dir))
}

func connect(ctx context.Context, dir string) (*Store, error) {
	path, err := filepath.Abs(filepath.Join(dir, FileName))
	if err != nil {
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: path}
	query := url.Values{"mode": {"rw"}, "_txlock": {"immediate"}, "_pragma": {"busy_timeout(5000)", "journal_mode(WAL)", "synchronous(FULL)"}}
	u.RawQuery = query.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := db.PingContext(ctx); err != nil {
		return nil, errors.Join(err, db.Close())
	}
	return &Store{db: db}, nil
}

func syncDir(dir string) error {
	f, err := os.OpenInRoot(dir, ".")
	if err != nil {
		return err
	}
	return errors.Join(f.Sync(), f.Close())
}

func (s *Store) strings(ctx context.Context, query string, args ...any) (values []string, err error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() {
		err = errors.Join(err, rows.Close())
	}()
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}
