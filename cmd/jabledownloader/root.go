package main

import (
	"github.com/spf13/cobra"

	"github.com/jooservices/go-jabledownloader/internal/media"
	"github.com/jooservices/go-jabledownloader/internal/media/translate"
)

// options holds the persistent flags of one command tree.
type options struct {
	outDir, pathTemplate, quality                                        string
	subtitleMode, subtitleLang, translator, whisperModel, spokenLanguage string
	workers                                                              int
	dryRun, yes, quiet, noColor, verbose, force, subtitle                bool
}

func newRootCmd(d deps) *cobra.Command {
	o := &options{}
	root := &cobra.Command{
		Use:           "jabledownloader",
		Short:         "Discover and download videos from supported sites",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetIn(d.stdin)
	root.SetOut(d.stdout)
	root.SetErr(d.stderr)

	f := root.PersistentFlags()
	f.StringVarP(&o.outDir, "out", "o", "", "Output directory (default: ./videos)")
	f.StringVar(&o.pathTemplate, "path-template", "", "Per-video directory under --out, from {site} and {code} (default: {site}/{code})")
	f.IntVarP(&o.workers, "workers", "w", 0, "Number of concurrent transfers (default: 16)")
	f.BoolVar(&o.dryRun, "dry-run", false, "Preview what would be downloaded without downloading")
	f.BoolVarP(&o.yes, "yes", "y", false, "Skip the interactive picker and confirmation prompts")
	f.BoolVarP(&o.quiet, "quiet", "q", false, "Only print results")
	f.BoolVar(&o.noColor, "no-color", false, "Disable ANSI colors")
	f.BoolVarP(&o.verbose, "verbose", "v", false, "Verbose output (inputs, source URLs, codec)")
	f.BoolVarP(&o.force, "force", "f", false, "Re-download even if the video file already exists")
	f.StringVar(&o.quality, "quality", "best", "Max video height: best, 240, 360, 480, 720, 1080")
	f.BoolVar(&o.subtitle, "subtitle", false, "Add subtitles (host mlx_whisper + ffmpeg)")
	f.StringVar(&o.subtitleMode, "subtitle-mode", "soft", "Subtitle style: soft (separate track) or hard (burn-in)")
	f.StringVar(&o.subtitleLang, "subtitle-lang", "en", "Subtitle language (ISO 639 code, e.g. en, vi)")
	f.StringVar(&o.translator, "translator", media.TranslatorAuto, "Subtitle translator; auto lets Whisper translate to English directly")
	f.StringVar(&o.whisperModel, "whisper-model", "", "mlx_whisper model (default: mlx-community/whisper-medium)")
	f.StringVar(&o.spokenLanguage, "spoken-language", "ja", "Spoken language hint for Whisper (empty = auto-detect)")
	_ = root.RegisterFlagCompletionFunc("translator", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return append([]string{media.TranslatorAuto}, translate.Default.Names()...), cobra.ShellCompDirectiveNoFileComp
	})

	root.AddGroup(
		&cobra.Group{ID: "download", Title: "Download:"},
		&cobra.Group{ID: "discovery", Title: "Discovery:"},
		&cobra.Group{ID: "self", Title: "Self-management:"},
	)
	root.AddCommand(
		newDownloadCmd(o, d),
		newGetCmd(o, d),
		newLatestCmd(o, d),
		newSearchCmd(o, d),
		newSiteCmd(o, d),
		newUpdateCmd(d),
		newConfigCmd(),
		newCompletionCmd(root),
	)
	return root
}
