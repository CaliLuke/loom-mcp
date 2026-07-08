package testscenarios

import (
	. "github.com/CaliLuke/loom-mcp/dsl"
	. "github.com/CaliLuke/loom/dsl"
)

// ArgsUnionSumTypes returns a DSL with union (OneOf) args and result.
func ArgsUnionSumTypes() func() {
	return func() {
		API("alpha", func() {})

		var UnionPayload = Type("UnionPayload", func() {
			Attribute("id", String, "Request identifier")
			OneOf("value", func() {
				Attribute("number", Int32, "Numeric value")
				Attribute("text", String, "Text value")
			})
			Required("id", "value")
		})

		Service("alpha", func() {
			Agent("scribe", "Doc helper", func() {
				Use("union", func() {
					Tool("echo", "Echo union", func() {
						Args(UnionPayload)
						Return(UnionPayload)
					})
				})
			})
		})
	}
}

// BadUnionPayloadExample returns a DSL design where an authored payload example
// cannot be matched to any union branch.
func BadUnionPayloadExample() func() {
	return func() {
		API("alpha", func() {})

		var UnionPayload = Type("UnionPayload", func() {
			Attribute("id", String, "Request identifier")
			OneOf("value", func() {
				Attribute("number", Int32, "Numeric value")
				Attribute("text", String, "Text value")
			})
			Required("id", "value")
			Example(Val{
				"id": "req-123",
				"value": Val{
					"unexpected": "shape",
				},
			})
		})

		Service("alpha", func() {
			Agent("scribe", "Doc helper", func() {
				Use("helpers", func() {
					Tool("bad_union_example", "Bad union example", func() {
						Args(UnionPayload)
					})
				})
			})
		})
	}
}
