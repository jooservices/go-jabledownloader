// Command jabledownloader discovers and downloads videos from supported
// sites. This package is the composition root: it parses flags, wires the
// concrete sites, engines, subtitle pipeline, and terminal UI into the app
// use-cases, and maps errors to exit codes.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/jooservices/go-jabledownloader/internal/app"
)

// version is stamped at build time via
//
//	go build -ldflags "-X main.version=v1.2.3"
var version = "dev"

// Exit codes.
const (
	exitOK          = 0
	exitError       = 1
	exitPartial     = 2   // some videos or sites failed
	exitInterrupted = 130 // SIGINT/SIGTERM, by shell convention
)

func main() {
	os.Exit(run(os.Args[1:], productionDeps()))
}

func run(args []string, d deps) int {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return execute(ctx, args, d)
}

func execute(ctx context.Context, args []string, d deps) int {
	root := newRootCmd(d)
	root.SetArgs(args)
	err := root.ExecuteContext(ctx)
	switch code := exitCodeFor(err); code {
	case exitOK:
	case exitInterrupted:
		fmt.Fprintln(d.stderr, "Interrupted — finished parts are kept; re-run the same command to resume.")
		return code
	default:
		fmt.Fprintf(d.stderr, "Error: %v\n", err)
		return code
	}
	return exitOK
}

func exitCodeFor(err error) int {
	var plan *app.PlanError
	var discovery *app.DiscoveryError
	switch {
	case err == nil:
		return exitOK
	case errors.Is(err, context.Canceled):
		return exitInterrupted
	case errors.As(err, &plan) || errors.As(err, &discovery):
		return exitPartial
	}
	return exitError
}
