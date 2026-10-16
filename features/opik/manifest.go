package opik

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/google/uuid"
)

type exportManifest struct {
	Version int    `json:"version"`
	Target  string `json:"target"`
	Policy  string `json:"policy"`
	Source  string `json:"source"`
	Epoch   int64  `json:"epoch"`
}

// publishManifest exclusively publishes a fully synced seed before network use.
func publishManifest(dir, path string, data []byte) (err error) {
	file, err := os.CreateTemp(dir, ".manifest-*")
	if err != nil {
		return err
	}
	defer func() {
		err = errors.Join(err, os.Remove(file.Name()))
	}()
	if _, err = file.Write(data); err != nil {
		return errors.Join(err, file.Close())
	}
	if err = file.Sync(); err != nil {
		return errors.Join(err, file.Close())
	}
	if err = file.Close(); err != nil {
		return err
	}
	if err = os.Link(file.Name(), path); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	return syncDirectory(dir)
}

func syncDirectory(path string) error {
	directory, err := os.Open(filepath.Clean(path))
	if err != nil {
		return err
	}
	return errors.Join(directory.Sync(), directory.Close())
}

// manifest publishes the identity seed before the first network operation.
// Exclusive initial publication ensures concurrent processes cannot pick seeds
// with different trace identities for the same source/target/policy.
func (c *Client) manifest(experiment *eval.Experiment) (*exportManifest, error) {
	target := strings.TrimRight(c.config.Endpoint, "/") + "\n" + c.config.Workspace + "\n" + c.config.Project
	digest := sha256.Sum256([]byte(target + "\n" + c.config.Policy.Name))
	dir := filepath.Join(experiment.Dir, "opik")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	if err := syncDirectory(experiment.Dir); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, hex.EncodeToString(digest[:])+".json")
	expected := exportManifest{Version: 1, Target: target, Policy: c.config.Policy.Name, Source: experiment.Header.ID, Epoch: time.Now().UnixMilli()}
	data, err := os.ReadFile(filepath.Clean(path))
	if errors.Is(err, os.ErrNotExist) {
		encoded, encodeErr := json.Marshal(expected)
		if encodeErr != nil {
			return nil, encodeErr
		}
		if err := publishManifest(dir, path, encoded); err != nil {
			return nil, err
		}
		data, err = os.ReadFile(filepath.Clean(path))
	}
	if err != nil {
		return nil, err
	}
	var stored exportManifest
	if err := json.Unmarshal(data, &stored, json.RejectUnknownMembers(true)); err != nil {
		return nil, fmt.Errorf("opik manifest: %w", err)
	}
	if stored.Version != 1 || stored.Target != target || stored.Policy != expected.Policy || stored.Source != expected.Source || stored.Epoch <= 0 {
		return nil, fmt.Errorf("opik: manifest identity mismatch")
	}
	return &stored, nil
}

// id combines a saved current ingestion epoch with deterministic identity bits.
// Original OTel IDs remain metadata; they are never reinterpreted as UUIDv7.
func (m *exportManifest) id(kind, key string) string {
	digest := sha256.Sum256([]byte(m.Target + "\n" + m.Policy + "\n" + m.Source + "\n" + kind + "\n" + key))
	var id uuid.UUID
	copy(id[:], digest[:16])
	epoch := m.Epoch
	for i := 5; i >= 0; i-- {
		id[i] = byte(epoch & 0xff)
		epoch >>= 8
	}
	id[6] = (id[6] & 0x0f) | 0x70
	id[8] = (id[8] & 0x3f) | 0x80
	return id.String()
}
