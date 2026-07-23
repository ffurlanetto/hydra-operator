package tokenstore_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/ffurlanetto/hydra-operator/internal/tokenstore"
)

func TestStore_Load_NoSecretYet_ReturnsEmptyNoError(t *testing.T) {
	client := fake.NewClientset()
	s := tokenstore.New(client, "hydra-operator", "hydra-operator-token")

	token, err := s.Load(t.Context())
	require.NoError(t, err)
	assert.Empty(t, token)
}

func TestStore_SaveThenLoad_RoundTrips(t *testing.T) {
	client := fake.NewClientset()
	s := tokenstore.New(client, "hydra-operator", "hydra-operator-token")

	require.NoError(t, s.Save(t.Context(), "jwt-abc"))

	token, err := s.Load(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "jwt-abc", token)
}

func TestStore_SaveTwice_UpdatesExistingSecret(t *testing.T) {
	client := fake.NewClientset()
	s := tokenstore.New(client, "hydra-operator", "hydra-operator-token")

	require.NoError(t, s.Save(t.Context(), "jwt-first"))
	require.NoError(t, s.Save(t.Context(), "jwt-rotated"))

	token, err := s.Load(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "jwt-rotated", token)
}
