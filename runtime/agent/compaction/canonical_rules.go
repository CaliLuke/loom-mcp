package compaction

// tokenRule stores a default's rational rule, or its absolute replacement.
// A zero cap is unbounded; zero tokens selects the rule.
type tokenRule struct {
	Tokens int   `json:"tokens,omitzero"`
	Ratio  Ratio `json:"ratio,omitzero"`
	Cap    int   `json:"cap,omitzero"`
}

type budgetRules struct {
	KeepRecent          tokenRule `json:"keep_recent"`
	KeepUser            tokenRule `json:"keep_user"`
	ProtectRecentTool   tokenRule `json:"protect_recent_tool"`
	MinSaving           tokenRule `json:"min_saving"`
	MaxSummary          tokenRule `json:"max_summary"`
	Receipt             tokenRule `json:"receipt"`
	MaxSummarisedResult tokenRule `json:"max_summarised_result"`
}

func canonicalRules(s Settings) budgetRules {
	return budgetRules{
		KeepRecent:          rule(s.KeepRecentTokens, 1, 10, 20000),
		KeepUser:            rule(s.KeepUserTokens, 1, 20, 10000),
		ProtectRecentTool:   rule(s.ProtectRecentToolTokens, 3, 20, 40000),
		MinSaving:           rule(s.MinSavingTokens, 1, 10, 20000),
		MaxSummary:          rule(s.MaxSummaryTokens, 1, 10, 16000),
		Receipt:             rule(s.ReceiptTokens, 3, 100, 6000),
		MaxSummarisedResult: rule(s.MaxSummarisedResultTokens, 1, 2, 0),
	}
}

func rule(tokens, num, den, capTokens int) tokenRule {
	if tokens != 0 {
		return tokenRule{Tokens: tokens}
	}
	return tokenRule{Ratio: Ratio{Num: num, Den: den}, Cap: capTokens}
}
