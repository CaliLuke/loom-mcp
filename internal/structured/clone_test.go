package structured_test

import (
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/internal/structured"
	"github.com/stretchr/testify/assert"
)

type cloneDocument struct {
	Records  [2]map[string][]*int
	Optional *int
	Empty    any
	Hidden   chan int
}

func TestCloneSeparatesNestedArrayOwnershipAndPreservesNilFields(t *testing.T) {
	original := cloneDocument{Records: [2]map[string][]*int{{"values": {new(7), nil}}, nil}, Hidden: make(chan int)}
	cloned := structured.Clone(original)
	*cloned.Records[0]["values"][0] = 9
	cloned.Records[0]["values"] = append(cloned.Records[0]["values"], new(11))
	assert.Equal(t, 7, *original.Records[0]["values"][0])
	assert.Len(t, original.Records[0]["values"], 2)
	assert.Nil(t, cloned.Records[0]["values"][1])
	assert.Nil(t, cloned.Records[1])
	assert.Nil(t, cloned.Optional)
	assert.Nil(t, cloned.Empty)
	assert.Equal(t, original.Hidden, cloned.Hidden)
	assert.Nil(t, structured.Clone[any](nil))
}
