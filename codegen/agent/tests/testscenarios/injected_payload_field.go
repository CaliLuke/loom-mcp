package testscenarios

import (
	. "github.com/CaliLuke/loom-mcp/dsl"
	. "github.com/CaliLuke/loom/dsl"
)

// InjectedPayloadField returns a DSL design where session_id is server-owned.
func InjectedPayloadField() func() {
	return func() {
		API("assistant", func() {})

		var LookupPayload = Type("LookupPayload", func() {
			Attribute("session_id", String, "Server-owned session identifier.")
			Attribute("query", String, "Lookup query.")
			Required("session_id", "query")
			Example(Val{
				"session_id": "sess-authored",
				"query":      "battery alarms",
			})
		})

		Service("assistant", func() {
			Method("Lookup", func() {
				Payload(LookupPayload)
				Result(String)
			})

			Agent("chat", "Chat agent", func() {
				Use("lookup", func() {
					Tool("lookup", "Lookup data", func() {
						Args(LookupPayload)
						Return(String)
						BindTo("assistant", "Lookup")
						Inject("session_id")
					})
				})
			})
		})
	}
}
