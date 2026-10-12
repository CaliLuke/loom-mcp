package judge

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type interruptedCalibrationClient struct {
	model.Client
	once      sync.Once
	interrupt context.CancelFunc
}

func TestRawCorrectionProviderGrammar(t *testing.T) {
	tests := []struct{ name, payload, tool string }{
		{"malformed JSON", `{"a":`, toolName},
		{"duplicate keys", `{"a":{},"a":{}}`, toolName},
		{"invalid UTF8", "{\"a\":\"\xff\"}", toolName},
		{"string", `"invalid"`, toolName},
		{"array", `[]`, toolName},
		{"null", `null`, toolName},
		{"wrong tool", validPayload, "wrong_tool"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			script, err := agenttest.NewScript(agenttest.Respond(agenttest.RawToolCall(tools.Ident(tt.tool), rawjson.Message(tt.payload))), validTurn())
			require.NoError(t, err)
			j := newTestJudge(t, rawClient{script: script})
			result, err := j.Judge(t.Context(), testInput())
			require.NoError(t, err)
			assert.Equal(t, 1, result.Corrections)
			requests := script.Requests()
			require.Len(t, requests, 2)
			request := requests[1]
			require.NoError(t, transcript.ValidateView(request.Messages, transcript.GrammarAnthropic{}))
			require.NoError(t, transcript.ValidateView(request.Messages, transcript.GrammarBedrock{}))
			declared := make(map[string]bool, len(request.Tools))
			for _, tool := range request.Tools {
				declared[tool.Name] = true
			}
			// The transcript validator checks roles and handshakes. Provider tool
			// blocks additionally require declared names and JSON object inputs.
			for _, message := range request.Messages {
				for _, part := range message.Parts {
					if call, ok := part.(model.ToolUsePart); ok {
						assert.True(t, declared[call.Name], "undeclared tool %q", call.Name)
						assert.IsType(t, map[string]any{}, call.Input, "tool input must be an object")
					}
				}
			}
			assert.NoError(t, script.Err())
		})
	}
}

func TestCalibrationRetriesCallerInterruption(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		t.Run(map[bool]string{false: "canceled", true: "deadline"}[deadline], func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				interrupt := cancel
				want := context.Canceled
				if deadline {
					var stop context.CancelFunc
					ctx, stop = context.WithTimeout(ctx, time.Second)
					defer stop()
					interrupt = func() {}
					want = context.DeadlineExceeded
				}
				script, err := agenttest.NewScript(append(calibrationTurns(), validTurn())...)
				require.NoError(t, err)
				cache := NewMemoryCache()
				j, err := New(Config{Client: &interruptedCalibrationClient{Client: rawClient{script: script}, interrupt: interrupt}, ModelID: "judge-model", MaxOutputTokens: 512, Cache: cache})
				require.NoError(t, err)
				_, err = j.Calibrate(ctx)
				require.ErrorIs(t, err, want)
				_, cached := cache.Load(j.CalibrationKey())
				assert.False(t, cached)

				calibration, err := j.Calibrate(t.Context())
				require.NoError(t, err)
				assert.True(t, calibration.Passed)
				assert.False(t, calibration.Completed.IsZero())
				result, err := j.Judge(t.Context(), testInput())
				require.NoError(t, err)
				assert.True(t, result.Calibrated)
				assert.Len(t, script.Requests(), 5)
				assert.NoError(t, script.Err())
			})
		})
	}
}

func TestCalibrationBlankExamples(t *testing.T) {
	for _, text := range []string{"", " \t\n\u2003"} {
		for _, want := range []Label{NotAddressed, Entailed, Contradicted, Indeterminate} {
			t.Run(fmt.Sprintf("%q/%s", text, want), func(t *testing.T) {
				// Extra turns expose accidental calls for accepted blank examples.
				script, err := agenttest.NewScript(append(calibrationTurns(), validTurn())...)
				require.NoError(t, err)
				in := testInput()
				in.Text = text
				example := Example{ID: "blank", Input: in, Want: map[string]Label{"a": NotAddressed, "b": want}}
				j, err := New(Config{Client: rawClient{script: script}, ModelID: "judge-model", MaxOutputTokens: 512, Cache: NewMemoryCache(), Examples: []Example{example}})
				assert.Empty(t, script.Requests(), "construction must not call the model")
				if want != NotAddressed {
					require.ErrorIs(t, err, ErrInvalidInput)
					assert.Nil(t, j)
					return
				}
				require.NoError(t, err)
				calibration, err := j.Calibrate(t.Context())
				require.NoError(t, err)
				assert.True(t, calibration.Passed)
				require.Len(t, calibration.Examples, 5)
				assert.Empty(t, calibration.Examples[4].Err)
				assert.Equal(t, map[string]Label{"a": NotAddressed, "b": NotAddressed}, calibration.Examples[4].Got)
				assert.Len(t, script.Requests(), 4)
			})
		}
	}
}

func (c *interruptedCalibrationClient) Complete(ctx context.Context, request *model.Request) (*model.Response, error) {
	interrupted := false
	c.once.Do(func() {
		interrupted = true
		c.interrupt()
		<-ctx.Done()
	})
	if interrupted {
		return nil, fmt.Errorf("calibration interrupted: %w", ctx.Err())
	}
	return c.Client.Complete(ctx, request)
}
