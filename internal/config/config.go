package config

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// Config is the root configuration for hydra-operator, populated from config.yaml and HYDRA_* env vars.
type Config struct {
	Log          Log          `mapstructure:"log"`
	Hydra        Hydra        `mapstructure:"hydra"`
	Reconciler   Reconciler   `mapstructure:"reconciler"`
	Capabilities Capabilities `mapstructure:"capabilities"`
	Metrics      Metrics      `mapstructure:"metrics"`
	Operator     Operator     `mapstructure:"operator"`
}

// Operator controls hydra-operator's own runtime identity — where it
// persists its cluster JWT and, for local development against a cluster
// it isn't running inside of, which kubeconfig to use.
type Operator struct {
	// Namespace is the operator's own namespace (where its token Secret
	// lives). Auto-detected from the in-cluster service account when
	// running as a pod; only needs setting explicitly for local dev.
	Namespace string `mapstructure:"namespace"`
	// TokenSecretName is the Secret holding the persisted cluster JWT
	// (internal/tokenstore).
	TokenSecretName string `mapstructure:"token_secret_name"`
	// Kubeconfig is a path to a kubeconfig file, used instead of the
	// in-cluster config — for local development against kind/k3d/CRC only.
	Kubeconfig string `mapstructure:"kubeconfig"`
}

// Log controls the structured logger output.
type Log struct {
	Level  string `mapstructure:"level"`
	Format string `mapstructure:"format"`
}

// Hydra holds the connection settings to the Hydra control plane API.
type Hydra struct {
	// URL is the base URL of the Hydra API (e.g. https://hydra.example.com).
	URL string `mapstructure:"url"`
	// ClusterID is the ClusterRef ID this operator instance registers/reconciles for.
	ClusterID string `mapstructure:"cluster_id"`
	// RegistrationToken is the one-time token used on first startup to exchange for a
	// long-lived cluster token (HYDRA_REGISTRATION_TOKEN). Never logged.
	RegistrationToken string `mapstructure:"registration_token"`
	// HTTPTimeout bounds every request to the Hydra API.
	HTTPTimeout time.Duration `mapstructure:"http_timeout"`
}

// Reconciler controls the desired-state pull and heartbeat loop.
type Reconciler struct {
	// SyncInterval is how often the operator pulls GET /clusters/:id/desired-state.
	SyncInterval time.Duration `mapstructure:"sync_interval"`
	// HeartbeatInterval is how often the operator posts /clusters/:id/heartbeat.
	HeartbeatInterval time.Duration `mapstructure:"heartbeat_interval"`
}

// Capabilities controls the cluster capability detector (Knative, Kourier, Gatekeeper, Kata, ...).
type Capabilities struct {
	// CheckInterval is how often capabilities are re-detected.
	CheckInterval time.Duration `mapstructure:"check_interval"`
	// ProbeTimeout bounds each individual capability probe against the K8s API.
	ProbeTimeout time.Duration `mapstructure:"probe_timeout"`
}

// Metrics controls the internal Prometheus/health server.
type Metrics struct {
	// Port serves /metrics (Prometheus) and /healthz, /readyz.
	Port int `mapstructure:"port"`
}

// Load reads configuration from the given file path (or discovers config.yaml if empty),
// applies HYDRA_* environment variable overrides, and validates the result.
func Load(path string) (*Config, error) {
	v := viper.New()

	v.SetDefault("log.level", "info")
	v.SetDefault("log.format", "json")
	v.SetDefault("hydra.http_timeout", "30s")
	v.SetDefault("reconciler.sync_interval", "30s")
	v.SetDefault("reconciler.heartbeat_interval", "30s")
	v.SetDefault("capabilities.check_interval", "5m")
	v.SetDefault("capabilities.probe_timeout", "10s")
	v.SetDefault("metrics.port", 8080)
	v.SetDefault("operator.namespace", "")
	v.SetDefault("operator.token_secret_name", "hydra-operator-token")
	v.SetDefault("operator.kubeconfig", "")

	v.SetEnvPrefix("HYDRA")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	if path != "" {
		v.SetConfigFile(path)
	} else {
		v.SetConfigName("config")
		v.SetConfigType("yaml")
		v.AddConfigPath(".")
		v.AddConfigPath("/etc/hydra-operator")
	}

	if err := v.ReadInConfig(); err != nil {
		var notFound viper.ConfigFileNotFoundError
		if !errors.As(err, &notFound) && path != "" {
			return nil, fmt.Errorf("reading config file: %w", err)
		}
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unmarshalling config: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	return &cfg, nil
}

// Validate returns an error if any required configuration value is missing or out of range.
func (c *Config) Validate() error {
	if c.Hydra.URL == "" {
		return errors.New("hydra.url is required (env HYDRA_URL)")
	}
	if c.Hydra.ClusterID == "" {
		return errors.New("hydra.cluster_id is required (env HYDRA_CLUSTER_ID)")
	}

	knownLevels := map[string]bool{"trace": true, "debug": true, "info": true, "warn": true, "error": true, "fatal": true}
	if !knownLevels[strings.ToLower(c.Log.Level)] {
		return fmt.Errorf("log.level %q is not valid (trace|debug|info|warn|error|fatal)", c.Log.Level)
	}

	knownFormats := map[string]bool{"json": true, "console": true}
	if !knownFormats[strings.ToLower(c.Log.Format)] {
		return fmt.Errorf("log.format %q is not valid (json|console)", c.Log.Format)
	}

	if c.Metrics.Port < 1 || c.Metrics.Port > 65535 {
		return fmt.Errorf("metrics.port %d out of range [1, 65535]", c.Metrics.Port)
	}

	return nil
}
