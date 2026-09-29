package main

import (
	"fmt"
	"io"
	"runtime"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jooservices/go-jabledownloader/internal/app"
	"github.com/jooservices/go-jabledownloader/internal/config"
	"github.com/jooservices/go-jabledownloader/internal/ui/cli"
	"github.com/jooservices/go-jabledownloader/internal/update"
)

func newUpdateCmd(d deps) *cobra.Command {
	var checkOnly bool
	cmd := &cobra.Command{
		Use:     "update",
		Short:   "Check for updates and install the latest release",
		GroupID: "self",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runUpdate(cmd, d, checkOnly)
		},
	}
	cmd.Flags().BoolVar(&checkOnly, "check", false, "Only check for a newer version, do not install")
	return cmd
}

func runUpdate(cmd *cobra.Command, d deps, checkOnly bool) error {
	w := cli.NewStdWriter(cmd.OutOrStdout(), d.color)
	rel, err := d.latestRelease(cmd.Context())
	if err != nil {
		return fmt.Errorf("check for updates: %w\n  hint: check your connection; GitHub allows 60 unauthenticated requests/hour", err)
	}
	if rel.TagName == "" {
		return fmt.Errorf("latest release has no version tag")
	}
	w.Printf("  Current: %s\n  Latest:  %s\n", version, rel.TagName)
	if !update.IsNewer(version, rel.TagName) {
		w.Printf("  %s%s Already up to date.%s\n", cli.ColorGreen, cli.IconOk, cli.ColorReset)
		return nil
	}
	if checkOnly {
		w.Printf("  %sA newer version is available — run 'jabledownloader update' to install it.%s\n", cli.ColorYellow, cli.ColorReset)
		return nil
	}
	asset := rel.AssetFor()
	if asset == nil {
		return fmt.Errorf("no prebuilt binary for %s/%s in release %s — build from source instead", runtime.GOOS, runtime.GOARCH, rel.TagName)
	}
	w.Printf("  Downloading %s (%.1f MB)...\n", asset.Name, float64(asset.Size)/1e6)
	warnings, err := d.install(cmd.Context(), asset)
	if err != nil {
		return err
	}
	for _, warning := range warnings {
		w.Printf("  %swarning: %s%s\n", cli.ColorYellow, warning, cli.ColorReset)
	}
	w.Printf("  %s%s Updated to %s%s\n", cli.ColorGreen, cli.IconOk, rel.TagName, cli.ColorReset)
	return nil
}

// configKeys are the persisted settings `config get/set` understand.
const configKeys = "output_dir, path_template, worker_count"

func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "config",
		Short:   "Show or change persisted CLI settings",
		GroupID: "self",
		Example: "  jabledownloader config\n" +
			"  jabledownloader config set output_dir ~/Downloads/videos\n" +
			"  jabledownloader config set path_template \"{site}/{code}\"\n" +
			"  jabledownloader config set worker_count 8",
		RunE: func(cmd *cobra.Command, _ []string) error { return showConfig(cmd.OutOrStdout()) },
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "get [key]",
		Short: "Print the config file or one key (" + configKeys + ", path)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return showConfig(cmd.OutOrStdout())
			}
			return getConfig(cmd.OutOrStdout(), args[0])
		},
	}, &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Persist a config value (" + configKeys + ")",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return setConfig(cmd.OutOrStdout(), args[0], args[1])
		},
	})
	return cmd
}

func showConfig(out io.Writer) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "  Path:          %s\n", config.Path())
	fmt.Fprintf(out, "  output_dir:    %s\n", cfg.OutputDir)
	fmt.Fprintf(out, "  path_template: %s\n", cfg.PathTemplate)
	fmt.Fprintf(out, "  worker_count:  %d\n", cfg.WorkerCount)
	return nil
}

func getConfig(out io.Writer, key string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	switch strings.ToLower(key) {
	case "output_dir":
		fmt.Fprintln(out, cfg.OutputDir)
	case "path_template":
		fmt.Fprintln(out, cfg.PathTemplate)
	case "worker_count":
		fmt.Fprintln(out, cfg.WorkerCount)
	case "path":
		fmt.Fprintln(out, config.Path())
	default:
		return fmt.Errorf("unknown config key %q (supported: %s, path)", key, configKeys)
	}
	return nil
}

func setConfig(out io.Writer, key, value string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	switch strings.ToLower(key) {
	case "output_dir":
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("output_dir must not be empty")
		}
		cfg.OutputDir = value
	case "path_template":
		if err := app.ValidatePathTemplate(value); err != nil {
			return err
		}
		cfg.PathTemplate = value
	case "worker_count":
		n, err := strconv.Atoi(value)
		if err != nil || n <= 0 {
			return fmt.Errorf("worker_count must be a positive integer")
		}
		cfg.WorkerCount = n
	default:
		return fmt.Errorf("unknown config key %q (supported: %s)", key, configKeys)
	}
	if err := cfg.Save(); err != nil {
		return err
	}
	fmt.Fprintf(out, "  Saved %s=%s to %s\n", key, value, config.Path())
	return nil
}

func newCompletionCmd(root *cobra.Command) *cobra.Command {
	return &cobra.Command{
		Use:       "completion [bash|zsh|fish|powershell]",
		Short:     "Generate shell completion script",
		GroupID:   "self",
		Args:      cobra.ExactArgs(1),
		ValidArgs: []string{"bash", "zsh", "fish", "powershell"},
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			switch args[0] {
			case "bash":
				return root.GenBashCompletionV2(out, true)
			case "zsh":
				return root.GenZshCompletion(out)
			case "fish":
				return root.GenFishCompletion(out, true)
			case "powershell":
				return root.GenPowerShellCompletionWithDesc(out)
			}
			return fmt.Errorf("unsupported shell: %s (supported: bash, zsh, fish, powershell)", args[0])
		},
	}
}
