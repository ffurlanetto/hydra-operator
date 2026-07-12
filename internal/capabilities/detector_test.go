package capabilities_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubeversion "k8s.io/apimachinery/pkg/version"
	discoveryfake "k8s.io/client-go/discovery/fake"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/ffurlanetto/hydra-operator/internal/capabilities"
)

func newFakeClientWithGroups(groupVersions ...string) *fake.Clientset {
	client := fake.NewClientset()
	fakeDiscovery := client.Discovery().(*discoveryfake.FakeDiscovery)
	fakeDiscovery.FakedServerVersion = &kubeversion.Info{GitVersion: "v1.31.0"}

	resources := make([]*metav1.APIResourceList, 0, len(groupVersions))
	for _, gv := range groupVersions {
		resources = append(resources, &metav1.APIResourceList{GroupVersion: gv})
	}
	fakeDiscovery.Resources = resources
	return client
}

func TestDetector_Detect_KubernetesOnlyCluster_NoOpenShiftNoGatewayAPI(t *testing.T) {
	client := newFakeClientWithGroups("serving.knative.dev/v1")

	d := capabilities.New(client, time.Second)
	result, err := d.Detect(t.Context())
	require.NoError(t, err)

	assert.False(t, result.Capabilities.OpenShiftAvailable)
	assert.False(t, result.Capabilities.GatewayAPIPresent)
	assert.True(t, result.Capabilities.KnativeAvailable)
	assert.False(t, result.Capabilities.KourierPresent) // no kourier Service seeded
	assert.Equal(t, "v1.31.0", result.K8sVersion)
}

func TestDetector_Detect_OpenShiftCluster_DetectsRouteAPI(t *testing.T) {
	client := newFakeClientWithGroups("route.openshift.io/v1", "serving.knative.dev/v1")

	d := capabilities.New(client, time.Second)
	result, err := d.Detect(t.Context())
	require.NoError(t, err)

	assert.True(t, result.Capabilities.OpenShiftAvailable)
	assert.True(t, result.Capabilities.KnativeAvailable)
}

func TestDetector_Detect_KourierServicePresent_DetectsKourier(t *testing.T) {
	client := newFakeClientWithGroups("serving.knative.dev/v1")
	_, err := client.CoreV1().Namespaces().Create(t.Context(), &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: "kourier-system"},
	}, metav1.CreateOptions{})
	require.NoError(t, err)
	_, err = client.CoreV1().Services("kourier-system").Create(t.Context(), &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "kourier", Namespace: "kourier-system"},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	d := capabilities.New(client, time.Second)
	result, err := d.Detect(t.Context())
	require.NoError(t, err)

	assert.True(t, result.Capabilities.KourierPresent)
}

func TestDetector_Detect_CountsNodes(t *testing.T) {
	client := newFakeClientWithGroups()
	for _, name := range []string{"node-1", "node-2", "node-3"} {
		_, err := client.CoreV1().Nodes().Create(t.Context(), &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{Name: name},
		}, metav1.CreateOptions{})
		require.NoError(t, err)
	}

	d := capabilities.New(client, time.Second)
	result, err := d.Detect(t.Context())
	require.NoError(t, err)

	assert.Equal(t, 3, result.NodeCount)
}

func TestResult_MarshalCapabilitiesJSON_ProducesExpectedFields(t *testing.T) {
	result := capabilities.Result{
		Capabilities: capabilities.Capabilities{OpenShiftAvailable: true, KourierPresent: false},
	}
	data, err := result.MarshalCapabilitiesJSON()
	require.NoError(t, err)
	assert.Contains(t, string(data), `"openshift_available":true`)
	assert.Contains(t, string(data), `"kourier_present":false`)
}
