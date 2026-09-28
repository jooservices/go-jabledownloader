package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jooservices/go-jabledownloader/internal/app"
	"github.com/jooservices/go-jabledownloader/internal/site"
	"github.com/jooservices/go-jabledownloader/internal/ui/cli"
)

type discoveryFlags struct {
	sites    []string
	page     int
	count    int
	view     string
	json     bool
	download bool
}

// discoveryScope selects the optional flags of a discovery command.
type discoveryScope struct {
	sites bool // --site: global commands across sites
	view  bool // --view: one site's list command
}

func addDiscoveryFlags(cmd *cobra.Command, flags *discoveryFlags, scope discoveryScope) {
	cmd.Flags().IntVarP(&flags.page, "page", "p", 1, "Result page")
	cmd.Flags().IntVarP(&flags.count, "count", "n", 10, "Maximum results per site")
	cmd.Flags().BoolVar(&flags.json, "json", false, "Print results as JSON")
	cmd.Flags().BoolVar(&flags.download, "download", false, "Select and download the listed results")
	if scope.sites {
		cmd.Flags().StringSliceVar(&flags.sites, "site", nil, "Limit to one or more sites ("+strings.Join(site.Names(), ", ")+")")
	}
	if scope.view {
		cmd.Flags().StringVar(&flags.view, "view", "latest", "Site list view")
	}
}

// discoveryCmd builds a discovery command; query runs the use-case.
func discoveryCmd(o *options, d deps, cmd *cobra.Command, scope discoveryScope,
	query func(*app.Service, *cobra.Command, []string, discoveryFlags) (app.DiscoveryResult, error),
) *cobra.Command {
	var flags discoveryFlags
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		svc, cleanup, err := newService(o, d, "", flags.json)
		if err != nil {
			return err
		}
		defer cleanup()
		result, discoveryErr := query(svc, cmd, args, flags)
		if err := renderItems(cmd.OutOrStdout(), cmd.ErrOrStderr(), result, flags.json, d.color && !o.noColor); err != nil {
			return err
		}
		if len(result.Items) > 0 && !flags.json && (flags.download || d.stdinTTY) {
			if err := svc.RunItems(cmd.Context(), result.Items); err != nil {
				return err
			}
		}
		return discoveryErr
	}
	addDiscoveryFlags(cmd, &flags, scope)
	return cmd
}

func newLatestCmd(o *options, d deps) *cobra.Command {
	return discoveryCmd(o, d, &cobra.Command{
		Use: "latest", Short: "List the latest videos from every site", GroupID: "discovery", Args: cobra.NoArgs,
	}, discoveryScope{sites: true}, func(svc *app.Service, cmd *cobra.Command, _ []string, f discoveryFlags) (app.DiscoveryResult, error) {
		return svc.Latest(cmd.Context(), f.sites, f.page, f.count)
	})
}

func newSearchCmd(o *options, d deps) *cobra.Command {
	return discoveryCmd(o, d, &cobra.Command{
		Use: "search <keyword>", Short: "Search every site by keyword", GroupID: "discovery", Args: cobra.MinimumNArgs(1),
	}, discoveryScope{sites: true}, func(svc *app.Service, cmd *cobra.Command, args []string, f discoveryFlags) (app.DiscoveryResult, error) {
		return svc.Search(cmd.Context(), f.sites, strings.Join(args, " "), f.page, f.count)
	})
}

func newSiteCmd(o *options, d deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "site <name>",
		Short:   "Run discovery for one site",
		GroupID: "discovery",
		Args:    cobra.ExactArgs(1),
		Example: "  jabledownloader site jable list --view hot\n  jabledownloader site eporner search keyword",
		RunE: func(_ *cobra.Command, args []string) error {
			return fmt.Errorf("site %q requires list or search; run 'jabledownloader site %s --help'", args[0], args[0])
		},
	}
	for _, name := range site.Names() {
		siteCmd := &cobra.Command{Use: name, Short: "Discovery actions for " + name, RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		}}
		siteCmd.AddCommand(
			discoveryCmd(o, d, &cobra.Command{Use: "list", Short: "List videos from " + name, Args: cobra.NoArgs}, discoveryScope{view: true},
				func(svc *app.Service, cmd *cobra.Command, _ []string, f discoveryFlags) (app.DiscoveryResult, error) {
					return svc.List(cmd.Context(), name, f.view, f.page, f.count)
				}),
			discoveryCmd(o, d, &cobra.Command{Use: "search <keyword>", Short: "Search " + name + " by keyword", Args: cobra.MinimumNArgs(1)}, discoveryScope{},
				func(svc *app.Service, cmd *cobra.Command, args []string, f discoveryFlags) (app.DiscoveryResult, error) {
					return svc.Search(cmd.Context(), []string{name}, strings.Join(args, " "), f.page, f.count)
				}),
		)
		cmd.AddCommand(siteCmd)
	}
	return cmd
}

// renderItems prints discovery rows grouped by site (or JSON) and lists
// failed sites on stderr.
func renderItems(out, errOut io.Writer, result app.DiscoveryResult, asJSON, color bool) error {
	if asJSON {
		data, err := json.MarshalIndent(result.Items, "", "  ")
		if err != nil {
			return err
		}
		fmt.Fprintln(out, string(data))
	} else {
		w := cli.NewStdWriter(out, color)
		lastSite := ""
		for i, item := range result.Items {
			if item.Site != lastSite {
				if lastSite != "" {
					w.Println()
				}
				w.Printf("%s%s%s\n", cli.ColorBold, item.Site, cli.ColorReset)
				lastSite = item.Site
			}
			duration := item.Duration
			if duration == "" {
				duration = "--:--"
			}
			w.Printf("  %s%2d%s  %s%-14s%s  %s  %s\n", cli.ColorCyan, i+1, cli.ColorReset, cli.ColorDim, item.Code, cli.ColorReset, duration, item.Title)
			w.Printf("       %s%s%s\n", cli.ColorDim, item.URL, cli.ColorReset)
		}
	}
	for _, failure := range result.Failures {
		fmt.Fprintf(errOut, "%s: %v\n", failure.Site, failure.Err)
	}
	return nil
}
