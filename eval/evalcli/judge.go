package evalcli

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/eval/judge"
	"github.com/CaliLuke/loom-mcp/v2/eval/judging"
)

// newJudge constructs judging before Begin, with an opt-out for disabled live models.
func newJudge(cfg Config, f flags, dir string) (eval.OutcomeJudge, error) {
	if cfg.NewJudge == nil || f.noJudge {
		return nil, nil
	}
	cache, err := judge.NewDirCache(filepath.Join(dir, "_calibration"))
	if err != nil {
		return nil, err
	}
	j, err := cfg.NewJudge(cache)
	if errors.Is(err, agenttest.ErrLiveModelDisabled) {
		_, noticeErr := fmt.Fprintln(cfg.Stderr, "judge disabled: set LOOM_LIVE_MODEL=1 to enable live judging; gating claims remain pending")
		return nil, noticeErr
	}
	if err != nil {
		return nil, err
	}
	return judging.New(judging.Config{Judge: j})
}
