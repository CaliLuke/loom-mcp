package dsl_test

import (
	"testing"

	. "github.com/CaliLuke/loom-mcp/dsl"
	. "github.com/CaliLuke/loom/dsl"
)

func TestHistoryDSLRejectsInvalidConfiguration(t *testing.T) {
	cases := []struct {
		name    string
		history func()
		want    string
	}{
		{
			name:    "empty policy",
			history: func() {},
			want:    "history policy must specify a mode",
		},
		{
			name:    "zero recent turns",
			history: func() { KeepRecentTurns(0) },
			want:    "KeepRecentTurns requires n > 0, got 0",
		},
		{
			name:    "negative recent turns",
			history: func() { KeepRecentTurns(-1) },
			want:    "KeepRecentTurns requires n > 0, got -1",
		},
		{
			name: "compression after recent turns",
			history: func() {
				KeepRecentTurns(2)
				CompressAtTurns(10)
			},
			want: "CompressAtTurns cannot be combined with KeepRecentTurns",
		},
		{
			name: "recent turns after compression",
			history: func() {
				CompressAtTurns(10)
				KeepRecentTurns(2)
			},
			want: "only one history policy may be configured per agent",
		},
		{
			name:    "zero compression turns",
			history: func() { CompressAtTurns(0) },
			want:    "CompressAtTurns requires n > 0, got 0",
		},
		{
			name:    "negative compression tokens",
			history: func() { CompressAtMaxInputTokens(-1) },
			want:    "CompressAtMaxInputTokens requires n > 0, got -1",
		},
		{
			name:    "zero retained turns",
			history: func() { KeepMaxTurns(0) },
			want:    "KeepMaxTurns requires n > 0, got 0",
		},
		{
			name:    "negative retained tokens",
			history: func() { KeepMaxInputTokens(-1) },
			want:    "KeepMaxInputTokens requires n > 0, got -1",
		},
		{
			name: "compression missing retention",
			history: func() {
				CompressAtTurns(10)
			},
			want: "compression requires KeepMaxTurns or KeepMaxInputTokens",
		},
		{
			name: "compression missing trigger",
			history: func() {
				KeepMaxTurns(2)
			},
			want: "compression requires CompressAtTurns or CompressAtMaxInputTokens",
		},
		{
			name: "retained turns equal trigger",
			history: func() {
				CompressAtTurns(10)
				KeepMaxTurns(10)
			},
			want: "KeepMaxTurns must be less than CompressAtTurns",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runDSLExpectError(t, historyDesign(tc.history), tc.want)
		})
	}
}

func TestHistoryDSLRejectsDuplicatePolicy(t *testing.T) {
	runDSLExpectError(t, func() {
		API("test", func() {})
		Service("svc", func() {
			Agent("agent", "desc", func() {
				RunPolicy(func() {
					History(func() { KeepRecentTurns(2) })
					History(func() { KeepRecentTurns(1) })
				})
			})
		})
	}, `History already defined for agent "agent"`)
}

func TestCacheDSLRejectsDuplicatePolicy(t *testing.T) {
	runDSLExpectError(t, func() {
		API("test", func() {})
		Service("svc", func() {
			Agent("agent", "desc", func() {
				RunPolicy(func() {
					Cache(func() { AfterSystem() })
					Cache(func() { AfterTools() })
				})
			})
		})
	}, `Cache already defined for agent "agent"`)
}

func historyDesign(history func()) func() {
	return func() {
		API("test", func() {})
		Service("svc", func() {
			Agent("agent", "desc", func() {
				RunPolicy(func() {
					History(history)
				})
			})
		})
	}
}
