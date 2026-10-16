package opik

import (
	"context"
	"encoding/json/v2"
	"fmt"

	"github.com/CaliLuke/loom-mcp/v2/eval/evalcli"
)

// Extension enables the explicit `opik-export <experiment>` application command.
// Register it in evalcli.Config.Extensions; evaluation never invokes it itself.
// Common --out selects the store when placed before the command name.
func (c *Client) Extension() evalcli.Extension {
	return evalcli.Extension{Name: "opik-export", Help: "opik-export <experiment>: explicitly publish stored evidence using the configured target and application projection policy", Run: func(ctx context.Context, request evalcli.ExtensionRequest) error {
		if len(request.Args) != 1 {
			return fmt.Errorf("opik-export requires one stored experiment reference")
		}
		result, exportErr := c.ExportStored(ctx, request.Store, request.Args[0])
		if result != nil {
			if err := json.MarshalWrite(request.Stdout, result, json.Deterministic(true)); err != nil {
				return err
			}
			if _, err := fmt.Fprintln(request.Stdout); err != nil {
				return err
			}
		}
		return exportErr
	}}
}
