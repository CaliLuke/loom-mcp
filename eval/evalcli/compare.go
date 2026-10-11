package evalcli

import (
	"errors"
	"fmt"

	"github.com/CaliLuke/loom-mcp/v2/eval"
)

func compare(cfg Config, f flags, refs []string) error {
	store, err := openStore(cfg, f)
	if err != nil {
		return err
	}
	base, err := loadRef(store, refs[0])
	if err != nil {
		return err
	}
	var comparisons []*eval.Comparison
	if len(f.variants) > 0 {
		if err := validateVariants(base, f.variants); err != nil {
			return usage(err)
		}
		comparisons, err = eval.CompareVariants(base, f.variants)
	} else {
		var cand *eval.Experiment
		cand, err = loadRef(store, refs[1])
		if err == nil {
			comparisons, err = compareExperiments(cfg, f, store, base, cand)
		}
	}
	if err != nil {
		return err
	}
	if f.json {
		return eval.WriteComparisonsJSON(cfg.Stdout, comparisons)
	}
	return eval.WriteComparisons(cfg.Stdout, comparisons)
}

func runComparison(cfg Config, f flags, store *eval.Store, cand *eval.Experiment) ([]*eval.Comparison, error) {
	if f.baseline == "" {
		return nil, nil
	}
	ref := f.baseline
	if ref == "last" {
		var pairs [][2]string
		for _, scenario := range cand.Header.Scenarios {
			for _, item := range scenario.Items {
				pairs = append(pairs, [2]string{scenario.ID, item})
			}
		}
		header, err := store.Last(pairs, cand.Header.ID)
		if err != nil {
			return nil, err
		}
		if header == nil {
			_, err := fmt.Fprintln(cfg.Stderr, "no overlapping experiment to compare")
			return nil, err
		}
		ref = header.ID
	}
	base, err := loadRef(store, ref)
	if err != nil {
		return nil, err
	}
	return compareExperiments(cfg, f, store, base, cand)
}

func compareExperiments(cfg Config, f flags, store *eval.Store, base, cand *eval.Experiment) ([]*eval.Comparison, error) {
	if base.Header.Stop == eval.StopLiveModelDisabled {
		if _, err := fmt.Fprintf(cfg.Stderr, "baseline %s stopped with live_model_disabled\n", base.Header.ID); err != nil {
			return nil, err
		}
	}
	var opts eval.CompareOptions
	if f.mergeParent {
		if cand.Header.Parent == "" {
			return nil, usage(errors.New("--merge-parent requires a candidate with a parent"))
		}
		var err error
		opts.Parent, err = store.Load(cand.Header.Parent)
		if err != nil {
			return nil, err
		}
	}
	return eval.CompareExperiments(base, cand, opts)
}

func validateVariants(e *eval.Experiment, variants []string) error {
	known := make(map[string]bool)
	for _, v := range e.Header.Variants {
		known[v.ID] = true
	}
	seen := make(map[string]bool)
	for _, id := range variants {
		if !known[id] || seen[id] {
			return fmt.Errorf("unknown or duplicate variant %q", id)
		}
		seen[id] = true
	}
	return nil
}
