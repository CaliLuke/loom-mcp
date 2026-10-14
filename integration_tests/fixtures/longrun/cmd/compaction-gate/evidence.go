package main

import (
	"context"
	"fmt"

	suite "example.com/longrun/gate"
)

// evidencePreflight checks explicit inputs before paths or providers are opened.
func evidencePreflight(command string, o options) error {
	switch o.config {
	case "offline", "anthropic", "bedrock-anthropic", "openai", "gemini":
	default:
		return fmt.Errorf("unknown provider configuration %q", o.config)
	}
	switch command {
	case "inventory":
		if !o.declareComplete || o.out == "" {
			return fmt.Errorf("inventory requires --out and a complete-store declaration (--declare-complete-store)")
		}
	case "archive-v1", "freeze":
		if o.inventory == "" {
			return fmt.Errorf("%s requires --inventory", command)
		}
	}
	return nil
}

func evidence(ctx context.Context, command string, o options) (any, error) {
	if err := suite.VerifyEvidenceExecutable(ctx, o.dir); err != nil {
		return nil, err
	}
	switch command {
	case "inventory":
		inventory, err := suite.InventoryStore(o.dir, o.config, o.out, o.declareComplete)
		if err != nil {
			return nil, err
		}
		return inventory, suite.WriteInventory(o.dir, inventory)
	case "archive-v1":
		return suite.ArchiveCurrentV1(ctx, o.dir, o.config, o.inventory)
	case "source":
		if o.verify {
			return suite.ReadSource(ctx, o.dir, o.config)
		}
		return suite.WriteSource(ctx, o.dir, o.config, o.base)
	default:
		return nil, fmt.Errorf("unknown evidence command %q", command)
	}
}
