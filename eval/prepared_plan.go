package eval

import "errors"

// validatePreparedConfig rejects ambiguous ownership without requiring a writer at Plan.
func validatePreparedConfig(cfg Config) []error {
	var problems []error
	if cfg.PrepareAttempt == nil {
		if cfg.PreparedWriter != nil || cfg.PrepareTimeout != 0 || cfg.CleanupTimeout != 0 {
			problems = append(problems, errors.New("eval: prepared writer and timeouts require PrepareAttempt"))
		}
		return problems
	}
	if cfg.PrepareTimeout <= 0 {
		problems = append(problems, errors.New("eval: PrepareTimeout must be positive"))
	}
	if cfg.CleanupTimeout <= 0 {
		problems = append(problems, errors.New("eval: CleanupTimeout must be positive"))
	}
	return problems
}

// preparedPlanForConfig normalizes admission facts without comparing opaque callbacks.
func preparedPlanForConfig(cfg Config, plan []plannedAttempt) preparedPlan {
	r := newReport(cfg, plan)
	p := preparedPlan{Kind: ExecutionEvaluation, Scenarios: r.Scenarios, Config: StoredConfig{Repeats: r.Repeats, Concurrency: r.Concurrency, MaxTokens: r.MaxTokens, Selection: r.Selection, PrepareTimeout: cfg.PrepareTimeout, CleanupTimeout: cfg.CleanupTimeout}}
	for _, a := range plan {
		p.Keys = append(p.Keys, a.key)
	}
	for _, v := range r.Variants {
		p.Variants = append(p.Variants, StoredVariant{ID: v.ID, Labels: v.Labels, Policy: v.Policy})
	}
	return p
}

// preparedPlanForProbe validates explicit operational admission without quality checks.
func preparedPlanForProbe(cfg ProbeConfig) (preparedPlan, error) {
	var problems []error
	key := cfg.Key
	if !idPattern.MatchString(key.Variant) || !idPattern.MatchString(key.Scenario) || !idPattern.MatchString(key.Item) || key.Repeat < 1 || key.Item != cfg.Item.ID || key.Variant != cfg.Variant.ID {
		problems = append(problems, errors.New("eval: probe requires a complete matching key, item and variant"))
	}
	problems = append(problems, validateItem(cfg.Item))
	problems = append(problems, validateVariants([]Variant{cfg.Variant})...)
	if cfg.Item.ExpectedTools != nil {
		problems = append(problems, errors.New("eval: probe cannot declare quality expectations"))
	}
	if cfg.Timeout < 0 {
		problems = append(problems, errors.New("eval: probe Timeout must be non-negative"))
	}
	if cfg.PrepareAttempt == nil {
		problems = append(problems, errors.New("eval: probe requires PrepareAttempt"))
	}
	problems = append(problems, validatePreparedConfig(probeRunnerConfig(cfg))...)
	if err := errors.Join(problems...); err != nil {
		return preparedPlan{}, err
	}
	p := preparedPlanForConfig(probeRunnerConfig(cfg), []plannedAttempt{probeAttempt(cfg)})
	p.Kind = ExecutionProbe
	return p, nil
}

func probeRunnerConfig(cfg ProbeConfig) Config {
	return Config{PrepareAttempt: cfg.PrepareAttempt, PreparedWriter: cfg.PreparedWriter, PrepareTimeout: cfg.PrepareTimeout, CleanupTimeout: cfg.CleanupTimeout, Scenarios: []Scenario{probeAttempt(cfg).scenario}, Variants: []Variant{cfg.Variant}, OnAttempt: cfg.OnAttempt}
}

func probeAttempt(cfg ProbeConfig) plannedAttempt {
	return plannedAttempt{key: cfg.Key, item: cloneItem(cfg.Item), variant: cfg.Variant, scenario: Scenario{ID: cfg.Key.Scenario, Agent: cfg.Agent, Timeout: cfg.Timeout, Items: Items(cfg.Item)}}
}
