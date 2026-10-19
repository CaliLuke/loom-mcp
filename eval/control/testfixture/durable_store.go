package testfixture

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/CaliLuke/loom-mcp/v2/eval/control"
)

func submissionIdentity(submission control.Submission) (string, string, error) {
	keyBytes, err := json.Marshal([]string{submission.Scope, submission.RequestKey})
	if err != nil {
		return "", "", err
	}
	data, err := json.Marshal(submission)
	if err != nil {
		return "", "", err
	}
	key := sha256.Sum256(keyBytes)
	identity := sha256.Sum256(data)
	return hex.EncodeToString(key[:]), hex.EncodeToString(identity[:]), nil
}

func randomToken() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}

func cloneJob(job control.JobRecord) control.JobRecord {
	if job.Failure != nil {
		failure := *job.Failure
		job.Failure = &failure
	}
	return job
}

// validateRecord rejects partial, mismatched and impossible state instead of guessing recovery.
func validateRecord(record diskRecord, filename string) error {
	expected, err := recordIntegrity(record)
	if err != nil {
		return err
	}
	if record.Integrity != expected {
		return errors.New("durable job integrity mismatch")
	}
	key, identity, err := submissionIdentity(record.Job.Submission)
	if err != nil {
		return err
	}
	if record.Version != 1 || record.Key != key || filename != key+".json" || record.Identity != identity || len(record.Job.ID) != 32 || record.Job.Submission.Scope == "" || record.Job.Submission.RequestKey == "" {
		return errors.New("invalid durable job identity")
	}
	switch record.Job.State {
	case control.Running, control.CancelRequested:
		if record.Claim == "" {
			return errors.New("running durable job has no claim")
		}
	case control.Queued, control.Succeeded, control.Failed, control.Canceled, control.Interrupted:
		if record.Claim != "" {
			return errors.New("unowned durable job has a claim")
		}
	default:
		return errors.New("invalid durable job state")
	}
	return nil
}

// replaceSynced publishes only fully written bytes and syncs directory rename metadata.
func replaceSynced(dirName, name string, data []byte) (err error) {
	file, err := os.CreateTemp(dirName, ".job-*")
	if err != nil {
		return err
	}
	temporary := file.Name()
	defer func() {
		removeErr := os.Remove(temporary)
		if removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			err = errors.Join(err, removeErr)
		}
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
	if err = os.Rename(temporary, filepath.Join(dirName, name)); err != nil {
		return err
	}
	return syncDirectory(dirName)
}

func syncDirectory(name string) error {
	root, err := os.OpenRoot(name)
	if err != nil {
		return err
	}
	dir, err := root.Open(".")
	if err != nil {
		return errors.Join(err, root.Close())
	}
	if err = dir.Sync(); err != nil {
		return errors.Join(err, dir.Close(), root.Close())
	}
	return errors.Join(dir.Close(), root.Close())
}

func recordIntegrity(record diskRecord) (string, error) {
	record.Integrity = ""
	data, err := json.Marshal(record)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:]), nil
}

func (e *DurableExecutor) available(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if e.closed || e.fault != nil {
		return control.NewError(control.ExecutorUnavailable, "", "", "")
	}
	return nil
}

func (e *DurableExecutor) checkpoint(point CrashPoint) error {
	if e.cfg.Checkpoint != nil {
		return e.cfg.Checkpoint(point)
	}
	return nil
}

func (e *DurableExecutor) lookup(ctx context.Context, scope, id string) (diskRecord, error) {
	if err := e.available(ctx); err != nil {
		return diskRecord{}, err
	}
	record, exists := e.records[id]
	if !exists || record.Job.Submission.Scope != scope {
		return diskRecord{}, control.NewError(control.NotFound, "", "", "job_id")
	}
	return record, nil
}

// persist replaces the single key/job record and syncs its directory before publishing in memory.
func (e *DurableExecutor) persist(record diskRecord) (err error) {
	defer func() {
		if err != nil {
			e.fault = err
		}
	}()
	record.Integrity, err = recordIntegrity(record)
	if err != nil {
		return err
	}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	if err = replaceSynced(e.cfg.Dir, record.Key+".json", data); err != nil {
		return err
	}
	e.records[record.Job.ID] = record
	return nil
}

// loadRecords admits only complete records with matching scoped identity and integrity.
func (e *DurableExecutor) loadRecords() error {
	entries, err := os.ReadDir(e.cfg.Dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(e.cfg.Dir, entry.Name()))
		if err != nil {
			return err
		}
		var record diskRecord
		if err = json.Unmarshal(data, &record, json.RejectUnknownMembers(true)); err != nil {
			return fmt.Errorf("read durable job: %w", err)
		}
		if err = validateRecord(record, entry.Name()); err != nil {
			return err
		}
		if _, exists := e.records[record.Job.ID]; exists {
			return errors.New("duplicate durable job identity")
		}
		e.records[record.Job.ID] = record
	}
	return nil
}

func (e *DurableExecutor) interruptLostClaims() error {
	for _, record := range e.records {
		if record.Job.State != control.Running && record.Job.State != control.CancelRequested {
			continue
		}
		record.Job.State = control.Interrupted
		record.Job.Failure = control.NewError(control.OperationFailed, "", "", "")
		record.Claim = ""
		if err := e.persist(record); err != nil {
			return err
		}
	}
	return nil
}
