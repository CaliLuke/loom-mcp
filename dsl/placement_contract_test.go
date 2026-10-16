package dsl_test

import (
	"testing"

	. "github.com/CaliLuke/loom-mcp/v2/dsl"
	. "github.com/CaliLuke/loom/dsl"
)

// Misplaced declarations must be diagnosed at DSL evaluation, before codegen
// produces an incomplete service or silently ignores the requested behavior.
func TestPublicDSLRejectsWrongParent(t *testing.T) {
	for _, tc := range []struct {
		name string
		call func()
	}{
		{"MCP", func() {
			MCP("test", "1.0.0")
		}},
		{"Tool", func() {
			Tool("read")
		}},
		{"Args", func() {
			Args(String)
		}},
		{"Return", func() {
			Return(String)
		}},
		{"ServerData", func() {
			ServerData("private", String)
		}},
		{"FromMethodResultField", func() {
			FromMethodResultField("field")
		}},
		{"Audience", func() {
			Audience(ServerDataAudienceInternal)
		}},
		{"Tags", func() {
			Tags("read")
		}},
		{"BindTo", func() {
			BindTo("service", "method")
		}},
		{"Inject", func() {
			Inject("session_id")
		}},
		{"CallHintTemplate", func() {
			CallHintTemplate("read")
		}},
		{"ResultHintTemplate", func() {
			ResultHintTemplate("done")
		}},
		{"BoundedResult", func() {
			BoundedResult()
		}},
		{"ResultReminder", func() {
			ResultReminder("continue")
		}},
		{"TerminalRun", TerminalRun},
		{"Bookkeeping", Bookkeeping},
		{"Cursor", func() {
			Cursor("cursor")
		}},
		{"NextCursor", func() {
			NextCursor("cursor")
		}},
		{"RunPolicy", func() {
			RunPolicy(nil)
		}},
		{"DefaultCaps", func() {
			DefaultCaps()
		}},
		{"TimeBudget", func() {
			TimeBudget("1m")
		}},
		{"InterruptsAllowed", func() {
			InterruptsAllowed(true)
		}},
		{"OnMissingFields", func() {
			OnMissingFields("resume")
		}},
		{"Interceptors", func() {
			Interceptors("audit")
		}},
		{"PreloadMemory", func() {
			PreloadMemory()
		}},
		{"PreloadLongTermMemory", func() {
			PreloadLongTermMemory()
		}},
		{"RetryAndReflect", func() {
			RetryAndReflect()
		}},
		{"Timing", func() {
			Timing(nil)
		}},
		{"Budget", func() {
			Budget("1m")
		}},
		{"Plan", func() {
			Plan("1s")
		}},
		{"Tools", func() {
			Tools("1s")
		}},
		{"Cache", func() {
			Cache(nil)
		}},
		{"AfterSystem", AfterSystem},
		{"AfterTools", AfterTools},
		{"APIVersion", func() {
			APIVersion("v1")
		}},
		{"Retry", func() {
			Retry(1, "1s")
		}},
		{"SyncInterval", func() {
			SyncInterval("1m")
		}},
		{"CacheTTL", func() {
			CacheTTL("1m")
		}},
		{"Federation", func() {
			Federation(nil)
		}},
		{"Include", func() {
			Include("*")
		}},
		{"Exclude", func() {
			Exclude("*")
		}},
		{"Resource", func() {
			Resource("docs", "doc://root", "text/plain")
		}},
		{"WatchableResource", func() {
			WatchableResource("docs", "doc://root", "text/plain")
		}},
		{"StaticPrompt", func() {
			StaticPrompt("prompt", "Prompt", "user", "hello")
		}},
		{"DynamicPrompt", func() {
			DynamicPrompt("prompt", "Prompt")
		}},
		{"Workflow", func() {
			Workflow(nil)
		}},
		{"Step", func() {
			Step("read", "docs.read", "{}")
		}},
		{"FinalMessage", func() {
			FinalMessage("done")
		}},
		{"Parallel", func() {
			Parallel(nil)
		}},
		{"Join", func() {
			Join("done", "read")
		}},
		{"RequestInput", func() {
			RequestInput("input", "Input", "{}")
		}},
		{"Loop", func() {
			Loop("read", "docs.read", "{}")
		}},
		{"Branch", func() {
			Branch("choice", "read")
		}},
		{"Agent", func() {
			Agent("agent", "Agent", nil)
		}},
		{"Use", func() {
			Use("tools")
		}},
		{"Export", func() {
			Export("tools")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runDSLExpectError(t, tc.call, "invalid use of "+tc.name)
		})
	}
}
