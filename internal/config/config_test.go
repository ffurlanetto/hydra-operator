package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLoad_EnvOnly_HydraFieldsBind_PopulatesConfig is a regression test for a
// real bug found via the first genuine end-to-end run against a live Hydra
// control plane: viper's AutomaticEnv() derives an env var name by joining
// SetEnvPrefix("HYDRA") with the dotted config key, so every key under the
// "hydra" config section collided with its own prefix — "hydra.url" would
// only ever bind from HYDRA_HYDRA_URL, never the documented HYDRA_URL. Since
// HYDRA_URL/HYDRA_CLUSTER_ID/HYDRA_REGISTRATION_TOKEN are exactly what
// deploy/base, helm/, and the README already configure the operator with,
// every real deployment silently failed Load()'s Validate() with
// "hydra.url is required" even when correctly configured — a missing
// v.SetDefault alone does not fix this; it needs the explicit v.BindEnv
// calls in Load() that map each hydra.* key back to its plain HYDRA_* name.
func TestLoad_EnvOnly_HydraFieldsBind_PopulatesConfig(t *testing.T) {
	t.Setenv("HYDRA_URL", "https://hydra.example.com")
	t.Setenv("HYDRA_CLUSTER_ID", "11f9143b-da50-4f00-b98b-79ff6b959e07")
	t.Setenv("HYDRA_REGISTRATION_TOKEN", "a-registration-token")
	t.Setenv("HYDRA_HTTP_TIMEOUT", "45s")

	cfg, err := Load("")

	require.NoError(t, err)
	assert.Equal(t, "https://hydra.example.com", cfg.Hydra.URL)
	assert.Equal(t, "11f9143b-da50-4f00-b98b-79ff6b959e07", cfg.Hydra.ClusterID)
	assert.Equal(t, "a-registration-token", cfg.Hydra.RegistrationToken)
	assert.Equal(t, 45*time.Second, cfg.Hydra.HTTPTimeout)
}

func TestLoad_MissingHydraURL_ReturnsValidationError(t *testing.T) {
	t.Setenv("HYDRA_CLUSTER_ID", "11f9143b-da50-4f00-b98b-79ff6b959e07")

	_, err := Load("")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "hydra.url is required")
}

func TestLoad_MissingHydraClusterID_ReturnsValidationError(t *testing.T) {
	t.Setenv("HYDRA_URL", "https://hydra.example.com")

	_, err := Load("")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "hydra.cluster_id is required")
}
