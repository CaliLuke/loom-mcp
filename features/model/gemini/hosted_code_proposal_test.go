package gemini_test

import (
	"encoding/json/v2"
	"errors"
	"io/fs"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genai"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

// This is a fixture-only design probe, not an adapter or new model capability.
// It proves the proposed wire shape retains each supported SDK field and order.
func TestHostedCodeEvaluationProposedRepresentation(t *testing.T) {
	fixtures := os.DirFS("testdata/hosted_code")
	paths, err := fs.Glob(fixtures, "*.response.json")
	require.NoError(t, err)
	require.Len(t, paths, 9)
	for _, path := range paths {
		name := strings.TrimSuffix(path, ".response.json")
		t.Run(name, func(t *testing.T) {
			raw, readErr := fs.ReadFile(fixtures, path)
			require.NoError(t, readErr)
			var response genai.GenerateContentResponse
			require.NoError(t, json.Unmarshal(raw, &response))
			parts := response.Candidates[0].Content.Parts
			proposed, proposalErr := proposedHostedParts(parts, true)
			if strings.HasPrefix(name, "unsupported_") {
				require.Error(t, proposalErr)
				assert.Nil(t, proposed)
				return
			}
			require.NoError(t, proposalErr)
			expected, readErr := fs.ReadFile(fixtures, strings.TrimSuffix(path, ".response.json")+".proposed.json")
			require.NoError(t, readErr)
			actual, marshalErr := json.Marshal(proposed)
			require.NoError(t, marshalErr)
			assert.JSONEq(t, string(expected), string(actual))
			var restored []map[string]string
			require.NoError(t, json.Unmarshal(actual, &restored))
			assert.Equal(t, parts, restoreProposedHostedParts(restored), "IDs, outcome, source/output bytes and order survive proposed serialization")
			if name != "text_only" {
				disabled, disabledErr := proposedHostedParts(parts, false)
				require.Error(t, disabledErr)
				assert.Nil(t, disabled)
				var message model.Message
				wire, marshalErr := json.Marshal(map[string]any{"Role": "assistant", "Parts": proposed})
				require.NoError(t, marshalErr)
				require.Error(t, json.Unmarshal(wire, &message), "current canonical model rejects unimplemented kinds before provider I/O")
			}
		})
	}
}

func proposedHostedParts(parts []*genai.Part, enabled bool) ([]map[string]string, error) {
	out := make([]map[string]string, 0, len(parts))
	for _, part := range parts {
		if part == nil || part.Thought || len(part.ThoughtSignature) > 0 {
			return nil, errors.New("proposed subset excludes reasoning metadata")
		}
		switch {
		case part.ExecutableCode != nil:
			code := part.ExecutableCode
			if !enabled || code.Language != genai.LanguagePython || code.Code == "" || !reflect.DeepEqual(part, &genai.Part{ExecutableCode: code}) {
				return nil, errors.New("unsupported executable code")
			}
			out = append(out, map[string]string{"Kind": "executable_code", "ID": code.ID, "Language": string(code.Language), "Code": code.Code})
		case part.CodeExecutionResult != nil:
			result := part.CodeExecutionResult
			if !enabled || !reflect.DeepEqual(part, &genai.Part{CodeExecutionResult: result}) || (result.Outcome != genai.OutcomeOK && result.Outcome != genai.OutcomeFailed && result.Outcome != genai.OutcomeDeadlineExceeded) {
				return nil, errors.New("unsupported execution result")
			}
			out = append(out, map[string]string{"Kind": "code_execution_result", "ID": result.ID, "Outcome": string(result.Outcome), "Output": result.Output})
		case part.Text != "":
			out = append(out, map[string]string{"Kind": "text", "Text": part.Text})
		default:
			return nil, errors.New("unsupported provider part")
		}
	}
	return out, nil
}

func restoreProposedHostedParts(parts []map[string]string) []*genai.Part {
	out := make([]*genai.Part, 0, len(parts))
	for _, part := range parts {
		switch part["Kind"] {
		case "text":
			out = append(out, &genai.Part{Text: part["Text"]})
		case "executable_code":
			out = append(out, &genai.Part{ExecutableCode: &genai.ExecutableCode{ID: part["ID"], Language: genai.Language(part["Language"]), Code: part["Code"]}})
		case "code_execution_result":
			out = append(out, &genai.Part{CodeExecutionResult: &genai.CodeExecutionResult{ID: part["ID"], Outcome: genai.Outcome(part["Outcome"]), Output: part["Output"]}})
		}
	}
	return out
}
