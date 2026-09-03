package design

import (
	. "github.com/CaliLuke/loom-mcp/v2/dsl"
	. "github.com/CaliLuke/loom/dsl"
)

var _ = API("agentfeatures", func() {
	Title("Generated Agent Feature Fixture")
	Description("Generated acceptance fixture for ADK-inspired agent features")
	Version("1.0")
})

var ArtifactTools = Toolset("artifacts", FromArtifacts(MaxArtifactBytes(65536), MaxArtifacts(50)))

var MemoryTools = Toolset("memory", FromMemory(MemoryMaxResults(20)))

var LongTermMemoryTools = Toolset("long_term_memory", FromMemory(MemoryLongTerm(), MemoryVisibilityUser(), MemoryMaxResults(20)))

var SkillTools = Toolset("skills", FromSkills(".agents/skills", SkillPreload(SkillPreloadOnStart), SkillReload(SkillReloadPerCall)))

var ValidationRegistry = Registry("validation-registry", func() {
	URL("https://registry.fixture.invalid")
})

var RegistryValidationTools = Toolset("registry_validation", FromRegistry(ValidationRegistry, "validation-tools"))

var TopicPayload = Type("TopicPayload", func() {
	Attribute("topic", String, "Topic to process")
	Required("topic")
})

var ReviewPayload = Type("ReviewPayload", func() {
	Attribute("strict", Boolean, "Whether review should be strict")
	Required("strict")
})

var EmptyPayload = Type("EmptyPayload", func() {})
var LimitPayload = Type("LimitPayload", func() {
	Attribute("reason", String, "Reason that ended the run")
	Required("reason")
})

var StatusResult = Type("StatusResult", func() {
	Attribute("ok", Boolean, "Whether the operation succeeded")
	Attribute("approved", Boolean, "Whether the operation was approved")
	Required("ok")
})

var MethodEchoPayload = Type("MethodEchoPayload", func() {
	Attribute("topic", String, "Topic to echo through the service method")
	Required("topic")
})

var MethodEchoResult = Type("MethodEchoResult", func() {
	Attribute("ok", Boolean, "Whether the method-backed tool succeeded")
	Attribute("message", String, "Message returned by the service method")
	Required("ok", "message")
})

var MethodToolPayload = Type("MethodToolPayload", func() {
	Attribute("topic", String, "Topic to send to the service method")
	Required("topic")
})

var MethodToolResult = Type("MethodToolResult", func() {
	Attribute("ok", Boolean, "Whether the method-backed tool succeeded")
	Attribute("message", String, "Message returned to the agent runtime")
	Required("ok", "message")
})

var _ = Service("features", func() {
	Method("echo_topic", func() {
		Payload(MethodEchoPayload)
		Result(MethodEchoResult)
	})
	Agent("specialist", "Generated child-agent fixture", func() {
		Export("delegated", func() {
			Tool("summarize", "Summarize a topic through a child run", func() {
				Args(TopicPayload)
				Return(MethodToolResult)
			})
		})
		RunPolicy(func() {
			DefaultCaps(MaxToolCalls(4), MaxRecoveryTurns(1))
			TimeBudget("10s")
			InterruptsAllowed(true)
		})
	})
	Agent("coordinator", "Generated acceptance agent", func() {
		Use(ArtifactTools)
		Use(MemoryTools)
		Use(LongTermMemoryTools)
		Use(SkillTools)
		Use(AgentToolset("features", "specialist", "delegated"))
		Use("workflow", func() {
			Tool("draft", "Draft a response", func() {
				Args(TopicPayload)
				Return(StatusResult)
			})
			Tool("review", "Review the draft", func() {
				Args(ReviewPayload)
				Return(StatusResult)
			})
			Tool("retry", "Run a bounded retry step", func() {
				Args(EmptyPayload)
				Return(StatusResult)
			})
			Tool("publish", "Publish the result", func() {
				Args(EmptyPayload)
				Return(StatusResult)
				Confirmation(func() {
					PromptTemplate("Publish this generated result?")
					DeniedResultTemplate(`{"ok":false,"approved":false}`)
				})
			})
			Tool("revise", "Revise the result", func() {
				Args(EmptyPayload)
				Return(StatusResult)
			})
			Tool("finalize", "Record the final workflow result", func() {
				Args(LimitPayload)
				Return(StatusResult)
				TerminalRun()
			})

			Tool("method_echo", "Echo a topic through the generated method dispatcher", func() {
				Args(MethodToolPayload)
				Return(MethodToolResult)
				BindTo("features", "echo_topic")
			})
		})
		RunPolicy(func() {
			DefaultCaps(MaxToolCalls(12), MaxRecoveryTurns(2))
			TimeBudget("30s")
			InterruptsAllowed(true)
			Interceptors("audit")
			RetryAndReflect(MaxRetries(1), ErrorIfRetryExceeded(true))
			PreloadMemory(MemoryScopeCurrentRun(), MemoryMaxResults(5))
			PreloadLongTermMemory(MemoryVisibilityUser(), MemoryMaxResults(5))
		})
		Workflow(func() {
			Parallel(func() {
				Step("draft", "workflow.draft", `{"topic":"loom"}`)
				Step("review", "workflow.review", `{"strict":true}`)
			})
			Join("reviewed", "draft", "review")
			RequestInput("approval", "Approval", `{"type":"object","properties":{"content-type":{"type":"string"}},"required":["content-type"]}`)
			Loop("retry", "workflow.retry", `{}`, MaxIterations(2))
			Branch("route", "approval", Case("$.content-type", "application/json", "publish"), BranchDefault("revise"))
			Step("publish", "workflow.publish", `{}`)
			Step("revise", "workflow.revise", `{}`)
			FinalMessage("generated workflow complete")
		})
	})
	Agent("registry-validator", "Generated registry schema validation fixture", func() {
		Use(RegistryValidationTools)
	})
})
