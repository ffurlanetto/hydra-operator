package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/ffurlanetto/hydra-operator/internal/config"
	"github.com/ffurlanetto/hydra-operator/internal/logging"
	"github.com/ffurlanetto/hydra-operator/internal/version"
)

func main() {
	var (
		configPath  = flag.String("config", "", "path to config file (default: config.yaml in CWD or /etc/hydra-operator)")
		showVersion = flag.Bool("version", false, "print version and exit")
	)
	flag.Parse()

	if *showVersion {
		fmt.Println(version.String())
		os.Exit(0)
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(1)
	}

	log := logging.New(cfg.Log.Level, cfg.Log.Format)
	log.Info().
		Str("version", version.String()).
		Str("cluster_id", cfg.Hydra.ClusterID).
		Str("hydra_url", cfg.Hydra.URL).
		Msg("hydra-operator starting")

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Registration, desired-state pull, heartbeat, and reconciliation loops are
	// introduced in the Epic 2 implementation (registration + reconciler manager).
	<-ctx.Done()
	log.Info().Msg("hydra-operator shutting down")
}
