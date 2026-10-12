package summaryeval

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/eval/judge"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/prompt"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
)

type (
	// Config supplies named templates, instructions, gated models and application facts.
	Config struct {
		Prompts        map[string]string
		Instructions   map[string]string
		NewModels      func() (map[string]Model, error)
		Facts          func(context.Context, Cut) ([]Fact, error)
		ReferenceBytes int
	}
	// Model describes a summarizer. Limits supplies ContextInfo for clients lacking it.
	Model struct {
		Client                          model.Client
		Class                           model.ModelClass
		AcceptsImages, AcceptsDocuments bool
		Limits                          *model.ContextLimits
	}
	// Fact adds an application-specific retention claim.
	Fact struct {
		Claim    judge.Claim
		Category string
	}
	// Variant is a validated fixed-cut change; ID is its sorted canonical specification.
	Variant struct {
		ID                        string  `json:"id"`
		Prompt                    string  `json:"prompt"`
		Template                  string  `json:"template"`
		Instructions              *string `json:"instructions"`
		Model                     string  `json:"model"`
		MaxSummaryChunks          int     `json:"max_summary_chunks"`
		MaxSummarisedResultTokens int     `json:"max_summarised_result_tokens"`
	}
	// VariantIdentity identifies effective settings for a variant on one cut.
	VariantIdentity struct {
		ID          string           `json:"id"`
		Fingerprint string           `json:"fingerprint"`
		Prompt      prompt.PromptRef `json:"prompt"`
		Template    runstore.Hash    `json:"template"`
		ModelID     string           `json:"model_id"`
	}
	// Source names the original experiment/attempts or the exported file's digest.
	Source struct {
		Experiment   string   `json:"experiment,omitzero"`
		Attempts     []string `json:"attempts,omitzero"`
		ExportSHA256 string   `json:"export_sha256,omitzero"`
	}
	// Options configures one offline replay. Now and NewID support reproducible tests.
	Options struct {
		// OnPreflight runs after all verification/planning and before any provider calls.
		OnPreflight                    func(Preflight) error
		Dir                            string
		Source                         Source
		Cuts                           []Cut
		Variants                       []Variant
		Repeat, Concurrency, MaxTokens int
		Label                          string
		DryRun, Fresh                  bool
		Judge                          *judge.Judge
		Notice                         io.Writer
		Now                            func() time.Time
		NewID                          func() (string, error)
	}
	// Preflight records planned cut count, provider call upper bound and table-rate input estimate.
	Preflight struct {
		Cuts           int `json:"cuts"`
		CallUpperBound int `json:"call_upper_bound"`
		InputTokens    int `json:"input_tokens"`
	}
	// Header is the atomically replaced replay manifest.
	Header struct {
		Format   string            `json:"format"`
		Version  int               `json:"version"`
		ID       string            `json:"id"`
		Label    string            `json:"label"`
		Status   string            `json:"status"`
		Started  time.Time         `json:"started"`
		Finished time.Time         `json:"finished"`
		Source   Source            `json:"source"`
		Identity eval.Identity     `json:"identity"`
		Variants []VariantIdentity `json:"variants"`
		Judge    *eval.JudgeInfo   `json:"judge"`
		Planned  int               `json:"planned"`
		Tokens   int               `json:"tokens"`
		Calls    int               `json:"calls"`
	}
	// Judgment adds side and category to the standard eval judgment contract.
	Judgment struct {
		eval.StoredJudgment
		Side     string `json:"side"`
		Category string `json:"category"`
		NoCall   bool   `json:"no_call"`
	}
	// Line is one durable (cut, variant, repeat) result.
	Line struct {
		Key          string           `json:"key"`
		Variant      string           `json:"variant"`
		Repeat       int              `json:"repeat"`
		Cut          Cut              `json:"cut"`
		Verification Verification     `json:"verification"`
		Old          Side             `json:"old"`
		New          Side             `json:"new"`
		Judgments    []Judgment       `json:"judgments"`
		JudgeUsage   model.TokenUsage `json:"judge_usage"`
		NotRun       string           `json:"not_run"`
		Error        string           `json:"error"`
	}
	// Experiment is a replay manifest and its append-only result lines.
	Experiment struct {
		Header        Header    `json:"header"`
		Preflight     Preflight `json:"preflight"`
		Lines         []Line    `json:"lines"`
		TruncatedTail bool      `json:"truncated_tail"`
	}
)

// ErrUsage identifies invalid caller specifications, mapped to CLI exit 2.
var ErrUsage = errors.New("compaction usage")
