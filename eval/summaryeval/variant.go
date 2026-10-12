package summaryeval

import (
	"context"
	"crypto/sha256"
	"fmt"
	"maps"
	"os"
	"slices"
	"strconv"
	"strings"
	"text/template"

	rtcompaction "github.com/CaliLuke/loom-mcp/v2/runtime/agent/compaction"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/prompt"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	agRuntime "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
)

type limitsClient struct {
	model.Client
	limits model.ContextLimits
	id     string
}

// ParseVariant validates the fixed-cut vocabulary and resolves named/file templates.
func ParseVariant(cfg Config, spec string) (Variant, error) {
	v := Variant{Prompt: promptRecorded}
	fields := map[string]string{}
	for _, part := range strings.Split(spec, ",") {
		key, value, ok := strings.Cut(part, "=")
		if !ok || value == "" {
			return v, fmt.Errorf("%w: expected key=value", ErrUsage)
		}
		if _, exists := fields[key]; exists {
			return v, fmt.Errorf("%w: duplicate variant key %s", ErrUsage, key)
		}
		fields[key] = value
		if err := setVariantField(cfg, &v, key, value); err != nil {
			return v, err
		}
	}
	if err := resolveVariantPrompt(cfg, &v); err != nil {
		return v, err
	}
	var canonical []string
	for _, key := range slices.Sorted(maps.Keys(fields)) {
		canonical = append(canonical, key+"="+fields[key])
	}
	v.ID = strings.Join(canonical, ",")
	return v, nil
}

func (c *limitsClient) ContextLimits(context.Context, *model.Request) (model.ContextLimits, string, bool) {
	return c.limits, c.id, c.limits.ContextWindow > 0
}

func variantInput(c Cut, baseline *agRuntime.SummarizeContextInput, v Variant, models map[string]Model, disabled bool) (Cut, *agRuntime.SummarizeContextInput, Model, VariantIdentity, error) {
	in := *baseline
	settings := in.ContextSettings
	templateText := string(c.record.Content[settings.Prompt.Template])
	if v.Prompt != promptRecorded {
		templateText = v.Template
	}
	hash := contentHash([]byte(templateText))
	// The recorded control retains its prompt identity and fingerprint so its
	// checkpoint can reproduce the original hash exactly.
	if v.Prompt != promptRecorded {
		settings.Prompt = rtcompaction.SummaryPrompt{Ref: prompt.PromptRef{ID: in.PromptID, Version: fmt.Sprintf("k20:%x", sha256.Sum256([]byte(templateText)))[:16]}, Template: hash}
	}
	settings = variantSettings(settings, v)
	id := settings.Summary.Model.ID
	if v.Model != "" {
		id = v.Model
	}
	m, ok := models[id]
	if (!ok || m.Client == nil) && !disabled {
		return c, nil, m, VariantIdentity{}, fmt.Errorf("%w: unknown summary model %q", ErrUsage, id)
	}
	if v.Model != "" {
		settings.Summary.Model = rtcompaction.ContextModel{ID: id, Class: m.Class, AcceptsImages: m.AcceptsImages, AcceptsDocuments: m.AcceptsDocuments}
	}
	settings.Summary.Enabled = true
	in.ContextSettings = settings
	in.ContextModel = settings.Summary.Model
	in.Settings = settings.Summary.Settings
	canonical := rtcompaction.Canonical(settings)
	in.ContextFingerprint = string(contentHash(canonical))
	c = withVariantContent(c, hash, templateText, in.ContextFingerprint, canonical)
	if m.Limits != nil {
		if _, ok := m.Client.(model.ContextInfo); !ok {
			m.Client = &limitsClient{Client: m.Client, limits: *m.Limits, id: id}
		}
	}
	identity := VariantIdentity{ID: v.ID, Fingerprint: in.ContextFingerprint, Prompt: settings.Prompt.Ref, Template: hash, ModelID: id}
	return c, &in, m, identity, nil
}

func contentHash(data []byte) runstore.Hash {
	return runstore.Hash(fmt.Sprintf("sha256:%x", sha256.Sum256(data)))
}

func setVariantField(cfg Config, v *Variant, key, value string) error {
	switch key {
	case "prompt":
		v.Prompt = value
	case "model":
		v.Model = value
	case "instructions":
		text, ok := cfg.Instructions[value]
		if !ok {
			return fmt.Errorf("%w: unknown instructions %q", ErrUsage, value)
		}
		v.Instructions = &text
	case "max_summary_chunks", "max_summarised_result_tokens":
		n, err := strconv.Atoi(value)
		if err != nil || n <= 0 {
			return fmt.Errorf("%w: %s must be positive", ErrUsage, key)
		}
		if key == "max_summary_chunks" {
			v.MaxSummaryChunks = n
		} else {
			v.MaxSummarisedResultTokens = n
		}
	default:
		if strings.Contains(key, "window") || strings.Contains(key, "budget") || strings.HasPrefix(key, "keep") || strings.HasPrefix(key, "protect") || strings.HasPrefix(key, "trigger_at") || strings.HasPrefix(key, "min_saving") {
			return fmt.Errorf("%w: cut-moving variants are not in K20", ErrUsage)
		}
		return fmt.Errorf("%w: unknown variant key %q", ErrUsage, key)
	}
	return nil
}

func resolveVariantPrompt(cfg Config, v *Variant) error {
	if v.Prompt != promptRecorded {
		if strings.HasPrefix(v.Prompt, "@") {
			b, err := os.ReadFile(strings.TrimPrefix(v.Prompt, "@"))
			if err != nil {
				return err
			}
			v.Template = string(b)
		} else {
			text, ok := cfg.Prompts[v.Prompt]
			if !ok {
				return fmt.Errorf("%w: unknown prompt %q", ErrUsage, v.Prompt)
			}
			v.Template = text
		}
		if _, err := template.New(originSummary).Option("missingkey=error").Parse(v.Template); err != nil {
			return fmt.Errorf("%w: %w", ErrUsage, err)
		}
	}
	return nil
}

func variantSettings(settings rtcompaction.Settings, v Variant) rtcompaction.Settings {
	if v.Instructions != nil {
		settings.Summary.Settings.Instructions = *v.Instructions
	}
	if v.MaxSummaryChunks > 0 {
		settings.MaxSummaryChunks = v.MaxSummaryChunks
	}
	if v.MaxSummarisedResultTokens > 0 {
		settings.MaxSummarisedResultTokens = v.MaxSummarisedResultTokens
	}
	return settings
}

func withVariantContent(c Cut, hash runstore.Hash, templateText, fingerprint string, canonical []byte) Cut {
	copyRecord := *c.record
	copyRecord.Content = maps.Clone(c.record.Content)
	copyRecord.Content[hash] = []byte(templateText)
	copyRecord.Content[runstore.Hash(fingerprint)] = canonical
	c.record = &copyRecord
	return c
}
