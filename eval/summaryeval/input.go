package summaryeval

import (
	"fmt"
	"os"

	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
)

// Selection restricts immutable cuts without recomputing them.
type Selection struct {
	From, Export, Run string
	Attempts          []string
	All               bool
	Step, MaxCuts     int
}

// Read selects T13 attempts or a raw self-contained export, never the original run store.
func Read(dir string, s Selection) ([]Cut, Source, error) {
	if (s.From == "") == (s.Export == "") {
		return nil, Source{}, fmt.Errorf("%w: exactly one of --from or --export is required", ErrUsage)
	}
	if s.Step < 0 || s.MaxCuts < 0 || (s.Export != "" && len(s.Attempts) > 0) {
		return nil, Source{}, fmt.Errorf("%w: invalid cut selection", ErrUsage)
	}
	records, source, err := readRecords(dir, s)
	if err != nil {
		return nil, source, err
	}
	var cuts []Cut
	for _, rec := range records {
		for _, run := range rec.Runs {
			if s.Run != "" && s.Run != run.RunID {
				continue
			}
			if run.ContextFingerprint == "" || run.ContextSettings == nil || run.ContextSettings.Prompt.Template == "" {
				return nil, source, fmt.Errorf("recorded before K17: no pinned summary template")
			}
		}
		for _, cut := range Cuts(rec) {
			if s.Run != "" && cut.RunID != s.Run || s.Step > 0 && cut.Step != s.Step || !s.All && cut.Origin == originReceipt {
				continue
			}
			cuts = append(cuts, cut)
		}
	}
	if s.MaxCuts > 0 && len(cuts) > s.MaxCuts {
		cuts = cuts[:s.MaxCuts]
	}
	return cuts, source, nil
}

func readRecords(dir string, s Selection) ([]*runrecord.RunRecord, Source, error) {
	var records []*runrecord.RunRecord
	var source Source
	if s.Export != "" {
		b, err := os.ReadFile(s.Export)
		if err != nil {
			return nil, source, err
		}
		rec, err := runrecord.FromExport(b)
		if err != nil {
			return nil, source, err
		}
		records = append(records, rec)
		source.ExportSHA256 = string(contentHash(b))
	} else {
		store, err := eval.Open(dir)
		if err != nil {
			return nil, source, err
		}
		id, err := store.Resolve(s.From)
		if err != nil {
			return nil, source, err
		}
		experiment, err := store.Load(id)
		if err != nil {
			return nil, source, err
		}
		source.Experiment = id
		attempts, err := selectedAttempts(experiment, s.Attempts)
		if err != nil {
			return nil, source, err
		}
		for _, a := range attempts {
			source.Attempts = append(source.Attempts, a.Key)
			for _, run := range a.Runs {
				rec, err := run.Record()
				if err != nil {
					return nil, source, err
				}
				records = append(records, rec)
			}
		}
	}
	return records, source, nil
}

func selectedAttempts(experiment *eval.Experiment, refs []string) ([]*eval.StoredAttempt, error) {
	attempts := experiment.Attempts
	if len(refs) > 0 {
		attempts = nil
		seen := map[string]bool{}
		for _, ref := range refs {
			a, err := experiment.Find(ref)
			if err != nil {
				return nil, err
			}
			if !seen[a.Key] {
				attempts = append(attempts, a)
				seen[a.Key] = true
			}
		}
	}
	return attempts, nil
}
