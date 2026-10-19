package eval

import (
	"encoding/json/jsontext"
	"fmt"
	"io"
)

type showExperiment struct {
	ExecutionKind ExecutionKind `json:"execution_kind,omitempty"`
	ID            string        `json:"id"`
	Label         string        `json:"label"`
	Parent        string        `json:"parent"`
	Status        string        `json:"status"`
	Stop          StopReason    `json:"stop"`
	Source        VCS           `json:"source"`
	Versions      Versions      `json:"versions"`
	Judge         *JudgeInfo    `json:"judge,omitempty"`
}

type showRun struct {
	StoredRun
	Export          jsontext.Value `json:"export,omitempty"`
	Trajectory      jsontext.Value `json:"trajectory,omitempty"`
	TrajectoryError string         `json:"trajectory_error,omitempty"`
}

type showAttempt struct {
	StoredAttempt
	Runs            []showRun `json:"runs"`
	TrajectoryError string    `json:"trajectory_error,omitempty"`
}

type summaryFailure struct {
	Ref     string  `json:"ref"`
	Key     string  `json:"key"`
	Verdict Verdict `json:"verdict"`
	Reason  string  `json:"reason"`
}

// ShowJSON embeds decoded trajectories, adding raw exports only when requested.
func ShowJSON(w io.Writer, e *Experiment, a *StoredAttempt, opts ShowOptions) error {
	if e == nil || a == nil {
		return fmt.Errorf("eval: show requires an experiment and attempt")
	}
	attempt := showAttempt{StoredAttempt: *a}
	for _, run := range a.Runs {
		entry := showRun{StoredRun: run}
		if opts.Export {
			entry.Export = run.Export
		}
		tr, err := run.Trajectory()
		if err == nil {
			entry.Trajectory, err = tr.Marshal()
		}
		if err != nil {
			entry.TrajectoryError = err.Error()
		}
		attempt.Runs = append(attempt.Runs, entry)
	}
	if len(a.Runs) == 0 && a.StorageError != "" {
		attempt.TrajectoryError = a.StorageError
	}
	h := e.Header
	return writeReportJSON(w, struct {
		Format     string         `json:"format"`
		Version    int            `json:"version"`
		Experiment showExperiment `json:"experiment"`
		Attempt    showAttempt    `json:"attempt"`
	}{Format: "loom-mcp.eval.show", Version: reportVersion(h), Experiment: showExperiment{ExecutionKind: h.ExecutionKind, ID: h.ID, Label: h.Label, Parent: h.Parent, Status: h.Status, Stop: h.Stop, Source: h.Source, Versions: h.Versions, Judge: h.Judge}, Attempt: attempt})
}

// SummaryJSON writes the header, failing-first groups and full failing references.
func SummaryJSON(w io.Writer, e *Experiment) error {
	if e == nil {
		return fmt.Errorf("eval: summary requires an experiment")
	}
	quality := ""
	if e.Header.ExecutionKind == ExecutionProbe {
		quality = probeQualityNotAssessed
	}
	groups := summaryGroups(e)
	var failures []summaryFailure
	for _, g := range groups {
		for _, a := range g.attempts {
			if reportFailure(a) {
				failures = append(failures, summaryFailure{Ref: fmt.Sprintf("%s/#%d", e.Header.ID, a.Index), Key: a.AttemptKey.String(), Verdict: a.Verdict, Reason: attemptReason(a)})
			}
		}
	}
	return writeReportJSON(w, struct {
		Quality         string           `json:"quality,omitempty"`
		Format          string           `json:"format"`
		Version         int              `json:"version"`
		Experiment      ExperimentHeader `json:"experiment"`
		Complete        bool             `json:"complete"`
		Groups          []reportGroup    `json:"groups"`
		FailingAttempts []summaryFailure `json:"failing_attempts"`
	}{Quality: quality, Format: "loom-mcp.eval.summary", Version: reportVersion(e.Header), Experiment: e.Header, Complete: e.Complete(), Groups: groups, FailingAttempts: failures})
}

func writeReportJSON(w io.Writer, value any) error {
	// Keep the same deterministic codec and raw-object ordering as persisted records.
	data, err := encodeStore(value)
	if err != nil {
		return err
	}
	_, err = w.Write(append(data, '\n'))
	return err
}
