package agenttest

import (
	"errors"
	"fmt"
	"os"
	"testing"
)

// ErrLiveModelDisabled indicates that explicit live-model opt-in is absent.
var ErrLiveModelDisabled = errors.New("agenttest: live model disabled")

// LiveModelEnabled validates LOOM_LIVE_MODEL: unset or 0 disables, 1 enables.
func LiveModelEnabled() (bool, error) {
	switch value := os.Getenv("LOOM_LIVE_MODEL"); value {
	case "", "0":
		return false, nil
	case "1":
		return true, nil
	default:
		return false, fmt.Errorf("agenttest: LOOM_LIVE_MODEL must be 0 or 1, got %q", value)
	}
}

// SkipUnlessLive skips t unless LOOM_LIVE_MODEL=1, naming the opted-in model ID.
// Invalid values fail t. Undeclared application clients are not blocked.
func SkipUnlessLive(t testing.TB, modelID string) {
	t.Helper()
	enabled, err := LiveModelEnabled()
	if err != nil {
		t.Fatal(err)
		return
	}
	if !enabled {
		t.Skipf("agenttest: live model %q requires LOOM_LIVE_MODEL=1", modelID)
	}
}

// LiveModel opts a named application-registered client in to live evaluation.
// It creates no provider and rejects disabled opt-in before application
// registration. Undeclared application clients remain available.
func LiveModel(id string) Option {
	return func(options *harnessOptions) error {
		enabled, err := LiveModelEnabled()
		if err != nil {
			return err
		}
		if !enabled {
			return fmt.Errorf("agenttest: live model %q requires LOOM_LIVE_MODEL=1: %w", id, ErrLiveModelDisabled)
		}
		options.live = append(options.live, id)
		return nil
	}
}
