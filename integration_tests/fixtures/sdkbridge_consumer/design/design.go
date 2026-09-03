package design

import (
	. "github.com/CaliLuke/loom-mcp/v2/dsl"
	. "github.com/CaliLuke/loom/dsl"
)

var LookupPayload = Type("LookupPayload", func() {
	Attribute("query", String)
})

var LookupResult = Type("LookupResult", func() {
	Attribute("answer", String)
	Required("answer")
})

var _ = API("sdkbridge-consumer", func() {
	Title("SDK bridge consumer")
})

var _ = Service("consumer", func() {
	MCP("consumer", "1.0.0")

	Method("lookup", func() {
		Payload(LookupPayload)
		Result(LookupResult)
		Tool("lookup", "Look up an answer")
	})
})
