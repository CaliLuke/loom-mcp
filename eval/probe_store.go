package eval

import (
	"context"
	"errors"
)

// ErrProbeRerunUnsupported rejects evaluation rerun overlays for operational probes.
var ErrProbeRerunUnsupported = errors.New("eval: probe rerun unsupported; submit a new probe")

// BeginProbe admits one explicit operational probe without evaluation assertions.
func (s *Store) BeginProbe(ctx context.Context, cfg ProbeConfig, meta ExperimentMeta) (*Writer, error) {
	if meta.Parent != "" {
		return nil, ErrProbeRerunUnsupported
	}
	plan, err := preparedPlanForProbe(cfg)
	if err != nil {
		return nil, err
	}
	h := ExperimentHeader{Format: experimentFormat, Version: 2, ExecutionKind: ExecutionProbe, Label: meta.Label, Status: statusRunning, Config: plan.Config, Scenarios: plan.Scenarios, Variants: plan.Variants, Planned: 1}
	return s.begin(ctx, h, plan.Keys, meta)
}
