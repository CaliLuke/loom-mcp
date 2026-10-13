package dsl_test

import (
	"testing"

	. "github.com/CaliLuke/loom-mcp/v2/dsl"
	. "github.com/CaliLuke/loom/dsl"
)

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
