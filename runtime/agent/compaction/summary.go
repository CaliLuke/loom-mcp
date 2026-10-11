package compaction

import "github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"

type (
	// ContextModel identifies a registered summariser and accepted evidence.
	ContextModel struct {
		ID                              string
		Class                           model.ModelClass
		AcceptsImages, AcceptsDocuments bool
	}
	// SummarySettings customizes the baseline prompt.
	SummarySettings struct{ Instructions, Prompt string }
	// SummaryConfig is an immutable run-stamped snapshot, comparable by value.
	SummaryConfig struct {
		Enabled  bool
		Model    ContextModel
		Settings SummarySettings
	}
	// SummaryState counts non-stale summary rejections for this run.
	SummaryState struct {
		Rejections int
		Disabled   bool
	}
)

// Reject counts a summary rejection; stale results and the disable notification do not count.
func (s SummaryState) Reject(kind string) SummaryState {
	if kind == "" || kind == "stale" || kind == "summaries_disabled" || s.Disabled {
		return s
	}
	s.Rejections++
	s.Disabled = s.Rejections >= 3
	return s
}
