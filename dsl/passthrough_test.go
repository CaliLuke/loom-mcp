package dsl_test

import (
	"testing"

	. "github.com/CaliLuke/loom-mcp/v2/dsl"
	. "github.com/CaliLuke/loom/dsl"
	goaexpr "github.com/CaliLuke/loom/expr"
)

func TestPassthroughDSLRejectsInvalidTargets(t *testing.T) {
	cases := []struct {
		name string
		call func()
		want string
	}{
		{
			name: "tool name mismatch",
			call: func() { Passthrough("other", "logging", "LogMessage") },
			want: `Passthrough tool name "other" does not match current tool "log_message"`,
		},
		{
			name: "detached method",
			call: func() { Passthrough("log_message", &goaexpr.MethodExpr{Name: "LogMessage"}) },
			want: "Passthrough target method must belong to a service",
		},
		{
			name: "service without method",
			call: func() { Passthrough("log_message", "logging") },
			want: "Passthrough with service name requires a method name",
		},
		{
			name: "unsupported target",
			call: func() { Passthrough("log_message", 42) },
			want: "Passthrough target must be a *goaexpr.MethodExpr or (serviceName string, methodName string)",
		},
		{
			name: "empty service",
			call: func() { Passthrough("log_message", "", "LogMessage") },
			want: "Passthrough requires non-empty service and method names",
		},
		{
			name: "empty method",
			call: func() { Passthrough("log_message", "logging", "") },
			want: "Passthrough requires non-empty service and method names",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runDSLExpectError(t, passthroughDesign(tc.call), tc.want)
		})
	}
}

func passthroughDesign(call func()) func() {
	return func() {
		API("test", func() {})
		Service("logging", func() {
			Agent("agent", "desc", func() {
				Export("logging-tools", func() {
					Tool("log_message", "Log a message", call)
				})
			})
		})
	}
}
