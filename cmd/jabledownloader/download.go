package main

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/jooservices/go-jabledownloader/internal/domain"
	"github.com/jooservices/go-jabledownloader/internal/ui/cli"
)

func newDownloadCmd(o *options, d deps) *cobra.Command {
	var name string
	cmd := &cobra.Command{
		Use:     "download <url|code>",
		Short:   "Download a single video by URL or code",
		GroupID: "download",
		Args:    cobra.ExactArgs(1),
		Example: "  jabledownloader download jur-827\n" +
			"  jabledownloader download https://en.jable.tv/videos/jur-827/\n" +
			"  jabledownloader download abf-382 --subtitle --subtitle-mode soft\n" +
			"  jabledownloader download abf-382 --subtitle --subtitle-lang vi --translator <name>\n" +
			"  jabledownloader download abf-382 --name my-file.mp4",
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, cleanup, err := newService(o, d, name, false)
			if err != nil {
				return err
			}
			defer cleanup()
			return svc.RunGet(cmd.Context(), args[0])
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "Custom output file name (default: <code>-<codec>.mp4)")
	return cmd
}

func newGetCmd(o *options, d deps) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:     "get <url|code>",
		Short:   "Show video info (URLs / JSON) without downloading",
		GroupID: "download",
		Args:    cobra.ExactArgs(1),
		Example: "  jabledownloader get jur-827\n  jabledownloader get jur-827 --json",
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, cleanup, err := newService(o, d, "", asJSON)
			if err != nil {
				return err
			}
			defer cleanup()
			info, err := svc.RunInspect(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return renderDetail(cmd.OutOrStdout(), info, asJSON, d.color && !o.noColor)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print the full detail as JSON")
	return cmd
}

// renderDetail prints a resolved video's sources, or the JSON contract.
func renderDetail(out io.Writer, info *domain.Detail, asJSON, color bool) error {
	if asJSON {
		data, err := json.MarshalIndent(info, "", "  ")
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(out, string(data))
		return err
	}
	w := cli.NewStdWriter(out, color)
	w.Printf("  %sCode:%s    %s\n", cli.ColorDim, cli.ColorReset, info.Code)
	w.Printf("  %sTitle:%s   %s\n", cli.ColorDim, cli.ColorReset, info.Title)
	w.Printf("  %sSources:%s %d\n", cli.ColorDim, cli.ColorReset, len(info.Sources))
	for _, src := range info.Sources {
		w.Printf("    %s\n", src.URL)
	}
	return nil
}
