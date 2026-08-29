// Command jabledownloader downloads videos from Jable.TV.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jooservices/go-jabledownloader/internal/app"
	"github.com/jooservices/go-jabledownloader/internal/config"
	"github.com/jooservices/go-jabledownloader/internal/scraper"
	"github.com/jooservices/go-jabledownloader/internal/telemetry"
	"github.com/jooservices/go-jabledownloader/internal/ui"
	"github.com/jooservices/go-jabledownloader/internal/update"
)

// version is stamped at build time via
//
//	go build -ldflags "-X main.version=v1.2.3"
var version = "dev"

var rootFlags struct {
	outDir  string
	workers int
	dryRun  bool
	yes     bool
	quiet   bool
	noColor bool
	verbose bool
	force   bool
}

var latestFlags struct {
	count int
}

var hotFlags struct {
	count int
}

var updateFlags struct {
	checkOnly bool
}

// Exit codes.
const (
	exitOK      = 0
	exitError   = 1
	exitPartial = 2
)

func main() {
	os.Exit(run())
}

func run() int {
	ctx, cancel := app.SetupContext()
	defer cancel()

	rootCmd := newRootCmd()

	if err := rootCmd.ExecuteContext(ctx); err != nil {
		var partial *app.PlanError
		if errors.As(err, &partial) {
			return exitPartial
		}
		return exitError
	}
	return exitOK
}

func newRootCmd() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:           "jabledownloader",
		Short:         "Download videos from Jable.TV",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	rootCmd.PersistentFlags().StringVarP(&rootFlags.outDir, "out", "o", "", "Output directory (default: ./videos)")
	rootCmd.PersistentFlags().IntVarP(&rootFlags.workers, "workers", "w", 0, "Number of concurrent workers (default: 16)")
	rootCmd.PersistentFlags().BoolVar(&rootFlags.dryRun, "dry-run", false, "Preview what would be downloaded without downloading")
	rootCmd.PersistentFlags().BoolVarP(&rootFlags.yes, "yes", "y", false, "Skip the interactive picker and confirmation prompts")
	rootCmd.PersistentFlags().BoolVarP(&rootFlags.quiet, "quiet", "q", false, "Only print the final result")
	rootCmd.PersistentFlags().BoolVar(&rootFlags.noColor, "no-color", false, "Disable ANSI colors")
	rootCmd.PersistentFlags().BoolVarP(&rootFlags.verbose, "verbose", "v", false, "Verbose output (URLs, HLS links, codec)")
	rootCmd.PersistentFlags().BoolVarP(&rootFlags.force, "force", "f", false, "Re-download even if the video file already exists")

	rootCmd.AddGroup(&cobra.Group{ID: "download", Title: "Download:"})
	rootCmd.AddGroup(&cobra.Group{ID: "discovery", Title: "Discovery:"})
	rootCmd.AddGroup(&cobra.Group{ID: "self", Title: "Self-management:"})

	rootCmd.AddCommand(newGetCmd())
	rootCmd.AddCommand(newSearchCmd())
	rootCmd.AddCommand(newLatestCmd())
	rootCmd.AddCommand(newHotCmd())
	rootCmd.AddCommand(newUpdateCmd())
	rootCmd.AddCommand(newCompletionCmd(rootCmd))

	return rootCmd
}

func newGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "get <url|code>",
		Short:   "Download a single video by URL or code",
		GroupID: "download",
		Args:    cobra.ExactArgs(1),
		Example: "  jabledownloader get jur-827\n  jabledownloader get https://en.jable.tv/videos/jur-827/",
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, cleanup, err := newScrapeService(cmd)
			if err != nil {
				return err
			}
			defer cleanup()
			return svc.RunGet(cmd.Context(), args[0])
		},
	}
}

func newSearchCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "search <query>",
		Short:   "Search and download videos",
		GroupID: "discovery",
		Args:    cobra.MinimumNArgs(1),
		Example: "  jabledownloader search cute",
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, cleanup, err := newScrapeService(cmd)
			if err != nil {
				return err
			}
			defer cleanup()
			query := strings.Join(args, " ")
			return svc.RunMulti(cmd.Context(), "search: "+query, latestFlags.count,
				func(ctx context.Context, page int) ([]scraper.VideoEntry, error) {
					return svc.Client.SearchVideos(ctx, query, page)
				})
		},
	}
}

func newLatestCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "latest",
		Short:   "Download the latest videos",
		GroupID: "discovery",
		Example: "  jabledownloader latest --count 5",
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, cleanup, err := newScrapeService(cmd)
			if err != nil {
				return err
			}
			defer cleanup()
			return svc.RunMulti(cmd.Context(), "latest", latestFlags.count, svc.Client.LatestVideos)
		},
	}
	cmd.Flags().IntVarP(&latestFlags.count, "count", "n", 10, "Number of videos to download")
	return cmd
}

func newHotCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "hot",
		Short:   "Download the hot/trending videos",
		GroupID: "discovery",
		Example: "  jabledownloader hot --count 5",
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, cleanup, err := newScrapeService(cmd)
			if err != nil {
				return err
			}
			defer cleanup()
			return svc.RunMulti(cmd.Context(), "hot", hotFlags.count, svc.Client.HotVideos)
		},
	}
	cmd.Flags().IntVarP(&hotFlags.count, "count", "n", 10, "Number of videos to download")
	return cmd
}

func newUpdateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "update",
		Short:   "Check for updates and install the latest release",
		GroupID: "self",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runUpdate(cmd.Context())
		},
	}
	cmd.Flags().BoolVar(&updateFlags.checkOnly, "check", false, "Only check for a newer version, do not install")
	return cmd
}

func newCompletionCmd(root *cobra.Command) *cobra.Command {
	return &cobra.Command{
		Use:       "completion [bash|zsh|fish|powershell]",
		Short:     "Generate shell completion script",
		GroupID:   "self",
		Args:      cobra.ExactArgs(1),
		ValidArgs: []string{"bash", "zsh", "fish", "powershell"},
		RunE: func(_ *cobra.Command, args []string) error {
			switch args[0] {
			case "bash":
				return root.GenBashCompletionV2(os.Stdout, true)
			case "zsh":
				return root.GenZshCompletion(os.Stdout)
			case "fish":
				return root.GenFishCompletion(os.Stdout, true)
			case "powershell":
				return root.GenPowerShellCompletionWithDesc(os.Stdout)
			}
			return fmt.Errorf("unsupported shell: %s (supported: bash, zsh, fish, powershell)", args[0])
		},
	}
}

// newScrapeService assembles config, telemetry, a Chrome-backed scraper and
// the app service. The returned cleanup releases the browser.
func newScrapeService(cmd *cobra.Command) (*app.Service, func(), error) {
	svc, tel, err := baseService()
	if err != nil {
		return nil, func() {}, err
	}

	browser, err := scraper.NewBrowser(cmd.Context())
	if err != nil {
		return nil, func() {}, fmt.Errorf("launch browser: %w\n\n  Chrome/Chromium is required to bypass Cloudflare protection.\n  Install from: https://www.google.com/chrome/", err)
	}
	svc.Client = scraper.NewClient(browser)

	cleanup := func() {
		browser.Close()
		tel.Shutdown(cmd.Context())
	}
	return svc, cleanup, nil
}

// baseService loads config and telemetry shared by all commands.
func baseService() (*app.Service, *telemetry.T, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, nil, err
	}
	if rootFlags.workers > 0 {
		cfg.WorkerCount = rootFlags.workers
	}
	if rootFlags.outDir != "" {
		cfg.OutputDir = rootFlags.outDir
	}

	color := ui.ColorEnabled(os.Stdout) && !rootFlags.noColor
	out := ui.NewStdWriter(os.Stdout, color)

	obsCfg := telemetry.Config{
		Endpoint: strings.TrimSpace(os.Getenv("OBS_ENDPOINT")),
		Org:      envOr("OBS_ORG", "jooservices"),
		Stream:   envOr("OBS_STREAM", "jabledownloader"),
		User:     os.Getenv("OBS_USER"),
		Password: os.Getenv("OBS_PASSWORD"),
	}
	tel := telemetry.New(obsCfg)

	svc := &app.Service{
		Config: cfg,
		Out:    out,
		Tel:    tel,
		Opts: app.Options{
			DryRun:  rootFlags.dryRun,
			Yes:     rootFlags.yes,
			Quiet:   rootFlags.quiet,
			Verbose: rootFlags.verbose,
			Force:   rootFlags.force,
			TTY:     color,
			Workers: cfg.WorkerCount,
			OutDir:  cfg.OutputDir,
		},
	}
	return svc, tel, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func runUpdate(ctx context.Context) error {
	_, tel, err := baseService()
	if err != nil {
		return err
	}
	defer tel.Shutdown(ctx)

	rel, err := update.LatestRelease(ctx)
	if err != nil {
		return fmt.Errorf("check for updates: %w\n  hint: check your connection; GitHub allows 60 unauthenticated requests/hour", err)
	}
	latest := rel.TagName
	if latest == "" {
		return fmt.Errorf("latest release has no version tag")
	}

	fmt.Printf("  Current: %s\n  Latest:  %s\n", version, latest)

	if !update.IsNewer(version, latest) {
		fmt.Printf("  %s%s Already up to date.%s\n", ui.ColorGreen, ui.IconOk, ui.ColorReset)
		return nil
	}

	if updateFlags.checkOnly {
		fmt.Printf("  %sA newer version is available — run '%s' to install it.%s\n",
			ui.ColorYellow, "jabledownloader update", ui.ColorReset)
		return nil
	}

	asset := rel.AssetFor()
	if asset == nil {
		return fmt.Errorf("no prebuilt binary for %s/%s in release %s — build from source instead",
			runtime.GOOS, runtime.GOARCH, latest)
	}

	fmt.Printf("  Downloading %s (%.1f MB)...\n", asset.Name, float64(asset.Size)/1e6)
	if err := update.Install(ctx, asset); err != nil {
		return err
	}
	fmt.Printf("  %s%s Updated to %s%s\n", ui.ColorGreen, ui.IconOk, latest, ui.ColorReset)
	return nil
}
