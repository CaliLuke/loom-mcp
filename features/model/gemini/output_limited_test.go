package gemini

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"google.golang.org/genai"
)

func TestGeminiOutputLimited(t *testing.T) {
	assert.True(t, geminiOutputLimited(string(genai.FinishReasonMaxTokens)))
	assert.False(t, geminiOutputLimited(string(genai.FinishReasonStop)))
	assert.False(t, geminiOutputLimited(""))
}
