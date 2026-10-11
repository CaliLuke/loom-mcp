package evalcli

import (
	"strings"

	"github.com/CaliLuke/loom-mcp/v2/eval"
)

func show(cfg Config, f flags, ref string) error {
	store, err := openStore(cfg, f)
	if err != nil {
		return err
	}
	experimentRef, attemptRef, hasAttempt := strings.Cut(ref, "/")
	e, err := loadRef(store, experimentRef)
	if err != nil {
		return err
	}
	if !hasAttempt {
		if f.json {
			return eval.SummaryJSON(cfg.Stdout, e)
		}
		return eval.Summary(cfg.Stdout, e)
	}
	a, err := e.Find(attemptRef)
	if err != nil {
		return usage(err)
	}
	opts := eval.ShowOptions{Full: f.full, Export: f.export}
	if f.json {
		return eval.ShowJSON(cfg.Stdout, e, a, opts)
	}
	return eval.Show(cfg.Stdout, e, a, opts)
}
