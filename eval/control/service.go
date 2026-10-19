package control

import (
	"context"
	"encoding/hex"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/CaliLuke/loom-mcp/v2/eval/candidate"
	"github.com/CaliLuke/loom-mcp/v2/eval/inspect"
)

// New binds immutable host stores, capabilities and verified authorization.
func New(cfg Config) (*Service, error) {
	if !identity(cfg.Scope) || cfg.Candidates == nil || cfg.Experiments == nil || cfg.Authorize == nil || cfg.Limits.MaxReadBytes <= 0 || cfg.Limits.MaxPageEntries <= 0 || len(cfg.Operations) == 0 {
		return nil, invalid("")
	}
	if cfg.Role != inspect.Editor && cfg.Role != inspect.Evaluator && cfg.Role != inspect.Selector && cfg.Role != inspect.Solver {
		return nil, invalid("")
	}
	s := &Service{cfg: cfg, tasks: make(map[string]TaskBinding), ops: make(map[Operation]bool)}
	if err := s.configureOperations(cfg); err != nil {
		return nil, err
	}
	cfg.Operations = slices.Clone(cfg.Operations)
	cfg.Tasks = slices.Clone(cfg.Tasks)
	for i, binding := range cfg.Tasks {
		owned, err := s.configureTask(binding)
		if err != nil {
			return nil, err
		}
		cfg.Tasks[i] = owned
		s.tasks[owned.Task.ID] = owned
	}
	s.cfg = cfg
	return s, nil
}

// DefaultLimits returns 64 KiB per read and 100 entries per page.
func DefaultLimits() Limits {
	return Limits{MaxReadBytes: 65536, MaxPageEntries: 100}
}

// Discover returns authorized capability metadata without selector lineage.
func (s *Service) Discover(ctx context.Context) (Discovery, error) {
	if err := s.authorizeRequest(ctx, DiscoverOperation, struct{}{}); err != nil {
		return Discovery{}, err
	}
	out := Discovery{Operations: slices.Clone(s.cfg.Operations)}
	if s.cfg.Role == inspect.Editor || s.cfg.Role == inspect.Evaluator {
		d := s.cfg.Candidates.Discover()
		out.Surface = &d
		for _, b := range s.cfg.Tasks {
			t := b.Task
			t.Modes = slices.Clone(t.Modes)
			t.EvaluationPlans = slices.Clone(t.EvaluationPlans)
			out.Tasks = append(out.Tasks, t)
		}
	}
	return out, nil
}

func knownOperation(op Operation) bool {
	switch op {
	case PreviewCopyOperation, CopyOperation, DiscoverOperation, CreateOperation, GetOperation, ReadOperation, EditOperation, ValidateOperation, FreezeOperation, SubmitOperation, StatusOperation, CancelOperation, ListEvidenceOperation, ReadEvidenceOperation, CompareOperation:
		return true
	default:
		return false
	}
}
func needsExecutor(op Operation) bool {
	return op == SubmitOperation || op == StatusOperation || op == CancelOperation || op == CompareOperation || op == ListEvidenceOperation || op == ReadEvidenceOperation
}
func identity(s string) bool {
	if len(s) == 0 || len(s) > 256 || !utf8.ValidString(s) || strings.TrimSpace(s) != s {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) || r == '/' || r == '\\' {
			return false
		}
	}
	return true
}
func digest(s string, n int) bool {
	if len(s) != n || strings.ToLower(s) != s {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}
func bindingValid(b candidate.Binding) bool {
	return identity(b.ID) && identity(b.Version)
}
func bindingsEqual(a, b []candidate.Binding) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[candidate.Binding]bool{}
	for _, v := range a {
		if !bindingValid(v) || seen[v] {
			return false
		}
		seen[v] = true
	}
	for _, v := range b {
		if !seen[v] {
			return false
		}
	}
	return true
}
func surfaceBindings(surface candidate.Surface) []candidate.Binding {
	var out []candidate.Binding
	for _, slot := range surface.Slots {
		if !slices.Contains(out, slot.Binding) {
			out = append(out, slot.Binding)
		}
	}
	return out
}
func (s *Service) authorize(ctx context.Context, op Operation) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !s.ops[op] {
		return NewError(Unsupported, "", "", "")
	}
	if err := s.cfg.Authorize(ctx, op); err != nil {
		return NewError(NotFound, "", "", "")
	}
	return nil
}
