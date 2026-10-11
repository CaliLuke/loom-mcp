package run

// ContextRecovery contains runtime-stamped context recovery settings. K14 adds
// overflow controls; DisableRecall's zero value enables context lookup.
type ContextRecovery struct {
	DisableRecall bool `json:"disable_recall"`
}
