package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/rs/zerolog"

	"github.com/ffurlanetto/hydra-operator/internal/capabilities"
	"github.com/ffurlanetto/hydra-operator/internal/config"
	"github.com/ffurlanetto/hydra-operator/internal/hydraclient"
	"github.com/ffurlanetto/hydra-operator/internal/k8sclient"
	"github.com/ffurlanetto/hydra-operator/internal/logging"
	"github.com/ffurlanetto/hydra-operator/internal/reconciler"
	"github.com/ffurlanetto/hydra-operator/internal/tokenstore"
	"github.com/ffurlanetto/hydra-operator/internal/version"
)

// errKnativeNotAvailable is returned by checkKnativeAvailable when the
// target cluster lacks Knative Serving — hydra-operator's only execution
// primitive (ADR-025). This is a permanent misconfiguration, not a
// transient failure: main() treats it as fatal so the Pod's restart count
// makes the problem visible immediately, rather than running an operator
// that would fail every container reconciliation forever.
var errKnativeNotAvailable = errors.New("knative serving not detected on this cluster (serving.knative.dev API group missing) — install Knative Serving before deploying hydra-operator, see docs/operator/architecture.md")

// inClusterNamespaceFile is where the Kubernetes service account projects
// the pod's own namespace — the standard way an in-cluster workload learns
// where it's running without being told explicitly.
const inClusterNamespaceFile = "/var/run/secrets/kubernetes.io/serviceaccount/namespace"

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

	var clients *k8sclient.Clients
	if cfg.Operator.Kubeconfig != "" {
		clients, err = k8sclient.NewFromKubeconfig(cfg.Operator.Kubeconfig)
	} else {
		clients, err = k8sclient.NewInCluster()
	}
	if err != nil {
		log.Fatal().Err(err).Msg("failed to build Kubernetes clients")
	}

	var ready atomic.Bool
	healthSrv := startHealthServer(cfg.Metrics.Port, log, &ready)
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = healthSrv.Shutdown(shutdownCtx)
	}()

	capsDetector := capabilities.New(clients.Core, cfg.Capabilities.ProbeTimeout)
	if err := checkKnativeAvailable(ctx, capsDetector); err != nil {
		log.Fatal().Err(err).Msg("startup preflight check failed")
	}
	ready.Store(true)

	operatorNamespace := cfg.Operator.Namespace
	if operatorNamespace == "" {
		operatorNamespace = detectInClusterNamespace(log)
	}

	tokens := tokenstore.New(clients.Core, operatorNamespace, cfg.Operator.TokenSecretName)
	hydra := hydraclient.New(cfg.Hydra.URL, cfg.Hydra.ClusterID, cfg.Hydra.HTTPTimeout)

	token, err := tokens.Load(ctx)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to load persisted cluster token")
	}
	if token == "" {
		if cfg.Hydra.RegistrationToken == "" {
			log.Fatal().Msg("no persisted cluster token and hydra.registration_token is not set — cannot register")
		}
		token, err = hydra.Register(ctx, cfg.Hydra.RegistrationToken)
		if err != nil {
			log.Fatal().Err(err).Msg("registration failed")
		}
		if err := tokens.Save(ctx, token); err != nil {
			log.Fatal().Err(err).Msg("failed to persist cluster token after registration")
		}
		log.Info().Msg("registered with Hydra and persisted cluster token")
	} else {
		hydra.SetToken(token)
		log.Info().Msg("loaded persisted cluster token")
	}

	manager := reconciler.NewManager(reconciler.ManagerConfig{
		Hydra:             hydra,
		Namespaces:        reconciler.NewNamespaceReconciler(clients.Core),
		Containers:        reconciler.NewContainerReconciler(clients.Knative),
		Routes:            reconciler.NewRouteReconciler(clients.Route),
		Domains:           reconciler.NewDomainReconciler(clients.Knative),
		Policy:            reconciler.NewPolicyReconciler(clients.Dynamic),
		Capabilities:      capsDetector,
		Log:               log,
		OperatorVersion:   version.String(),
		SyncInterval:      cfg.Reconciler.SyncInterval,
		HeartbeatInterval: cfg.Reconciler.HeartbeatInterval,
	})

	go manager.Run(ctx)

	<-ctx.Done()
	log.Info().Msg("hydra-operator shutting down")
}

// detectInClusterNamespace reads the namespace the service account projects
// into every pod, falling back to "default" if unreadable (local dev
// without an explicit operator.namespace override).
func detectInClusterNamespace(log zerolog.Logger) string {
	data, err := os.ReadFile(inClusterNamespaceFile)
	if err != nil {
		log.Warn().Err(err).Msg("could not read in-cluster namespace file — defaulting operator.namespace to \"default\"")
		return "default"
	}
	return strings.TrimSpace(string(data))
}

// newHealthMux builds the /healthz + /readyz handler. Separated from
// startHealthServer so tests can exercise it via httptest.NewServer instead
// of binding a real port.
func newHealthMux(ready *atomic.Bool) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		if !ready.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	return mux
}

// startHealthServer serves /healthz and /readyz on port for K8s liveness/
// readiness probes. /healthz is always 200 once the process is up; /readyz
// reflects ready (503 until the caller flips it, e.g. once startup
// preflight checks like checkKnativeAvailable have passed). Prometheus
// /metrics is left for a future pass.
func startHealthServer(port int, log zerolog.Logger, ready *atomic.Bool) *http.Server {
	srv := &http.Server{Addr: fmt.Sprintf(":%d", port), Handler: newHealthMux(ready)}
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error().Err(err).Msg("health server failed")
		}
	}()
	return srv
}

// checkKnativeAvailable probes the cluster once at startup and returns
// errKnativeNotAvailable if Knative Serving isn't installed. Separated from
// main() so it can be tested without exiting the process.
func checkKnativeAvailable(ctx context.Context, detector *capabilities.Detector) error {
	result, err := detector.Detect(ctx)
	if err != nil {
		return fmt.Errorf("detect cluster capabilities: %w", err)
	}
	if !result.Capabilities.KnativeAvailable {
		return errKnativeNotAvailable
	}
	return nil
}
