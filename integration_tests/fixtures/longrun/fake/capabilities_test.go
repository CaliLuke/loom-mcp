package fake_test

import (
	"testing"

	"example.com/longrun/fake"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/stretchr/testify/assert"
)

type capableClient struct {
	model.Client
	model.TokenCounter
	model.ContextInfo
}

func TestRejectionPreservesCapabilities(t *testing.T) {
	source := capableClient{}
	wrapped := fake.Reject(source)
	_, counter := wrapped.(model.TokenCounter)
	_, limits := wrapped.(model.ContextInfo)
	assert.True(t, counter)
	assert.True(t, limits)
}
