package evalcli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/CaliLuke/loom-mcp/v2/eval"
)

func check(cfg Config, f flags, ref string) error {
	store, err := openStore(cfg, f)
	if err != nil {
		return err
	}
	experiment, err := loadRef(store, ref)
	if err != nil {
		return err
	}
	result := eval.CheckQualityStrict(experiment)
	if f.json {
		err = json.NewEncoder(cfg.Stdout).Encode(result)
	} else {
		decision := "rejected"
		if result.Passed {
			decision = "passed"
		}
		if result.ExecutionKind == eval.ExecutionProbe {
			_, err = fmt.Fprintf(cfg.Stdout, "strict check rejected: quality not_assessed (%d probes; planned %d)\n", result.Counts.Probes, result.Planned)
		} else {
			_, err = fmt.Fprintf(cfg.Stdout, "strict check %s: %d/%d passed (planned %d; %d failed, %d error, %d pending, %d not run)\n",
				decision, result.Counts.Passed, result.Attempts, result.Planned,
				result.Counts.Failed, result.Counts.Error, result.Counts.Pending, result.Counts.NotRun)
		}
		if err == nil && len(result.Reasons) > 0 {
			_, err = fmt.Fprintf(cfg.Stdout, "Reasons: %s\n", strings.Join(result.Reasons, ", "))
		}
	}
	if err != nil {
		return err
	}
	if !result.Passed {
		return &commandError{code: 3, err: fmt.Errorf("quality check rejected")}
	}
	return nil
}
