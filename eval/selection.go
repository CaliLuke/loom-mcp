package eval

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// Selection combines selectors with OR within each kind and AND across kinds.
// Items may be bare item IDs or qualified scenario/item IDs.
type Selection struct {
	Variants, Scenarios, Tags, Items []string
	// Keys selects exact expanded attempts. Nil uses ordinary selectors; a
	// non-nil slice must be nonempty and cannot be mixed with other selectors.
	Keys []AttemptKey
}

type plannedAttempt struct {
	key      AttemptKey
	variant  Variant
	scenario Scenario
	item     Item
}

// Plan validates the entire configuration and returns keys in declaration order.
func Plan(cfg Config) ([]AttemptKey, error) {
	plan, err := buildPlan(cfg)
	if err != nil {
		return nil, err
	}
	keys := make([]AttemptKey, len(plan))
	for i, a := range plan {
		keys[i] = a.key
	}
	return keys, nil
}

func buildPlan(cfg Config) ([]plannedAttempt, error) {
	variants := cfg.Variants
	if len(variants) == 0 {
		variants = []Variant{{ID: DefaultVariant}}
	}
	problems := validateConfig(cfg, variants)
	var full []plannedAttempt
	for _, v := range variants {
		v.Policy = clonePolicy(v.Policy)
		for _, s := range cfg.Scenarios {
			for _, item := range s.Items.Items() {
				full = append(full, plannedAttempt{variant: v, scenario: s, item: item})
			}
		}
	}
	problems = append(problems, validateSelection(cfg.Select, full)...)
	repeats := cfg.Repeats
	if repeats == 0 {
		repeats = 1
	}
	var selected []plannedAttempt
	for _, a := range full {
		if !matchesSelection(cfg.Select, a) {
			continue
		}
		for n := 1; n <= repeats; n++ {
			a.key = AttemptKey{Variant: a.variant.ID, Scenario: a.scenario.ID, Item: a.item.ID, Repeat: n}
			selected = append(selected, a)
		}
	}
	if cfg.Select.Keys != nil {
		var exactProblems []error
		selected, exactProblems = selectExact(cfg.Select, selected)
		problems = append(problems, exactProblems...)
	}
	if len(selected) == 0 {
		problems = append(problems, errors.New("eval: empty plan"))
	}
	if err := errors.Join(problems...); err != nil {
		return nil, err
	}
	return selected, nil
}

func validateConfig(cfg Config, variants []Variant) []error {
	var problems []error
	if (cfg.Harness == nil) == (cfg.AttemptHarness == nil) {
		problems = append(problems, errors.New("eval: exactly one of Harness and AttemptHarness is required"))
	}
	if len(cfg.Scenarios) == 0 {
		problems = append(problems, errors.New("eval: no scenarios"))
	}
	if cfg.JudgeTimeout < 0 {
		problems = append(problems, errors.New("eval: JudgeTimeout must be non-negative"))
	}
	problems = append(problems, Validate(cfg.Scenarios...))
	problems = append(problems, validateVariants(variants)...)
	for _, field := range []struct {
		name  string
		value int
	}{{"Repeats", cfg.Repeats}, {"Concurrency", cfg.Concurrency}, {"MaxTokens", cfg.MaxTokens}} {
		if field.value < 0 {
			problems = append(problems, fmt.Errorf("eval: %s must be non-negative", field.name))
		}
	}
	return problems
}

// validateSelection checks each selector both against the full matrix and its intersection.
func validateSelection(sel Selection, full []plannedAttempt) []error {
	var problems []error
	for kind, values := range [][]string{sel.Variants, sel.Scenarios, sel.Tags, sel.Items} {
		seen := make(map[string]bool, len(values))
		for _, value := range values {
			if value == "" {
				problems = append(problems, fmt.Errorf("eval: empty %s selector", selectorName(kind)))
				continue
			}
			if seen[value] {
				problems = append(problems, fmt.Errorf("eval: duplicate %s selector %q", selectorName(kind), value))
			}
			seen[value] = true
			known, matched := false, false
			for _, a := range full {
				if matchesSelector(kind, value, a) {
					known = true
					matched = matched || matchesSelection(sel, a)
				}
			}
			if !known {
				problems = append(problems, fmt.Errorf("eval: unknown %s selector %q", selectorName(kind), value))
			} else if !matched {
				problems = append(problems, fmt.Errorf("eval: %s selector %q matches nothing with other selectors", selectorName(kind), value))
			}
		}
	}
	return problems
}

func matchesSelection(sel Selection, a plannedAttempt) bool {
	for kind, values := range [][]string{sel.Variants, sel.Scenarios, sel.Tags, sel.Items} {
		if len(values) == 0 {
			continue
		}
		matched := false
		for _, v := range values {
			matched = matched || matchesSelector(kind, v, a)
		}
		if !matched {
			return false
		}
	}
	return true
}
func matchesSelector(kind int, value string, a plannedAttempt) bool {
	switch kind {
	case 0:
		return value == a.variant.ID
	case 1:
		return value == a.scenario.ID
	case 2:
		return slices.Contains(a.scenario.Tags, value)
	case 3:
		scenario, item, qualified := strings.Cut(value, "/")
		if qualified {
			return scenario == a.scenario.ID && item == a.item.ID
		}
		return value == a.item.ID
	default:
		panic("eval: invalid selector kind")
	}
}
func selectorName(kind int) string {
	return []string{"variant", "scenario", "tag", "item"}[kind]
}

// selectExact validates keys against the fully expanded matrix and retains its
// declaration order. A missing key never widens the requested work.
func selectExact(sel Selection, full []plannedAttempt) ([]plannedAttempt, []error) {
	var problems []error
	if len(sel.Keys) == 0 {
		problems = append(problems, errors.New("eval: exact keys must be nonempty"))
	}
	if len(sel.Variants)+len(sel.Scenarios)+len(sel.Tags)+len(sel.Items) > 0 {
		problems = append(problems, errors.New("eval: exact keys cannot mix with ordinary selectors"))
	}
	wanted := make(map[AttemptKey]bool, len(sel.Keys))
	known := make(map[AttemptKey]bool, len(full))
	for _, a := range full {
		known[a.key] = true
	}
	for _, key := range sel.Keys {
		if wanted[key] {
			problems = append(problems, fmt.Errorf("eval: duplicate exact key %s", key.String()))
		}
		if !known[key] {
			problems = append(problems, fmt.Errorf("eval: unknown exact key %s", key.String()))
		}
		wanted[key] = true
	}
	selected := make([]plannedAttempt, 0, len(sel.Keys))
	for _, a := range full {
		if wanted[a.key] {
			selected = append(selected, a)
		}
	}
	return selected, problems
}
