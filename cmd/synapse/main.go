// Command synapse is the declarative webhook router from Gitea to an executor.
//
// It loads configuration, starts the HTTP server and runs until interrupted.
// The webhook pipeline (HMAC, parsing, routing) is added in later phases; this
// entry point owns only process startup, signal handling and shutdown.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"go.oxef.dev/ci/synapse/internal/config"
	"go.oxef.dev/ci/synapse/internal/server"
	"go.oxef.dev/ci/synapse/internal/version"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "synapse:", err)
		os.Exit(1)
	}
}

func run() error {
	configPath := flag.String("config", os.Getenv("SYNAPSE_CONFIG"), "path to YAML config file")
	showVersion := flag.Bool("version", false, "print build info and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("synapse %s\n", version.Human())
		return nil
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: cfg.Log.SlogLevel()}))
	logger.Info("starting synapse", "version", version.Technical())

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return server.New(cfg, logger).Run(ctx)
}
