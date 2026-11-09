package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"

	"github.com/jooservices/go-jabledownloader/internal/app"
	"github.com/jooservices/go-jabledownloader/internal/config"
	"github.com/jooservices/go-jabledownloader/internal/domain"
	"github.com/jooservices/go-jabledownloader/internal/media"
	"github.com/jooservices/go-jabledownloader/internal/monitor"
	"github.com/jooservices/go-jabledownloader/internal/notify"
	"github.com/jooservices/go-jabledownloader/internal/platform/httpx"
	"github.com/jooservices/go-jabledownloader/internal/server"
	"github.com/jooservices/go-jabledownloader/internal/store"
)

// Environment variables of the daemon.
const (
	tokenEnv = "JABLEDOWNLOADER_API_TOKEN"
	addrEnv  = "JABLEDOWNLOADER_API_ADDR"
)

type daemonFlags struct {
	addr, token, state string
	interval           time.Duration
	concurrency        int
	noMonitor          bool
	noSubtitle         bool
}

func addDaemonFlags(cmd *cobra.Command, f *daemonFlags) {
	fl := cmd.Flags()
	fl.StringVar(&f.state, "state", "", "State file for subscriptions, seen items, and jobs (default: next to the config file)")
	fl.DurationVar(&f.interval, "interval", monitor.DefaultInterval, "How often each subscription is checked for new videos")
	fl.IntVar(&f.concurrency, "concurrency", 1, "Downloads running at once")
	fl.BoolVar(&f.noMonitor, "no-monitor", false, "Serve the API only; do not poll subscriptions or run downloads")
	fl.BoolVar(&f.noSubtitle, "no-subtitle", false, "Do not add subtitles (they are on by default when the host supports them)")
}

func newServeCmd(o *options, d deps) *cobra.Command {
	var f daemonFlags
	cmd := &cobra.Command{
		Use:     "serve",
		Short:   "Run 24/7: HTTP API + MCP endpoint, monitoring subscriptions, auto-downloading new videos",
		GroupID: "daemon",
		Example: "  jabledownloader serve\n" +
			"  jabledownloader serve --addr 0.0.0.0:8787 --token $TOKEN --out /data\n" +
			"  TELEGRAM_BOT_TOKEN=... TELEGRAM_CHAT_ID=... jabledownloader serve",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runDaemon(cmd.Context(), o, d, f, false)
		},
	}
	cmd.Flags().StringVar(&f.addr, "addr", "", "Listen address (default 127.0.0.1:8787; env "+addrEnv+")")
	cmd.Flags().StringVar(&f.token, "token", "", "API bearer token (env "+tokenEnv+"); required off loopback")
	addDaemonFlags(cmd, &f)
	return cmd
}

func newMCPCmd(o *options, d deps) *cobra.Command {
	var f daemonFlags
	cmd := &cobra.Command{
		Use:     "mcp",
		Short:   "Run an MCP server on stdin/stdout so an AI assistant can search and download",
		GroupID: "daemon",
		Long: "Speaks the Model Context Protocol over stdio. Register it in an MCP client, for example:\n\n" +
			`  {"mcpServers":{"jabledownloader":{"command":"jabledownloader","args":["mcp"]}}}`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runDaemon(cmd.Context(), o, d, f, true)
		},
	}
	addDaemonFlags(cmd, &f)
	return cmd
}

// runDaemon builds the shared core and runs the monitor plus either the HTTP
// server or the stdio MCP server until ctx ends.
func runDaemon(ctx context.Context, o *options, d deps, f daemonFlags, stdio bool) error {
	logw := d.stderr
	log := slog.New(slog.NewTextHandler(logw, &slog.HandlerOptions{Level: slog.LevelInfo}))

	// Downloads run unattended: no prompts, subtitles when the host can.
	o.yes, o.quiet = true, true
	o.subtitle = !f.noSubtitle
	if o.subtitle {
		if err := subtitleHostCheck(d.runner); err != nil {
			log.Warn("subtitles disabled on this host", "reason", err.Error())
			o.subtitle = false
		} else if _, err := newSubtitler(o, d.runner, io.Discard); err != nil {
			log.Warn("subtitles disabled on this host", "reason", err.Error())
			o.subtitle = false
		}
	}
	svc, cleanup, err := newService(o, d, "", true)
	if err != nil {
		return err
	}
	defer cleanup()
	svc.Reporter = logReporter{log: log}
	svc.Prompter = nil

	statePath := f.state
	if statePath == "" {
		statePath = config.StatePath()
	}
	st, err := store.Open(statePath)
	if err != nil {
		return err
	}

	_, telegramOff := notify.FromEnv(nil).(notify.Nop)
	telegramOn := !telegramOff
	client := httpx.NewClient(httpx.Options{Timeout: 20 * time.Second})
	mon := &monitor.Monitor{
		Store: st, Discoverer: svc, Downloader: svc,
		Notifier: notify.FromEnv(client), Log: log,
		Interval: f.interval, Concurrency: f.concurrency,
	}
	core := &server.Core{Backend: backend{svc: svc}, Queue: mon, Poller: mon, Store: st, Version: version}

	gctx, cancel := context.WithCancel(ctx)
	defer cancel()
	g := new(errgroup.Group)
	if !f.noMonitor {
		g.Go(func() error {
			if err := mon.Run(gctx); !errors.Is(err, context.Canceled) {
				return err
			}
			return nil
		})
	}
	if stdio {
		g.Go(func() error {
			err := core.ServeStdio(gctx, d.stdin, d.stdout)
			cancel() // a closed stdin ends the session
			return err
		})
	} else {
		cfg := server.HTTPConfig{Addr: firstNonEmpty(f.addr, os.Getenv(addrEnv)), Token: firstNonEmpty(f.token, os.Getenv(tokenEnv))}
		g.Go(func() error {
			return core.ListenAndServe(gctx, cfg, func(addr string) {
				log.Info("serving", "api", "http://"+addr, "mcp", "http://"+addr+"/mcp", "state", statePath,
					"monitor", !f.noMonitor, "telegram", telegramOn)
			})
		})
	}
	err = g.Wait()
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// backend adapts app.Service to the server's read-only Backend port.
type backend struct{ svc *app.Service }

func (b backend) Latest(ctx context.Context, sites []string, count int) ([]domain.Item, error) {
	res, err := b.svc.Latest(ctx, sites, 1, count)
	return res.Items, err
}

func (b backend) Search(ctx context.Context, sites []string, keyword string, count int) ([]domain.Item, error) {
	res, err := b.svc.Search(ctx, sites, keyword, 1, count)
	return res.Items, err
}

func (b backend) Inspect(ctx context.Context, input string) (*domain.Detail, error) {
	return b.svc.RunInspect(ctx, input)
}

// logReporter turns use-case events into structured log lines; per-chunk
// progress is dropped.
type logReporter struct{ log *slog.Logger }

func (r logReporter) Report(ev app.Event) {
	switch e := ev.(type) {
	case app.VideoResolved:
		r.log.Info("resolved", "site", e.Site, "code", e.Code, "dir", e.Dir)
	case app.VideoSkipped:
		r.log.Info("already downloaded", "code", e.Code, "path", e.Path)
	case app.VideoDownloaded:
		r.log.Info("downloaded", "code", e.Code, "path", e.Path, "bytes", e.Size)
	case app.GalleryDownloaded:
		r.log.Info("gallery downloaded", "code", e.Code, "dir", e.Dir, "saved", e.Saved, "skipped", e.Skipped, "failed", e.Failed)
	case app.SubtitleDone:
		r.log.Info("subtitles", "video", e.Video, "srt", e.SRT, "skipped", e.Skipped)
	case app.ItemFailed:
		r.log.Warn("item failed", "code", e.Code, "err", e.Err)
	case app.DiscoveryDone:
		if e.Err != nil {
			r.log.Warn("discovery failed", "site", e.Site, "err", e.Err)
		}
	}
}

// subtitleHostCheck reports why subtitles cannot run here: they need ffmpeg
// and a Whisper backend (mlx_whisper, or uvx to fetch it).
func subtitleHostCheck(r media.Runner) error {
	if _, err := r.LookPath("ffmpeg"); err != nil {
		return errors.New("ffmpeg not found")
	}
	for _, name := range []string{"mlx_whisper", "uvx"} {
		if _, err := r.LookPath(name); err == nil {
			return nil
		}
	}
	return errors.New("no Whisper backend found (mlx_whisper or uvx)")
}
