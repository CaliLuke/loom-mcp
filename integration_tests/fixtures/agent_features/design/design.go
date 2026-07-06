package design

import (
	. "github.com/CaliLuke/loom-mcp/dsl"
	. "github.com/CaliLuke/loom/dsl"
)

var _ = API("agentfeatures", func() {
	Title("Generated Agent Feature Fixture")
	Description("Generated acceptance fixture for ADK-inspired agent features")
	Version("1.0")
})

var ArtifactTools = Toolset("artifacts", FromArtifacts(MaxArtifactBytes(65536), MaxArtifacts(50)))

var MemoryTools = Toolset("memory", FromMemory(MemoryMaxResults(20)))

var SkillTools = Toolset("skills", FromSkills(".agents/skills", SkillPreload(SkillPreloadOnStart), SkillReload(SkillReloadPerCall)))

var TopicPayload = Type("TopicPayload", func() {
	Attribute("topic", String, "Topic to process")
	Required("topic")
})

var ReviewPayload = Type("ReviewPayload", func() {
	Attribute("strict", Boolean, "Whether review should be strict")
	Required("strict")
})

var EmptyPayload = Type("EmptyPayload", func() {})

var StatusResult = Type("StatusResult", func() {
	Attribute("ok", Boolean, "Whether the operation succeeded")
	Attribute("approved", Boolean, "Whether the operation was approved")
	Required("ok")
})

var _ = Service("features", func() {
	Agent("coordinator", "Generated acceptance agent", func() {
		Use(ArtifactTools)
		Use(MemoryTools)
		Use(SkillTools)
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
			})
		})
		RunPolicy(func() {
			DefaultCaps(MaxToolCalls(12), MaxConsecutiveFailedToolCalls(2))
			TimeBudget("30s")
			Interceptors("audit")
			RetryAndReflect(MaxRetries(1), ErrorIfRetryExceeded(true))
			PreloadMemory(MemoryScopeCurrentRun(), MemoryMaxResults(5))
		})
		Workflow(func() {
			Parallel(func() {
				Step("draft", "workflow.draft", `{"topic":"loom"}`)
				Step("review", "workflow.review", `{"strict":true}`)
			})
			Join("reviewed", "draft", "review")
			RequestInput("approval", "Approval", `{"type":"object","properties":{"approved":{"type":"boolean"}},"required":["approved"]}`)
			Loop("retry", "workflow.retry", `{}`, MaxIterations(2))
			Branch("route", "approval", Case("$.approved", "true", "publish"), BranchDefault("publish"))
			Step("publish", "workflow.publish", `{}`)
			FinalMessage("generated workflow complete")
		})
	})
})
