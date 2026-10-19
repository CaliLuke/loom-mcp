package control

import (
	"slices"

	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/eval/inspect"
)

func validateTaskModes(t Task) error {
	modes := map[eval.ExecutionKind]bool{}
	for _, kind := range t.Modes {
		if (kind != eval.ExecutionProbe && kind != eval.ExecutionEvaluation) || modes[kind] {
			return invalid(jsonKind)
		}
		modes[kind] = true
	}
	if modes[eval.ExecutionEvaluation] != (len(t.EvaluationPlans) > 0) {
		return invalid(jsonEvaluationPlan)
	}
	plans := map[string]bool{}
	for _, plan := range t.EvaluationPlans {
		if !identity(plan) || plans[plan] {
			return invalid(jsonEvaluationPlan)
		}
		plans[plan] = true
	}
	return nil
}

// configureOperations validates a fixed grant before any service is published.
func (s *Service) configureOperations(cfg Config) error {
	for _, op := range cfg.Operations {
		if !knownOperation(op) || s.ops[op] {
			return invalid(jsonOperation)
		}
		if (cfg.Role == inspect.Selector || cfg.Role == inspect.Solver) && op != DiscoverOperation && op != StatusOperation && op != ListEvidenceOperation && op != ReadEvidenceOperation {
			return invalid(jsonOperation)
		}
		if (needsExecutor(op) && cfg.Executor == nil) || ((op == ListEvidenceOperation || op == ReadEvidenceOperation) && cfg.Evidence == nil) {
			return invalid(jsonOperation)
		}
		s.ops[op] = true
	}
	return nil
}

// configureTask owns declared task/binding metadata and rejects ambiguous modes.
func (s *Service) configureTask(b TaskBinding) (TaskBinding, error) {
	if !identity(b.Task.ID) || !identity(b.Snapshot) || !bindingValid(b.HostBinding) || len(b.Task.Modes) == 0 || s.tasks[b.Task.ID].Task.ID != "" {
		return TaskBinding{}, invalid("task_id")
	}
	if err := validateTaskModes(b.Task); err != nil {
		return TaskBinding{}, err
	}
	if !bindingsEqual(b.ComponentBindings, surfaceBindings(s.cfg.Candidates.Discover().Surface)) {
		return TaskBinding{}, invalid(jsonSlot)
	}
	b.Task.Modes = slices.Clone(b.Task.Modes)
	b.Task.EvaluationPlans = slices.Clone(b.Task.EvaluationPlans)
	b.ComponentBindings = slices.Clone(b.ComponentBindings)
	return b, nil
}
