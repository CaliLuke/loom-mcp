package compaction

import (
	"encoding/json/v2"
	"fmt"
)

type canonicalSettings struct {
	Version int `json:"v"`
	Settings
	Rules budgetRules `json:"rules"`
}

// Canonical encodes the fixed-order v1 settings without resolving B-dependent rules.
func Canonical(settings Settings) []byte {
	data, err := json.Marshal(canonicalSettings{Version: 1, Settings: Merge(settings, nil), Rules: canonicalRules(settings)}, json.Deterministic(true))
	if err != nil {
		panic(fmt.Sprintf("compaction canonical settings: %v", err))
	}
	return data
}

// DecodeCanonical rejects unknown settings and unsupported canonical versions.
func DecodeCanonical(data []byte, settings *Settings) error {
	var wire canonicalSettings
	if err := json.Unmarshal(data, &wire, json.RejectUnknownMembers(true)); err != nil {
		return err
	}
	if wire.Version != 1 {
		return fmt.Errorf("context settings version %d is unsupported", wire.Version)
	}
	if wire.Rules != canonicalRules(wire.Settings) {
		return fmt.Errorf("context settings budget rules differ from version 1")
	}
	*settings = wire.Settings
	return nil
}
