package design

import (
	. "github.com/CaliLuke/loom-mcp/v2/dsl"
	. "github.com/CaliLuke/loom/dsl"
)

// SearchWeight exercises a named numeric payload representation.
var SearchWeight = Type("SearchWeight", Float64)

// SearchTags exercises a named collection payload representation.
var SearchTags = Type("SearchTags", ArrayOf(String))

// SearchPreferences exercises recursive default conversion for MCP inputs.
var SearchPreferences = Type("SearchPreferences", func() {
	Attribute("weight", SearchWeight, func() {
		Default(1.5)
	})
	Attribute("enabled", Boolean, func() {
		Default(true)
	})
	Attribute("label", String, func() {
		Default("standard")
	})
	Attribute("tags", SearchTags, func() {
		Default([]string{"base"})
	})
	Attribute("nullable_weight", Float64, func() {
		Nullable()
		Default(2.5)
	})
	Attribute("custom_weight", Float64, func() {
		Meta("struct:field:type", "float32")
		Default(3.5)
	})
	Attribute("choice", OneOf(SearchTextOption, SearchCountOption), func() {
		Untagged()
	})
	Attribute("required_label", String, func() {
		Default("required")
	})
	Required("required_label")
})

// SearchTextOption identifies a text search choice with an optional weight.
var SearchTextOption = Type("SearchTextOption", func() {
	Attribute("text", String)
	Attribute("weight", Float64, func() {
		Default(4.5)
	})
	Required("text")
})

// SearchCountOption identifies a count search choice with an optional weight.
var SearchCountOption = Type("SearchCountOption", func() {
	Attribute("count", Int)
	Attribute("weight", Float64, func() {
		Default(5.5)
	})
	Required("count")
})

// Keep the defaults specimen separate from the frozen assistant search catalog.
var _ = Service("payload_defaults", func() {
	MCP("payload-defaults", "1.0.0")
	Method("search", func() {
		Payload(func() {
			Attribute("query", String)
			Attribute("weight", Float64, func() {
				Default(0.75)
			})
			Attribute("preferences", SearchPreferences)
			Attribute("preference_list", ArrayOf(SearchPreferences))
			Attribute("preference_map", MapOf(String, SearchPreferences))
			Required("query")
		})
		Result(String)
		Tool("search", "Exercise method payload default conversion")
	})
})
