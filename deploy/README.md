# Deploying hydra-operator

## Prerequisite: Knative Serving

hydra-operator only reconciles Knative `Service`/`DomainMapping` objects — it
has no Deployment/HPA/KEDA fallback (see
[ADR-025](https://github.com/ffurlanetto/hydra/blob/main/docs/adr/ADR-025-knative-only-control-plane.md)
in the `hydra` repo). **Knative Serving must already be installed on the
target cluster.** The operator detects this at startup
(`internal/capabilities`) and refuses to start if Knative isn't present —
it does not install Knative for you.

**Required Knative feature flag**: Hydra's desired-state API always sets
`runtime_class_name` on every container (ADR-026: Kata Containers isolation,
no exceptions). Knative's own admission webhook rejects
`spec.template.spec.runtimeClassName` outright unless the
`kubernetes.podspec-runtimeclassname` feature flag is explicitly enabled —
it's off by default. Without it, **every agent deploy fails identically**
with `admission webhook "validation.webhook.serving.knative.dev" denied the
request: validation failed: must not set the field(s):
spec.template.spec.runtimeClassName` — found the hard way by the first real
end-to-end run of `scripts/e2e-hydra-integration.sh` against a freshly
installed cluster. Enable it before deploying any agent:

```sh
kubectl patch configmap/config-features -n knative-serving --type merge \
  -p '{"data":{"kubernetes.podspec-runtimeclassname":"enabled"}}'
```

Optional, auto-detected (never installed by the operator):
- Kourier or another Knative networking layer, or OpenShift Routes as a
  fallback
- [Gatekeeper](https://open-policy-agent.github.io/gatekeeper/) — if
  present, the operator applies its ADR-026 policy `ConstraintTemplate`s
  (resource limits, block-privileged, allowed image registries). Absent
  Gatekeeper, this step is skipped and logged, not fatal.

## Registration token

Every cluster registers with Hydra once, using a one-time token generated
from the Hydra UI/API for that `cluster_id`. Create it as a Secret in the
operator's namespace before (or right after) deploying — neither manifest
set below generates or commits this token:

```bash
kubectl create secret generic hydra-operator-registration \
  -n hydra-operator-system \
  --from-literal=token=<one-time-token>
```

After a successful first registration the operator persists its own
cluster token into another Secret (`hydra-operator-token`, see
`deploy/base/role.yaml`) and no longer needs the registration Secret; it's
safe to delete afterwards.

Two equivalent install mechanisms are provided. Pick one — don't mix them
on the same cluster/namespace.

## Option A — Kustomize

Simplest path, no extra tooling beyond `kubectl`.

```bash
# 1. Copy the example overlay and edit HYDRA_URL / HYDRA_CLUSTER_ID
cp -r deploy/overlays/example deploy/overlays/my-cluster
$EDITOR deploy/overlays/my-cluster/patch-config.yaml

# 2. Create the registration Secret (see above), then apply
kubectl create namespace hydra-operator-system --dry-run=client -o yaml | kubectl apply -f -
kubectl create secret generic hydra-operator-registration \
  -n hydra-operator-system --from-literal=token=<one-time-token>
kubectl apply -k deploy/overlays/my-cluster
```

`deploy/base` alone is intentionally not deployable — `HYDRA_URL` and
`HYDRA_CLUSTER_ID` are empty there and the operator's config validation
rejects empty values at startup. Always deploy through an overlay.

## Option B — Helm

Same manifests, packaged as a chart — useful if your cluster's app
delivery already standardizes on Helm (e.g. Rancher's catalog), or as a
stepping stone toward an OLM bundle for OpenShift's OperatorHub.

```bash
kubectl create namespace hydra-operator-system --dry-run=client -o yaml | kubectl apply -f -
kubectl create secret generic hydra-operator-registration \
  -n hydra-operator-system --from-literal=token=<one-time-token>

helm install hydra-operator ./helm \
  --namespace hydra-operator-system \
  --set hydra.url=https://hydra.example.com \
  --set hydra.clusterId=example-cluster
```

See `helm/values.yaml` for the full set of configurable values.

## Keeping both in sync

`helm/templates` mirrors `deploy/base` resource-for-resource, in
particular the RBAC (`ClusterRole`/`Role`). CI renders both
(`make deploy-validate`, `make helm-template`) and fails the build if the
two `ClusterRole` rule sets diverge — see `.github/workflows/build.yml`.
If you add a permission to one, add it to the other in the same change.
