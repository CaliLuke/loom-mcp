package testscenarios

import (
	. "github.com/CaliLuke/loom-mcp/dsl"
	. "github.com/CaliLuke/loom/dsl"
)

// MethodSimpleCompatible defines a simple method-bound tool whose shapes are compatible
// to trigger transform emission (Args -> Method Payload, Method Result -> Return).
func MethodSimpleCompatible() func() {
	return func() {
		API("svc", func() {})
		var QPayload = Type("QPayload", func() {
			Attribute("q", String, "Q")
			Required("q")
		})
		var OkResult = Type("OkResult", func() {
			Attribute("ok", Boolean, "OK")
		})
		Service("svc", func() {
			Method("Do", func() {
				Payload(func() {
					Attribute("q", String, "Q")
					Required("q")
				})
				Result(func() {
					Attribute("ok", Boolean, "OK")
				})
			})
			Agent("scribe", "Doc helper", func() {
				Use("lookup", func() {
					Tool("by_id", "Lookup by ID", func() {
						Args(QPayload)
						Return(OkResult)
						BindTo("Do")
					})
				})
			})
		})
	}
}
