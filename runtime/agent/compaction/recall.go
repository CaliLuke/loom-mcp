package compaction

// RecallAllowances tracks independently charged discovery and read calls.
type RecallAllowances struct {
	Discovery int `json:"discovery"`
	Reads     int `json:"reads"`
}

// Refill adds ceil(initial/2), capped at three times initial. Settings validate
// positive initial values before workflow execution.
func Refill(remaining, initial int) int {
	return min(remaining+initial/2+initial%2, 3*initial)
}

// NewRecall resolves zero settings to twenty calls for each allowance.
func NewRecall(settings Settings) RecallAllowances {
	discovery, reads := settings.RecallDiscoveryCalls, settings.RecallReadCalls
	if discovery == 0 {
		discovery = 20
	}
	if reads == 0 {
		reads = 20
	}
	return RecallAllowances{Discovery: discovery, Reads: reads}
}

// Charge consumes one call, returning false without changing an exhausted allowance.
func (a *RecallAllowances) Charge(read bool) bool {
	allowance := &a.Discovery
	if read {
		allowance = &a.Reads
	}
	if *allowance == 0 {
		return false
	}
	*allowance--
	return true
}

// Refilled returns the allowances after one committed compaction of any tier.
func (a RecallAllowances) Refilled(settings Settings) RecallAllowances {
	initial := NewRecall(settings)
	return RecallAllowances{Discovery: Refill(a.Discovery, initial.Discovery), Reads: Refill(a.Reads, initial.Reads)}
}

// RecallPageBytes returns the byte page bound from the resolved protected result
// setting. The minimum allows a useful page even at the smallest supported window.
func (s Settings) RecallPageBytes(budget int) int {
	return max(512, s.resolved(budget).ProtectRecentToolTokens)
}
