package ollama

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestOllamaOutputLimited(t *testing.T) {
	assert.True(t, ollamaOutputLimited("length"))
	assert.False(t, ollamaOutputLimited("stop"))
	assert.False(t, ollamaOutputLimited(""))
}
