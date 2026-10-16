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

// dispatchRegisteredExtension recognizes store selection only for registered commands.
// Other commands retain their existing flag parser and argument ownership.
func dispatchRegisteredExtension(ctx context.Context, args []string, cfg Config) (bool, error) {
	var common flags
	root := newFlags(cfg.Name, &common)
	if err := root.Parse(args); err != nil {
		return false, err
	}
	if err := validateExtensions(cfg.Extensions); err != nil {
		return true, usage(err)
	}
	rest := root.Args()
	if len(rest) == 0 {
		return false, nil
	}
	for _, extension := range cfg.Extensions {
		if rest[0] == commandHelp && len(rest) == 2 && rest[1] == extension.Name {
			return dispatchExtension(ctx, rest, cfg, common)
		}
		if rest[0] != extension.Name {
			continue
		}
		prefix := args[:len(args)-len(rest)]
		var selected flags
		if _, err := consumeExtensionOut(prefix, &selected); err != nil {
			return true, usage(err)
		}
		common.out = selected.out
		if len(prefix) > 0 && prefix[len(prefix)-1] == "--" {
			return dispatchExtension(ctx, rest, cfg, common)
		}
		remaining, err := consumeExtensionOut(rest[1:], &common)
		if err != nil {
			return true, usage(err)
		}
		return dispatchExtension(ctx, append([]string{extension.Name}, remaining...), cfg, common)
	}
	return false, nil
}

// consumeExtensionOut removes only --out before --, leaving extension flags untouched.
func consumeExtensionOut(args []string, common *flags) ([]string, error) {
	remaining := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			return append(remaining, args[i+1:]...), nil
		}
		value := ""
		switch {
		case arg == "--out" || arg == "-out":
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "--") {
				return nil, fmt.Errorf("--out requires a nonempty directory")
			}
			i++
			value = args[i]
		case strings.HasPrefix(arg, "--out="):
			value = strings.TrimPrefix(arg, "--out=")
		case strings.HasPrefix(arg, "-out="):
			value = strings.TrimPrefix(arg, "-out=")
		default:
			remaining = append(remaining, arg)
			continue
		}
		if value == "" {
			return nil, fmt.Errorf("--out requires a nonempty directory")
		}
		if common.out != "" && common.out != value {
			return nil, fmt.Errorf("conflicting --out directories %q and %q", common.out, value)
		}
		common.out = value
	}
	return remaining, nil
}

func dispatchExtension(ctx context.Context, args []string, cfg Config, common flags) (bool, error) {
	for _, extension := range cfg.Extensions {
		if len(args) == 2 && args[0] == commandHelp && args[1] == extension.Name {
			_, err := fmt.Fprintf(cfg.Stdout, "%s\nCommon store flag: --out DIR or --out=DIR may appear before the command or anywhere before --. Repeated values must agree. -- passes following arguments literally to the extension. Other arguments belong to the extension.\n", extension.Help)
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

func validateExtensions(extensions []Extension) error {
	seen := make(map[string]bool)
	for _, extension := range extensions {
		if extension.Name == "" || strings.ContainsAny(extension.Name, " /\t\r\n") || extension.Run == nil || seen[extension.Name] {
			return fmt.Errorf("invalid or duplicate extension %q", extension.Name)
		}
		switch extension.Name {
		case commandHelp, commandRun, commandCheck, commandShow, commandCompare, commandList, commandCompaction:
			return fmt.Errorf("extension shadows built-in command %q", extension.Name)
		}
		seen[extension.Name] = true
	}
	return nil
}
