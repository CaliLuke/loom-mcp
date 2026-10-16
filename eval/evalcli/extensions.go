package evalcli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/CaliLuke/loom-mcp/v2/eval"
)

type (
	// Extension is an explicitly registered application command. Core evalcli has
	// no dependency on optional exporters. Names cannot shadow built-in commands.
	Extension struct {
		Name string
		Help string
		Run  func(context.Context, ExtensionRequest) error
	}
	// ExtensionRequest supplies the selected local store and application I/O.
	// Args follow the command name; the extension owns their validation.
	ExtensionRequest struct {
		Args   []string
		Store  *eval.Store
		Stdout io.Writer
		Stderr io.Writer
	}
)

func dispatchExtension(ctx context.Context, args []string, cfg Config, common flags) (bool, error) {
	seen := make(map[string]bool)
	for _, extension := range cfg.Extensions {
		if extension.Name == "" || strings.ContainsAny(extension.Name, " /\t\r\n") || extension.Run == nil || seen[extension.Name] {
			return true, usage(fmt.Errorf("invalid or duplicate extension %q", extension.Name))
		}
		switch extension.Name {
		case commandHelp, commandRun, commandShow, commandCompare, commandList, commandCompaction:
			return true, usage(fmt.Errorf("extension shadows built-in command %q", extension.Name))
		}
		seen[extension.Name] = true
	}
	for _, extension := range cfg.Extensions {
		if len(args) == 2 && args[0] == commandHelp && args[1] == extension.Name {
			_, err := fmt.Fprintln(cfg.Stdout, extension.Help)
			return true, err
		}
		if len(args) == 0 || args[0] != extension.Name {
			continue
		}
		store, err := openStore(cfg, common)
		if err != nil {
			return true, err
		}
		return true, extension.Run(ctx, ExtensionRequest{Args: append([]string(nil), args[1:]...), Store: store, Stdout: cfg.Stdout, Stderr: cfg.Stderr})
	}
	return false, nil
}
