package gateway

import (
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/stretchr/testify/assert"
)

func TestContextLimitsUnsupported(t *testing.T) {
	_, ok := any(&RemoteClient{}).(model.ContextInfo)
	assert.False(t, ok)
}
