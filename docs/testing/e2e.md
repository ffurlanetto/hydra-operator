# Testing hydra-operator against real clusters

hydra-operator's tests run in three tiers, each trading realism for speed/
portability. Pick the narrowest tier that answers your question — don't
reach for kind to check a struct mapping the unit tests already cover.

## 1. Unit tests (`go test ./...`, part of every CI run)

`internal/**/*_test.go` use fake clientsets
(`k8s.io/client-go/kubernetes/fake`, the Knative and OpenShift Route fake
clientsets) — no real API server, no network, sub-second. These cover every
reconciler's object-building logic (`internal/reconciler`), the Hydra API
client (`internal/hydraclient`), token persistence (`internal/tokenstore`),
and capability detection (`internal/capabilities`).

**What they can't catch:** a fake clientset accepts almost any object you
hand it — it doesn't enforce a CRD's structural schema, and it never runs
Knative's or OpenShift's own controllers. A `Service` this operator builds
could be schema-invalid, or could simply never become Ready, and these
tests would still pass.

## 2. envtest (`make test-envtest`, gated CI job, not in the default `test` target)

`test/envtest/` (build tag `envtest`) starts a **real** `kube-apiserver` +
`etcd` via [`sigs.k8s.io/controller-runtime/pkg/envtest`](https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/envtest) —
no kubelet, no container runtime, no Knative/OpenShift controllers. This
catches schema-invalid objects a fake clientset would silently accept (a
struct field this repo's Go types allow but the CRD's OpenAPI schema
rejects), and validates real Kubernetes admission behavior (e.g. a
`ResourceQuota` actually blocking an over-quota `ResourceQuota` object,
namespace uniqueness, etc.) without needing any container image or
virtualization.

Needs `KUBEBUILDER_ASSETS` (`setup-envtest use`, downloaded from
`storage.googleapis.com`) and Knative Serving's CRD YAML in
`test/envtest/crds/` (**not checked in** — it changes with every Knative
release, and is fetched fresh by `make test-envtest` / CI via GitHub's
`/releases/latest/download/serving-crds.yaml` floating URL, rather than
pinning a version tag that may not exist by the time you read this).

Run locally: `make test-envtest`. Deliberately scoped to Knative Serving
only — OpenShift Route CRD coverage is left to tier 1 (fake clientset) and
tier 3's opt-in CRC leg, since this repo could not verify the exact release
asset path for OpenShift's Route CRD without a live OpenShift cluster to
check against.

## 3. e2e (`make test-e2e` / `make e2e-local`, kind in CI, kind/k3d/CRC locally)

`test/e2e/` (build tag `e2e`) is the only tier that runs against **Knative's
actual controller** — the thing that watches a `Service` object and
actually creates a `Revision`, builds a route, and flips the `Ready`
condition. Nothing in tiers 1–2 can tell you whether a real rollout, canary
split, or rollback actually converges; this tier does, by polling the real
object's status the way Hydra's operator itself does.

Scenarios covered:
- **Deploy**: a new `Container` becomes a Ready Knative `Service` with a
  real route URL.
- **Rollout**: an image change + restart-generation bump produces a new
  Revision.
- **Canary**: an explicit traffic split across two Revisions is honored by
  the real Route.
- **Rollback**: moving 100% of traffic back to the first Revision is
  honored.
- **Quota enforcement**: a managed namespace's `ResourceQuota` actually
  rejects a Pod that would exceed `count/pods`, not just persists an object.
- **OpenShift Route** (opt-in, real OpenShift only, see below).

### In CI: kind

`.github/workflows/e2e-kind.yml` creates a [kind](https://kind.sigs.k8s.io/)
cluster, installs Knative Serving + [Kourier](https://github.com/knative-sandbox/net-kourier)
via the same manifests documented at
[knative.dev/docs/install](https://knative.dev/docs/install/), and runs
`go test -tags e2e ./test/e2e/...` against it. This is the **real**
verification surface for `test/e2e/` and `test/envtest/` — this repo's own
sandboxed development environment cannot pull container images or run a VM,
so those two tiers only get their first real run in CI, not during
day-to-day local iteration by whoever wrote them.

### Locally: `scripts/e2e-local.sh`

```sh
make e2e-local
# or directly:
./scripts/e2e-local.sh
```

Installs `kind` if missing, stands up (or reuses) a local cluster named
`hydra-e2e-local`, installs Knative Serving + Kourier, and runs the e2e
suite against it — the cluster is left running afterward
(`kind delete cluster --name hydra-e2e-local` to tear it down).

### OpenShift Local (CRC) — local-hardware-only, never in CI

If the script also finds a **running** `crc` (OpenShift Local) instance
(`crc status` reports `OpenShift: ... Running`), it separately points
`KUBECONFIG` at CRC and runs
`TestE2E_RouteReconciler_OnOpenShift_RealRouterAdmitsRoute` — the one
scenario that needs a genuine OpenShift Route controller: does OpenShift's
real router admit the `Route` this operator builds and assign it a routable
host.

This scenario deploys a Knative `Service` first (it reconciles the Route for
an already-Ready ksvc), so CRC needs **Knative Serving** too. A stock CRC has
`route.openshift.io` but no `serving.knative.dev`; in that case the test skips
itself cleanly (its `KnativeAvailable` guard) rather than failing. To actually
run this leg, install OpenShift Serverless:

```sh
./scripts/e2e-local.sh --install-serverless
```

which subscribes CRC to the OpenShift Serverless operator and creates a
`KnativeServing` instance before running the leg. This is opt-in because it's
heavy — an operator subscription plus several minutes and notable RAM/CPU on
single-node CRC.

**This can never run in GitHub Actions**, or in any environment without
hardware virtualization: CRC packages a full OpenShift cluster in a VM via
libvirt/KVM, and standard hosted CI runners (and this repository's own
sandboxed development container) have no `/dev/kvm` and no nested
virtualization. If you don't have CRC installed or running, the script
prints a message and skips this leg — the kind leg above still gives you
everything except OpenShift-Route-specific behavior.

To get it manually:
1. Install CRC: https://developers.redhat.com/products/openshift-local/overview
2. `crc start` (needs a machine with virtualization enabled)
3. `./scripts/e2e-local.sh` (it detects CRC automatically), or directly:
   `KUBECONFIG=$(crc oc-env | grep -o '/[^"]*kubeconfig[^"]*') go test -tags e2e ./test/e2e/... -run TestE2E_RouteReconciler_OnOpenShift -v`

## Known gaps (documented, not silently worked around)

- **envtest does not cover the OpenShift Route CRD** — see tier 2 above.
- ~~**Env var secrets** (`internal/reconciler/container.go`'s `buildEnv`) assume a Secret
  key matching the env var's own name~~ — resolved: `hydraclient.EnvVar` now has an
  optional `SecretKey` field (`secret_key` in JSON). When set, `buildEnv` uses it as the
  Secret's key; when absent/empty it falls back to the env var's own name, preserving the
  prior behavior. This still isn't validated against a real Secret's actual keys in any
  e2e tier (unit tests only), and depends on Hydra's desired-state payload populating a
  matching field — see `internal/hydraclient/client.go`'s `EnvVar.SecretKey` doc comment
  for the expected shape.
- **No tier exercises `PolicyReconciler` against a real Gatekeeper install** (ADR-026 MVP):
  unit tests cover it via `k8s.io/client-go/dynamic/fake`, but `e2e-kind.yml` doesn't
  install Gatekeeper, so the Rego policies themselves (`internal/reconciler/policy.go`)
  have never been evaluated by a real admission webhook — only that the operator
  produces the `ConstraintTemplate`/`Constraint` objects it intends to.
- **cosign/sigstore image-signature verification is not implemented** — ADR-026's Phase 2,
  tracked as a follow-up, not started (see the ADR's "État d'implémentation" section).
