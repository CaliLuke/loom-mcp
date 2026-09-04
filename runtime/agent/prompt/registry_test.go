package prompt

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
)

func TestRegistryRegisterRejectsDuplicateID(t *testing.T) {
	t.Parallel()

	reg := NewRegistry(nil)
	spec := PromptSpec{
		ID:       "example.agent.system",
		AgentID:  "example.agent",
		Role:     PromptRoleSystem,
		Template: "hello",
	}
	if err := reg.Register(spec); err != nil {
		t.Fatalf("register first spec: %v", err)
	}
	err := reg.Register(spec)
	if !errors.Is(err, ErrDuplicatePromptSpec) {
		t.Fatalf("expected ErrDuplicatePromptSpec, got %v", err)
	}
}

func TestRegistryRenderReturnsBaselinePrompt(t *testing.T) {
	t.Parallel()

	reg := NewRegistry(nil)
	if err := reg.Register(PromptSpec{
		ID:       "example.agent.system",
		AgentID:  "example.agent",
		Role:     PromptRoleSystem,
		Template: "hello {{ .Name }}",
	}); err != nil {
		t.Fatalf("register spec: %v", err)
	}

	out, err := reg.Render(context.Background(), "example.agent.system", Scope{}, map[string]any{
		"Name": "operator",
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if out == nil {
		t.Fatal("expected prompt content")
	}
	if out.Text != "hello operator" {
		t.Fatalf("unexpected render output: %q", out.Text)
	}
	if out.Ref.ID != "example.agent.system" {
		t.Fatalf("unexpected prompt ref id: %q", out.Ref.ID)
	}
	expectedVersion := VersionFromTemplate("hello {{ .Name }}")
	if out.Ref.Version != expectedVersion {
		t.Fatalf("unexpected prompt ref version: got %q want %q", out.Ref.Version, expectedVersion)
	}
}

func TestRegistryRenderBaselineWithFuncMap(t *testing.T) {
	t.Parallel()

	reg := NewRegistry(nil)
	if err := reg.Register(PromptSpec{
		ID:       "example.agent.system",
		AgentID:  "example.agent",
		Role:     PromptRoleSystem,
		Template: "hello {{ upper .Name }}",
		Funcs: map[string]any{
			"upper": strings.ToUpper,
		},
	}); err != nil {
		t.Fatalf("register spec: %v", err)
	}
	out, err := reg.Render(context.Background(), "example.agent.system", Scope{}, map[string]any{
		"Name": "operator",
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if out.Text != "hello OPERATOR" {
		t.Fatalf("unexpected render output: %q", out.Text)
	}
}

func TestRegistryRenderReturnsScopedOverride(t *testing.T) {
	t.Parallel()

	store := NewInMemoryStore()
	reg := NewRegistry(store)
	if err := reg.Register(PromptSpec{
		ID:       "example.agent.system",
		AgentID:  "example.agent",
		Role:     PromptRoleSystem,
		Template: "baseline {{ .Name }}",
	}); err != nil {
		t.Fatalf("register spec: %v", err)
	}

	scope := Scope{
		SessionID: "sess_1",
		Labels: map[string]string{
			"account": "acme",
			"region":  "west",
		},
	}
	if err := store.Set(context.Background(), "example.agent.system", scope, "override {{ .Name }}", nil); err != nil {
		t.Fatalf("set override: %v", err)
	}

	out, err := reg.Render(context.Background(), "example.agent.system", scope, map[string]any{
		"Name": "operator",
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if out.Text != "override operator" {
		t.Fatalf("expected override render, got %q", out.Text)
	}
	expectedVersion := VersionFromTemplate("override {{ .Name }}")
	if out.Ref.Version != expectedVersion {
		t.Fatalf("unexpected override version: got %q want %q", out.Ref.Version, expectedVersion)
	}
}

func TestRegistryRenderInvalidOverrideTemplateFails(t *testing.T) {
	t.Parallel()

	store := NewInMemoryStore()
	reg := NewRegistry(store)
	if err := reg.Register(PromptSpec{
		ID:       "example.agent.system",
		AgentID:  "example.agent",
		Role:     PromptRoleSystem,
		Template: "baseline {{ .Name }}",
	}); err != nil {
		t.Fatalf("register spec: %v", err)
	}
	scope := Scope{SessionID: "sess_1"}
	if err := store.Set(context.Background(), "example.agent.system", scope, "{{", nil); err != nil {
		t.Fatalf("set override: %v", err)
	}
	_, err := reg.Render(context.Background(), "example.agent.system", scope, map[string]any{"Name": "operator"})
	if !errors.Is(err, ErrTemplateParse) {
		t.Fatalf("expected ErrTemplateParse, got %v", err)
	}
}

func TestRegistryRenderConcurrentSetStore(t *testing.T) {
	reg := NewRegistry(nil)
	if err := reg.Register(PromptSpec{
		ID:       "example.agent.system",
		AgentID:  "example.agent",
		Role:     PromptRoleSystem,
		Template: "hello",
	}); err != nil {
		t.Fatalf("register spec: %v", err)
	}

	store := NewInMemoryStore()
	start := make(chan struct{})
	errs := make(chan error, 1)
	var workers sync.WaitGroup
	workers.Add(2)
	go func() {
		defer workers.Done()
		<-start
		for i := 0; i < 1000; i++ {
			if i%2 == 0 {
				reg.SetStore(store)
			} else {
				reg.SetStore(nil)
			}
		}
	}()
	go func() {
		defer workers.Done()
		<-start
		for i := 0; i < 1000; i++ {
			if _, err := reg.Render(context.Background(), "example.agent.system", Scope{}, nil); err != nil {
				errs <- err
				return
			}
		}
	}()
	close(start)
	workers.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("render: %v", err)
	}
}

func TestRegistryRenderReturnsNotFound(t *testing.T) {
	t.Parallel()

	reg := NewRegistry(nil)
	_, err := reg.Render(context.Background(), "missing.prompt", Scope{}, nil)
	if !errors.Is(err, ErrPromptNotFound) {
		t.Fatalf("expected ErrPromptNotFound, got %v", err)
	}
}
