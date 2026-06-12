package main

import (
	"flag"
	"fmt"
	"io"
)

// Version information — injected via ldflags at build time.
//
//	go build -ldflags="-X main.version=0.9.0 -X main.commit=abc1234 -X main.buildDate=2025-01-01T00:00:00Z"
var (
	version   = "dev"
	commit    = "unknown"
	buildDate = "unknown"
)

type config struct {
	root          string
	listen        string
	wsListen      string
	metricsListen string
	actor         string
	ansi          bool
	validate      bool
	dryRun        bool
	migrate       bool
	showVersion   bool
}

type validationError struct {
	errors int
}

func (e validationError) Error() string {
	return fmt.Sprintf("world validation reported %d errors", e.errors)
}

func parseFlags(args []string, stderr io.Writer) (config, error) {
	fs := flag.NewFlagSet("muhan-server", flag.ContinueOnError)
	fs.SetOutput(stderr)

	root := fs.String("root", ".", "legacy Muhan source/data root")
	sourceRoot := fs.String("source-root", "", "legacy Muhan source/data root (overrides -root)")
	listen := fs.String("listen", defaultListenAddr, "TCP listen address")
	wsListen := fs.String("ws-listen", "127.0.0.1:4041", "WebSocket listen address")
	metricsListen := fs.String("metrics-listen", ":2112", "Prometheus metrics listen address")
	actor := fs.String("actor", "", "temporary actor player ID for accepted sessions until login is ported")
	ansi := fs.Bool("ansi", true, "emit ANSI color sequences for clients")
	validate := fs.Bool("validate", false, "load and validate runtime inputs, then exit without listening")
	dryRun := fs.Bool("dry-run", false, "load runtime inputs, then exit without listening")
	migrate := fs.Bool("migrate-sidecars", false, "rewrite supported old JSON sidecar schemas before startup")
	showVersion := fs.Bool("version", false, "print version information and exit")

	if err := fs.Parse(args); err != nil {
		return config{}, err
	}
	if fs.NArg() != 0 {
		return config{}, fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	if *sourceRoot != "" {
		*root = *sourceRoot
	}
	return config{
		root:          *root,
		listen:        *listen,
		wsListen:      *wsListen,
		metricsListen: *metricsListen,
		actor:         *actor,
		ansi:          *ansi,
		validate:      *validate,
		dryRun:        *dryRun,
		migrate:       *migrate,
		showVersion:   *showVersion,
	}, nil
}
