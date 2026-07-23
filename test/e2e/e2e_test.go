//go:build e2e

// Package e2e_test runs hydra-operator's reconcilers against a real
// Kubernetes cluster with Knative Serving + a networking layer (Kourier)
// installed — kind in CI (see .github/workflows/e2e-kind.yml), or
// kind/k3d/OpenShift Local locally (see scripts/e2e-local.sh). This is the
// only tier of this project's test suite that exercises Knative's actual
// controller: asynchronous Ready conditions, Revision creation, and traffic
// rollout. Neither the fake-clientset unit tests (internal/reconciler/*_test.go)
// nor envtest (kube-apiserver + etcd only, no controllers) can validate that
// a Service this operator builds actually becomes Ready and serves traffic
// the way Knative's control loop says it does.
//
// Requires:
//   - KUBECONFIG pointing at a cluster with Knative Serving + Kourier ready
//   - Outbound internet access to pull the public sample images used below
//
// Not part of `go test ./...` or CI's default job — see `make test-e2e`.
package e2e_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	servingv1 "knative.dev/serving/pkg/apis/serving/v1"
	knativeversioned "knative.dev/serving/pkg/client/clientset/versioned"

	"github.com/ffurlanetto/hydra-operator/internal/hydraclient"
	"github.com/ffurlanetto/hydra-operator/internal/k8sclient"
	"github.com/ffurlanetto/hydra-operator/internal/reconciler"
)

// helloworldImage is Knative's own public sample image (unauthenticated
// pull). A second real "version" of it isn't used here — guessing another
// registry tag risks the same 404 this test used to hit before it settled
// on a TARGET env var change instead, which forces a new Revision (any
// PodSpec diff does) without depending on any tag's existence.
const (
	helloworldImage = "gcr.io/knative-samples/helloworld-go"

	readyTimeout = 3 * time.Minute
	pollInterval = 2 * time.Second
)

func requireCluster(t *testing.T) *k8sclient.Clients {
	t.Helper()
	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig == "" {
		t.Skip("KUBECONFIG not set — skipping e2e test (needs a real cluster, see docs/testing/e2e.md)")
	}
	clients, err := k8sclient.NewFromKubeconfig(kubeconfig)
	require.NoError(t, err)
	return clients
}

// newTestNamespace creates a uniquely-named namespace for one test run and
// registers its deletion, so parallel/successive CI runs never collide.
func newTestNamespace(t *testing.T, clients *k8sclient.Clients) string {
	t.Helper()
	ctx := context.Background()
	name := fmt.Sprintf("hydra-e2e-%d", time.Now().UnixNano())

	_, err := clients.Core.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: name},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	t.Cleanup(func() {
		delCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = clients.Core.CoreV1().Namespaces().Delete(delCtx, name, metav1.DeleteOptions{})
	})
	return name
}

// waitForReady polls the real Knative controller until it reports the
// Service Ready, returning the final observed object.
func waitForReady(t *testing.T, knative knativeversioned.Interface, namespace, name string) *servingv1.Service {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), readyTimeout)
	defer cancel()

	var final *servingv1.Service
	err := wait.PollUntilContextTimeout(ctx, pollInterval, readyTimeout, true, func(ctx context.Context) (bool, error) {
		svc, err := knative.ServingV1().Services(namespace).Get(ctx, name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		final = svc
		// ObservedGeneration lagging Generation means the controller hasn't
		// processed the latest spec update yet — without this check, a
		// Service that was already Ready before an Update reads as "Ready"
		// again instantly on the very next poll, before Knative has even
		// created the new Revision for that update.
		if svc.Status.ObservedGeneration != svc.Generation {
			return false, nil
		}
		cond := svc.Status.GetCondition(servingv1.ServiceConditionReady)
		return cond != nil && cond.IsTrue(), nil
	})
	require.NoErrorf(t, err, "Service %s/%s never became Ready; last status: %+v", namespace, name, final)
	return final
}

// TestE2E_ContainerLifecycle_DeployScaleRollbackCanary drives one
// hydraclient.Container through the full lifecycle Hydra's control plane
// puts an operator through, against a real Knative controller:
//  1. initial deploy becomes Ready and reports a real endpoint
//  2. a restart-generation bump + env var change rolls out a new Revision
//  3. an explicit traffic split (canary) is honored across two Revisions
//  4. rolling 100% traffic back to the first Revision (rollback) is honored
func TestE2E_ContainerLifecycle_DeployScaleRollbackCanary(t *testing.T) {
	clients := requireCluster(t)
	namespace := newTestNamespace(t, clients)
	containerReconciler := reconciler.NewContainerReconciler(clients.Knative)

	containerID := "e2e-lifecycle"
	svcName := reconciler.KsvcNameFor(containerID)

	base := hydraclient.Container{
		ID:               containerID,
		NamespaceK8sName: namespace,
		Definition: hydraclient.Definition{
			Image:       helloworldImage,
			CPULimit:    "100m",
			MemoryLimit: "128Mi",
		},
		Scaling:           hydraclient.Scaling{MinScale: 0, MaxScale: 2},
		RestartGeneration: 1,
	}

	// 1. Deploy v1, wait for the real Knative controller to mark it Ready.
	status, err := containerReconciler.Reconcile(context.Background(), base)
	require.NoError(t, err)
	require.NotEmpty(t, status)

	readySvc := waitForReady(t, clients.Knative, namespace, svcName)
	revisionV1 := readySvc.Status.LatestReadyRevisionName
	require.NotEmpty(t, revisionV1)
	require.NotEmpty(t, readySvc.Status.URL, "Ready service should have a route URL")

	finalStatus, err := containerReconciler.Reconcile(context.Background(), base)
	require.NoError(t, err)
	require.Equal(t, "running", finalStatus.Status)
	require.NotEmpty(t, finalStatus.EndpointURL)

	// 2. Roll out v2: change an env var and bump the restart generation.
	// Any PodSpec diff forces a new Revision — an env var change is used
	// here instead of a second image, since guessing another registry
	// tag's exact existence isn't something this repo can verify without
	// a live pull (see helloworldImage's doc comment).
	v2 := base
	v2.Definition.Env = []hydraclient.EnvVar{{Name: "TARGET", Value: "v2"}}
	v2.RestartGeneration = 2
	_, err = containerReconciler.Reconcile(context.Background(), v2)
	require.NoError(t, err)

	readySvcV2 := waitForReady(t, clients.Knative, namespace, svcName)
	revisionV2 := readySvcV2.Status.LatestReadyRevisionName
	require.NotEmpty(t, revisionV2)
	require.NotEqual(t, revisionV1, revisionV2, "env var change should have produced a new Revision")

	// 3. Canary: split traffic 90/10 between the two Revisions explicitly.
	canary := v2
	canary.Traffic = []hydraclient.Traffic{
		{RevisionName: revisionV2, Percent: 90},
		{RevisionName: revisionV1, Percent: 10},
	}
	_, err = containerReconciler.Reconcile(context.Background(), canary)
	require.NoError(t, err)

	canarySvc := waitForReady(t, clients.Knative, namespace, svcName)
	require.Len(t, canarySvc.Status.Traffic, 2)
	percentByRevision := map[string]int64{}
	for _, tt := range canarySvc.Status.Traffic {
		require.NotNil(t, tt.Percent)
		percentByRevision[tt.RevisionName] = *tt.Percent
	}
	require.Equal(t, int64(90), percentByRevision[revisionV2])
	require.Equal(t, int64(10), percentByRevision[revisionV1])

	// 4. Rollback: send 100% of traffic back to the first Revision.
	rollback := canary
	rollback.Traffic = []hydraclient.Traffic{{RevisionName: revisionV1, Percent: 100}}
	_, err = containerReconciler.Reconcile(context.Background(), rollback)
	require.NoError(t, err)

	rolledBackSvc := waitForReady(t, clients.Knative, namespace, svcName)
	require.Len(t, rolledBackSvc.Status.Traffic, 1)
	require.Equal(t, revisionV1, rolledBackSvc.Status.Traffic[0].RevisionName)
	require.EqualValues(t, 100, *rolledBackSvc.Status.Traffic[0].Percent)
}

// TestE2E_NamespaceReconciler_ManagedNamespace_QuotaEnforced verifies a
// managed namespace's ResourceQuota is honored by the real API server (an
// over-quota Pod creation is rejected) — not just persisted as an object,
// which is all envtest and the fake-clientset unit tests can check.
func TestE2E_NamespaceReconciler_ManagedNamespace_QuotaEnforced(t *testing.T) {
	clients := requireCluster(t)
	ctx := context.Background()
	name := fmt.Sprintf("hydra-e2e-quota-%d", time.Now().UnixNano())

	nsReconciler := reconciler.NewNamespaceReconciler(clients.Core)
	require.NoError(t, nsReconciler.Reconcile(ctx, hydraclient.Namespace{
		ID: "ns-quota", K8sName: name, Mode: "managed",
		Quotas: hydraclient.Quotas{LimitsCPU: "200m", LimitsMemory: "256Mi", CountPods: 1},
	}))
	t.Cleanup(func() {
		delCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = clients.Core.CoreV1().Namespaces().Delete(delCtx, name, metav1.DeleteOptions{})
	})

	pod := func(podName string) *corev1.Pod {
		return &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: podName, Namespace: name},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{{
					Name:  "c",
					Image: helloworldImage,
					Resources: corev1.ResourceRequirements{
						Limits: corev1.ResourceList{
							corev1.ResourceCPU:    resourceQuantity("100m"),
							corev1.ResourceMemory: resourceQuantity("128Mi"),
						},
					},
				}},
			},
		}
	}

	_, err := clients.Core.CoreV1().Pods(name).Create(ctx, pod("first"), metav1.CreateOptions{})
	require.NoError(t, err, "first pod should fit within the quota")

	// The quota admission check reads ResourceQuota.Status.Used, which the
	// quota controller updates asynchronously after the first pod is
	// admitted — Create returning success doesn't mean it's reflected yet.
	// Without waiting for it, the second Create can race ahead of that
	// update and be wrongly admitted.
	waitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	err = wait.PollUntilContextTimeout(waitCtx, 500*time.Millisecond, 30*time.Second, true, func(ctx context.Context) (bool, error) {
		quota, err := clients.Core.CoreV1().ResourceQuotas(name).Get(ctx, "hydra-quota", metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		used := quota.Status.Used[corev1.ResourcePods]
		return used.Value() >= 1, nil
	})
	require.NoError(t, err, "resourcequota controller should observe the first pod")

	_, err = clients.Core.CoreV1().Pods(name).Create(ctx, pod("second"), metav1.CreateOptions{})
	require.Error(t, err, "second pod should be rejected: count/pods quota is 1")
}

func resourceQuantity(s string) resource.Quantity {
	return resource.MustParse(s)
}
