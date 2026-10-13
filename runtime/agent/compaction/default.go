package compaction

import (
	"crypto/sha256"
	"encoding/json/v2"
	"fmt"
	"maps"
	"slices"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
)

// Qualification records an exact provider/model context qualification.
type Qualification struct {
	Config                            string
	Models                            []string
	Tier                              Tier
	Freeze, Calibration, Report, Date string
}

// Qualifications returns the shipped attestations. No providers are qualified
// until their exact configuration has a committed qualification report.
func Qualifications() []Qualification {
	return nil
}

// QualifiedTier looks up an exact pair in the shipped qualification table.
func QualifiedTier(config, modelID string) Tier {
	return LookupQualification(Qualifications(), config, modelID)
}

// LookupQualification matches only exact normalized pairs in supplied attestations.
func LookupQualification(qualifications []Qualification, config, modelID string) Tier {
	if config == "" || modelID == "" {
		return ""
	}
	modelID = model.NormalizeProviderModel(config, modelID)
	for _, q := range qualifications {
		if q.Config != config || (q.Tier != Evict && q.Tier != Summary) {
			continue
		}
		for _, candidate := range q.Models {
			candidate = model.NormalizeProviderModel(config, candidate)
			if candidate == modelID {
				return q.Tier
			}
		}
	}
	return ""
}

// ObservedQualifiedTier folds immutable recorded model qualifications.
func ObservedQualifiedTier(state State) Tier {
	if len(state.Models) == 0 {
		return ""
	}
	tier := Summary
	for _, m := range state.Models {
		if m.Qualified != Evict && m.Qualified != Summary {
			return ""
		}
		if m.Qualified == Evict {
			tier = Evict
		}
	}
	return tier
}

// On reports whether the recorded policy permits threshold compaction.
func On(state State, _ []index.MessageEntry, settings Settings) bool {
	return (settings.Threshold || settings.Default && ObservedQualifiedTier(state) != "") && !(settings.KeepNonTextResident && state.Flags.NonTextResident)
}

// EffectiveTier limits default transformations to the recorded qualification.
func EffectiveTier(state State, settings Settings) Tier {
	tier := settings.MaxTier
	if tier == "" {
		tier = Summary
	}
	if !settings.Threshold && settings.Default && tier == Summary && ObservedQualifiedTier(state) == Evict {
		return Evict
	}
	return tier
}

// Compatibility is a compact result bound to original entries and model support.
type Compatibility struct {
	Coverage   string
	Support    string
	Checked    bool
	Compatible bool
	Reason     string
	Model      string
}

// NonTextCoverage binds original non-text metadata without carrying part descriptors.
// Text-only additions and committing the same pending entries preserve this identity.
func NonTextCoverage(entries []index.MessageEntry) string {
	type covered struct {
		ID          index.MessageID
		Kind        index.EntryKind
		Hash, Parts runstore.Hash
		NonText     int
	}
	var original []covered
	for _, entry := range entries {
		if entry.NonText <= 0 {
			continue
		}
		next := covered{ID: entry.ID, Kind: entry.Kind, Hash: entry.Hash, NonText: entry.NonText}
		if entry.Result != nil {
			next.Parts = entry.Result.Parts
		}
		original = append(original, next)
	}
	return compatibilityDigest(original)
}

// SupportFingerprint binds first-observed call identities and immutable support.
func SupportFingerprint(state State) string {
	type observed struct {
		Model, Config string
		Call          runstore.CallKey
		Support       model.ToolResultPartSupport
	}
	models := make([]observed, 0, len(state.Models))
	for _, name := range slices.Sorted(maps.Keys(state.Models)) {
		m := state.Models[name]
		models = append(models, observed{Model: name, Config: m.ProviderConfig, Call: m.FirstCall, Support: m.ToolResultParts})
	}
	return compatibilityDigest(models)
}

func compatibilityDigest(value any) string {
	data, err := json.Marshal(value, json.Deterministic(true))
	if err != nil {
		panic(fmt.Sprintf("encode compatibility metadata: %v", err))
	}
	return fmt.Sprintf("sha256:%x", sha256.Sum256(data))
}

// NonTextResident reports whether original attachments must remain resident.
func NonTextResident(state State, entries []index.MessageEntry, settings Settings) (bool, string, string) {
	if !settings.KeepNonTextResident {
		return false, "", ""
	}
	if state.Flags.NonTextResident {
		return true, "", ""
	}
	// Absence of recorded support is not evidence of incompatibility.
	if len(state.Models) == 0 {
		return false, "", ""
	}
	present, tooLarge := nonTextBudget(entries, settings.RecallPartTokens(state.MinB))
	if !present {
		return false, "", ""
	}
	support := ToolResultParts(state)
	names := slices.Sorted(maps.Keys(state.Models))
	if len(support.ImageFormats) == 0 && len(support.DocumentFormats) == 0 && len(support.DocumentURISchemes) == 0 {
		return true, "provider", unsupportedModel(state, names)
	}
	if tooLarge {
		return true, "too_large", limitingBudgetModel(state, names)
	}
	evidence := state.Compatibility
	if evidence.Checked && evidence.Coverage == NonTextCoverage(entries) && evidence.Support == SupportFingerprint(state) && !evidence.Compatible {
		return true, evidence.Reason, evidence.Model
	}
	return false, "", ""
}

func limitingBudgetModel(state State, names []string) string {
	if state.LimitingModel != "" {
		return state.LimitingModel
	}
	witness := ""
	budget := 0
	for _, name := range names {
		b := state.Models[name].Budget
		if b > 0 && (budget == 0 || b < budget) {
			witness = name
			budget = b
		}
	}
	return witness
}

func nonTextBudget(entries []index.MessageEntry, limit int) (bool, bool) {
	total, tooLarge := 0, false
	for _, entry := range entries {
		if entry.NonText <= 0 {
			continue
		}
		if entry.NonText > limit-total {
			tooLarge = true
		} else {
			total += entry.NonText
		}
	}
	return total > 0 || tooLarge, tooLarge
}

func unsupportedModel(state State, names []string) string {
	for _, name := range names {
		support := state.Models[name].ToolResultParts
		if len(support.ImageFormats) == 0 && len(support.DocumentFormats) == 0 && len(support.DocumentURISchemes) == 0 {
			return name
		}
	}
	if len(names) > 0 {
		return names[0]
	}
	return ""
}
