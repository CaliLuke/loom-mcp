package dsl_test

import (
	"testing"

	. "github.com/CaliLuke/loom-mcp/v2/dsl"
	agentsexpr "github.com/CaliLuke/loom-mcp/v2/expr/agent"
	. "github.com/CaliLuke/loom/dsl"
	"github.com/stretchr/testify/assert"
)

func TestContextDSLValidation(t *testing.T) {
	cases := []struct {
		name string
		body func()
		want string
	}{
		{"duplicate Context", func() {
			Context(nil)
			Context(nil)
		}, `Context already defined for agent "agent"`},
		{"History first", func() {
			History(func() {
				KeepRecentTurns(2)
			})
			Context(nil)
		}, "Context cannot be combined with History"},
		{"Context first", func() {
			Context(nil)
			History(func() {
				KeepRecentTurns(2)
			})
		}, "Context cannot be combined with History"},
		{"zero trigger", func() {
			Context(func() {
				TriggerAt(0)
			})
		}, "TriggerAt requires 0 < t < 1 with at most 4 decimal places"},
		{"one trigger", func() {
			Context(func() {
				TriggerAt(1)
			})
		}, "TriggerAt requires 0 < t < 1 with at most 4 decimal places"},
		{"precision", func() {
			Context(func() {
				TriggerAt(0.85001)
			})
		}, "TriggerAt requires 0 < t < 1 with at most 4 decimal places"},
		{"zero retention", func() {
			Context(func() {
				KeepRecentTokens(0)
			})
		}, "KeepRecentTokens requires n > 0, got 0"},
		{"duplicate TriggerAt", func() {
			Context(func() {
				TriggerAt(.85)
				TriggerAt(.8)
			})
		}, "TriggerAt already defined"},
		{"duplicate KeepRecentTokens", func() {
			Context(func() {
				KeepRecentTokens(10)
				KeepRecentTokens(20)
			})
		}, "KeepRecentTokens already defined"},
		{"duplicate Summarize", func() {
			Context(func() {
				Summarize(func() {
					Instructions("keep")
				})
				Summarize(nil)
			})
		}, "Summarize already defined"},
		{"duplicate Recall", func() {
			Context(func() {
				Recall(false)
				Recall(true)
			})
		}, "Recall already defined"},
		{"duplicate Disable", func() {
			Context(func() {
				Disable()
				Disable()
			})
		}, "Disable already defined"},
		{"duplicate Instructions", func() {
			Context(func() {
				Summarize(func() {
					Instructions("keep")
					Instructions("again")
				})
			})
		}, "Instructions already defined"},
		{"duplicate Prompt", func() {
			Context(func() {
				Summarize(func() {
					Prompt("{{.Evicted}}")
					Prompt("{{.Evicted}}")
				})
			})
		}, "Prompt already defined"},
		{"disable trigger", func() {
			Context(func() {
				Disable()
				TriggerAt(.85)
			})
		}, "Disable cannot be combined with TriggerAt, KeepRecentTokens or Summarize"},
		{"disable retention", func() {
			Context(func() {
				KeepRecentTokens(20)
				Disable()
			})
		}, "Disable cannot be combined with TriggerAt, KeepRecentTokens or Summarize"},
		{"disable summary", func() {
			Context(func() {
				Summarize(func() {
					Instructions("keep")
				})
				Disable()
			})
		}, "Disable cannot be combined with TriggerAt, KeepRecentTokens or Summarize"},
		{"empty summary", func() {
			Context(func() {
				Summarize(nil)
			})
		}, "Summarize requires Instructions or Prompt"},
		{"empty instructions", func() {
			Context(func() {
				Summarize(func() {
					Instructions("")
				})
			})
		}, "Instructions requires non-empty text"},
		{"empty prompt", func() {
			Context(func() {
				Summarize(func() {
					Prompt("")
				})
			})
		}, "Prompt requires non-empty text"},
		{"prompt parse", func() {
			Context(func() {
				Summarize(func() {
					Prompt("{{")
				})
			})
		}, "invalid context summary Prompt: template: context.summary:1: unclosed action"},
		{"unknown field", func() {
			Context(func() {
				Summarize(func() {
					Prompt("{{.Evicted}} {{.Foo}}")
				})
			})
		}, `invalid context summary Prompt: template: context.summary:1:15: executing "context.summary" at <.Foo>: can't evaluate field Foo in type *agent.contextPromptProbe`},
		{"missing evicted", func() {
			Context(func() {
				Summarize(func() {
					Prompt("{{.Previous}}")
				})
			})
		}, "context summary Prompt must reference .Evicted"},
		{"ignored instructions", func() {
			Context(func() {
				Summarize(func() {
					Instructions("keep")
					Prompt("{{.Evicted}}")
				})
			})
		}, "context summary Prompt must reference .Instructions when Instructions is declared"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runDSLExpectError(t, contextDesign(func() {
				RunPolicy(tc.body)
			}), tc.want)
		})
	}
}

func TestContextDSLWrongParents(t *testing.T) {
	cases := []struct {
		name string
		fn   func()
	}{
		{"Context", func() {
			Context(nil)
		}},
		{"TriggerAt", func() {
			TriggerAt(.85)
		}},
		{"KeepRecentTokens", func() {
			KeepRecentTokens(20)
		}},
		{"Summarize", func() {
			Summarize(nil)
		}},
		{"Recall", func() {
			Recall(false)
		}},
		{"Disable", Disable},
		{"Instructions", func() {
			Instructions("keep")
		}},
		{"Prompt", func() {
			Prompt("{{.Evicted}}")
		}},
		{"KeepResultInContext", KeepResultInContext},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runDSLExpectError(t, tc.fn, "invalid use of "+tc.name)
		})
	}
}

func TestContextDSLToolAndWorkflowValidation(t *testing.T) {
	cases := []struct {
		name string
		body func()
		want string
	}{
		{"workflow", func() {
			RunPolicy(func() {
				Context(nil)
			})
			Workflow(func() {
				Step("one", "tools.one", "{}")
			})
		}, "Context requires a planner agent and cannot be combined with Workflow"},
		{"bookkeeping first", func() {
			Use("tools", func() {
				Tool("one", "one", func() {
					Bookkeeping()
					KeepResultInContext()
				})
			})
		}, "KeepResultInContext cannot be combined with Bookkeeping"},
		{"keep first", func() {
			Use("tools", func() {
				Tool("one", "one", func() {
					KeepResultInContext()
					Bookkeeping()
				})
			})
		}, "KeepResultInContext cannot be combined with Bookkeeping"},
		{"duplicate keep", func() {
			Use("tools", func() {
				Tool("one", "one", func() {
					KeepResultInContext()
					KeepResultInContext()
				})
			})
		}, "KeepResultInContext already defined"},
		{"recall name", func() {
			RunPolicy(func() {
				Context(nil)
			})
			Use("tools", func() {
				Tool("my_recall_context_tool", "one", func() {

				})
			})
		}, "Context reserves tool names containing recall_context"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runDSLExpectError(t, contextDesign(tc.body), tc.want)
		})
	}
}

func TestContextDSLValues(t *testing.T) {
	cases := []struct {
		name        string
		fn          func()
		set, recall bool
	}{
		{"unset", func() {

		}, false, false},
		{"true", func() {
			Recall(true)
		}, true, true},
		{"false", func() {
			Recall(false)
		}, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runDSL(t, contextDesign(func() {
				RunPolicy(func() {
					Context(func() {
						TriggerAt(.85)
						tc.fn()
					})
				})
			}))
			c := agentsexpr.Root.Agents[0].RunPolicy.Context
			assert.Equal(t, &agentsexpr.Rate{Num: 85, Den: 100}, c.TriggerAt)
			assert.Equal(t, tc.set, c.RecallSet)
			assert.Equal(t, tc.recall, c.Recall)
		})
	}
	t.Run("disabled with recall", func(t *testing.T) {
		runDSL(t, contextDesign(func() {
			RunPolicy(func() {
				Context(func() {
					Disable()
					Recall(false)
				})
			})
		}))
		assert.True(t, agentsexpr.Root.Agents[0].RunPolicy.Context.Disabled)
	})
	t.Run("full prompt", func(t *testing.T) {
		runDSL(t, contextDesign(func() {
			RunPolicy(func() {
				Context(func() {
					KeepRecentTokens(8000)
					Summarize(func() {
						Instructions("keep IDs")
						Prompt("{{.Previous}} {{.Evicted}} {{.Instructions}} {{.Budget}}")
					})
				})
			})
		}))
	})
}

func TestContextPromptFieldAccess(t *testing.T) {
	cases := []struct {
		name, source, instructions, want string
	}{
		{"root alias", "{{$x := .}}{{$x.Evicted}}", "", ""},
		{"chain", "{{(.).Evicted}}", "", ""},
		{"invoked definition", `{{define "part"}}{{.Evicted}}{{end}}{{template "part" .}}`, "", ""},
		{"instructions alias", "{{$x := .}}{{$x.Evicted}} {{$x.Instructions}}", "keep IDs", ""},
		{"uninvoked evicted", `{{define "part"}}{{.Evicted}}{{end}}unused`, "", "context summary Prompt must reference .Evicted"},
		{"uninvoked instructions", `{{.Evicted}}{{define "part"}}{{.Instructions}}{{end}}`, "keep IDs", "context summary Prompt must reference .Instructions when Instructions is declared"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			design := contextDesign(func() {
				RunPolicy(func() {
					Context(func() {
						Summarize(func() {
							if tc.instructions != "" {
								Instructions(tc.instructions)
							}
							Prompt(tc.source)
						})
					})
				})
			})
			if tc.want != "" {
				runDSLExpectError(t, design, tc.want)
			} else {
				runDSL(t, design)
			}
		})
	}
}

func contextDesign(body func()) func() {
	return func() {
		API("test", func() {

		})
		Service("svc", func() {
			Agent("agent", "desc", body)
		})
	}
}
