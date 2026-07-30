package testscenarios

import (
	. "github.com/CaliLuke/loom-mcp/v2/dsl"
	. "github.com/CaliLuke/loom/dsl"
)

// SkillsToolset references local agent skills as model-facing tools.
func SkillsToolset() func() {
	return func() {
		API("alpha", func() {})
		var Skills = Toolset(FromSkills(".agents/skills", "shared/skills", SkillPreload(SkillPreloadOnStart), SkillReload(SkillReloadPerCall)))
		Service("alpha", func() {
			Agent("scribe", "Doc helper", func() {
				Use(Skills)
			})
		})
	}
}
