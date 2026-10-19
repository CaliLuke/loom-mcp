package eval

import (
	"context"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent"
)

// ProbeConfig declares one prepared operational attempt without quality assessment.
// Key is complete and agrees with Item and Variant. The caller begins and finishes
// storage; Probe requires its matching PreparedWriter before acquiring resources.
type ProbeConfig struct {
	Key            AttemptKey
	Item           Item
	Variant        Variant
	Agent          agent.Ident
	Timeout        time.Duration
	PrepareAttempt PrepareAttemptFunc
	PreparedWriter *Writer
	PrepareTimeout time.Duration
	CleanupTimeout time.Duration
	OnAttempt      func(*AttemptRecord)
}

// Probe executes one prepared attempt, retaining evidence without assessing quality.
func Probe(ctx context.Context, cfg ProbeConfig) (*Report, error) {
	plan, err := preparedPlanForProbe(cfg)
	if err != nil {
		return nil, err
	}
	return runPlan(ctx, probeRunnerConfig(cfg), []plannedAttempt{probeAttempt(cfg)}, plan)
}
