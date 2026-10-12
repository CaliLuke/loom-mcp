package testutil

import (
	"fmt"
	"github.com/stretchr/testify/assert"
	"testing"
)

type conformanceFailure struct{ message string }

func (*conformanceFailure) Helper() {}
func (f *conformanceFailure) Fatalf(format string, args ...any) {
	f.message = fmt.Sprintf(format, args...)
}
func TestProviderConformanceRequiresToolResultParts(t *testing.T) {
	suite := ProviderConformanceSuite{Provider: "test"}
	failure := &conformanceFailure{}
	validateCapabilityConformance(failure, suite.Provider, "multimodal tool results", suite.MultimodalToolResults)
	assert.Contains(t, failure.message, "multimodal tool results capability declaration is required")
}
