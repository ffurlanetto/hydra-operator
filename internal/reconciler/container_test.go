package reconciler_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	knativefake "knative.dev/serving/pkg/client/clientset/versioned/fake"

	"github.com/ffurlanetto/hydra-operator/internal/hydraclient"
	"github.com/ffurlanetto/hydra-operator/internal/reconciler"
)

func baseContainer() hydraclient.Container {
	return hydraclient.Container{
		ID:               "11111111-1111-1111-1111-111111111111",
		NamespaceK8sName: "prod",
		Definition: hydraclient.Definition{
			Image:           "registry.example.com/agent:v1",
			CPULimit:        "500m",
			MemoryLimit:     "512Mi",
			HealthCheckPath: "/healthz",
		},
		Scaling:           hydraclient.Scaling{MinScale: 1, MaxScale: 3, ConcurrencyTarget: 10},
		RestartGeneration: 1,
		RuntimeClassName:  "kata-containers",
	}
}

func TestContainerReconciler_NewContainer_CreatesKnativeService(t *testing.T) {
	client := knativefake.NewSimpleClientset()
	r := reconciler.NewContainerReconciler(client)

	_, err := r.Reconcile(t.Context(), baseContainer())
	require.NoError(t, err)

	name := reconciler.KsvcNameFor(baseContainer().ID)
	svc, err := client.ServingV1().Services("prod").Get(t.Context(), name, metav1.GetOptions{})
	require.NoError(t, err)

	assert.Equal(t, "registry.example.com/agent:v1", svc.Spec.Template.Spec.Containers[0].Image)
	assert.Equal(t, "500m", svc.Spec.Template.Spec.Containers[0].Resources.Limits.Cpu().String())
	assert.Equal(t, "kata-containers", *svc.Spec.Template.Spec.RuntimeClassName)
	assert.Equal(t, "1", svc.Spec.Template.Annotations["hydra.io/restart-generation"])
	assert.Equal(t, "1", svc.Spec.Template.Annotations["autoscaling.knative.dev/min-scale"])
	assert.Equal(t, "3", svc.Spec.Template.Annotations["autoscaling.knative.dev/max-scale"])
	require.Len(t, svc.Spec.Traffic, 1)
	assert.True(t, *svc.Spec.Traffic[0].LatestRevision)
}

func TestContainerReconciler_RestartGenerationBump_UpdatesAnnotation(t *testing.T) {
	client := knativefake.NewSimpleClientset()
	r := reconciler.NewContainerReconciler(client)

	c := baseContainer()
	_, err := r.Reconcile(t.Context(), c)
	require.NoError(t, err)

	c.RestartGeneration = 2
	_, err = r.Reconcile(t.Context(), c)
	require.NoError(t, err)

	name := reconciler.KsvcNameFor(c.ID)
	svc, err := client.ServingV1().Services("prod").Get(t.Context(), name, metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, "2", svc.Spec.Template.Annotations["hydra.io/restart-generation"])
}

func TestContainerReconciler_ExplicitTrafficSplit_MapsToKnativeTraffic(t *testing.T) {
	client := knativefake.NewSimpleClientset()
	r := reconciler.NewContainerReconciler(client)

	c := baseContainer()
	c.Traffic = []hydraclient.Traffic{
		{RevisionName: "ksvc-agent-1-00002", Percent: 90},
		{RevisionName: "ksvc-agent-1-00001", Percent: 10},
	}
	_, err := r.Reconcile(t.Context(), c)
	require.NoError(t, err)

	name := reconciler.KsvcNameFor(c.ID)
	svc, err := client.ServingV1().Services("prod").Get(t.Context(), name, metav1.GetOptions{})
	require.NoError(t, err)
	require.Len(t, svc.Spec.Traffic, 2)
	assert.Equal(t, "ksvc-agent-1-00002", svc.Spec.Traffic[0].RevisionName)
	assert.Equal(t, int64(90), *svc.Spec.Traffic[0].Percent)
	assert.Equal(t, "ksvc-agent-1-00001", svc.Spec.Traffic[1].RevisionName)
	assert.Equal(t, int64(10), *svc.Spec.Traffic[1].Percent)
}

func TestContainerReconciler_EnvWithSecretRef_MapsToSecretKeyRef(t *testing.T) {
	client := knativefake.NewSimpleClientset()
	r := reconciler.NewContainerReconciler(client)

	c := baseContainer()
	c.Definition.Env = []hydraclient.EnvVar{
		{Name: "PLAIN", Value: "hello"},
		{Name: "SECRET_VAL", SecretRef: "agent-secrets"},
	}
	_, err := r.Reconcile(t.Context(), c)
	require.NoError(t, err)

	name := reconciler.KsvcNameFor(c.ID)
	svc, err := client.ServingV1().Services("prod").Get(t.Context(), name, metav1.GetOptions{})
	require.NoError(t, err)
	envs := svc.Spec.Template.Spec.Containers[0].Env
	require.Len(t, envs, 2)
	assert.Equal(t, "hello", envs[0].Value)
	require.NotNil(t, envs[1].ValueFrom)
	require.NotNil(t, envs[1].ValueFrom.SecretKeyRef)
	assert.Equal(t, "agent-secrets", envs[1].ValueFrom.SecretKeyRef.Name)
	assert.Equal(t, "SECRET_VAL", envs[1].ValueFrom.SecretKeyRef.Key)
}

func TestContainerReconciler_EnvWithExplicitSecretKey_UsesSecretKeyNotEnvName(t *testing.T) {
	client := knativefake.NewSimpleClientset()
	r := reconciler.NewContainerReconciler(client)

	c := baseContainer()
	c.Definition.Env = []hydraclient.EnvVar{
		{Name: "SECRET_VAL", SecretRef: "agent-secrets", SecretKey: "actual-key-in-secret"},
	}
	_, err := r.Reconcile(t.Context(), c)
	require.NoError(t, err)

	name := reconciler.KsvcNameFor(c.ID)
	svc, err := client.ServingV1().Services("prod").Get(t.Context(), name, metav1.GetOptions{})
	require.NoError(t, err)
	envs := svc.Spec.Template.Spec.Containers[0].Env
	require.Len(t, envs, 1)
	require.NotNil(t, envs[0].ValueFrom)
	require.NotNil(t, envs[0].ValueFrom.SecretKeyRef)
	assert.Equal(t, "agent-secrets", envs[0].ValueFrom.SecretKeyRef.Name)
	assert.Equal(t, "actual-key-in-secret", envs[0].ValueFrom.SecretKeyRef.Key)
}

func TestContainerReconciler_EnvWithSecretRefAndNoSecretKey_FallsBackToEnvName(t *testing.T) {
	client := knativefake.NewSimpleClientset()
	r := reconciler.NewContainerReconciler(client)

	c := baseContainer()
	c.Definition.Env = []hydraclient.EnvVar{
		{Name: "SECRET_VAL", SecretRef: "agent-secrets"},
	}
	_, err := r.Reconcile(t.Context(), c)
	require.NoError(t, err)

	name := reconciler.KsvcNameFor(c.ID)
	svc, err := client.ServingV1().Services("prod").Get(t.Context(), name, metav1.GetOptions{})
	require.NoError(t, err)
	envs := svc.Spec.Template.Spec.Containers[0].Env
	require.Len(t, envs, 1)
	require.NotNil(t, envs[0].ValueFrom)
	require.NotNil(t, envs[0].ValueFrom.SecretKeyRef)
	assert.Equal(t, "agent-secrets", envs[0].ValueFrom.SecretKeyRef.Name)
	assert.Equal(t, "SECRET_VAL", envs[0].ValueFrom.SecretKeyRef.Key)
}
