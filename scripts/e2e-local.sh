#!/usr/bin/env bash
# scripts/e2e-local.sh — run hydra-operator's e2e suite against a real
# cluster on your own machine.
#
# Two independent legs, run in sequence:
#   1. kind (or k3d, if you already have it and not kind) + Knative Serving
#      + Kourier + OPA Gatekeeper — the same lifecycle scenarios
#      .github/workflows/e2e-kind.yml runs in CI (deploy/rollout/canary/
#      rollback, quota enforcement, and PolicyReconciler's ConstraintTemplates/
#      Constraints rejecting real violating Pods via Gatekeeper's admission
#      webhook).
#   2. OpenShift Local (CRC), only if `crc status` finds a running instance —
#      exercises the real OpenShift Route reconciler. This can never run in
#      CI or in any sandboxed/virtualized-nested environment (CRC needs a
#      full VM via libvirt/KVM) — it is opt-in, local-hardware-only.
#
# Requires: docker (or podman), kubectl, go 1.25+. Installs kind for you if
# missing. Does NOT install/manage CRC — see docs/testing/e2e.md.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
CLUSTER_NAME="${HYDRA_E2E_CLUSTER_NAME:-hydra-e2e-local}"
KNATIVE_SERVING_CRDS_URL="https://github.com/knative/serving/releases/latest/download/serving-crds.yaml"
KNATIVE_SERVING_CORE_URL="https://github.com/knative/serving/releases/latest/download/serving-core.yaml"
KOURIER_URL="https://github.com/knative/net-kourier/releases/latest/download/kourier.yaml"
# Pinned (not "latest") since this manifest is applied on every e2e run and
# PolicyReconciler's Rego bodies are asserted against its exact admission
# behavior — floating here would make a real Gatekeeper release the thing
# that silently breaks the policy e2e leg. Bump deliberately alongside
# .github/workflows/e2e-kind.yml's copy of the same URL.
GATEKEEPER_VERSION="v3.23.0"
GATEKEEPER_URL="https://raw.githubusercontent.com/open-policy-agent/gatekeeper/${GATEKEEPER_VERSION}/deploy/gatekeeper.yaml"

log() { printf '\n\033[1;36m==> %s\033[0m\n' "$1"; }
die() { printf '\033[1;31merror: %s\033[0m\n' "$1" >&2; exit 1; }

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || die "'$1' is required but not found in PATH"
}

install_kind_if_missing() {
  if command -v kind >/dev/null 2>&1; then
    return
  fi
  log "kind not found — installing to \$HOME/.local/bin/kind"
  mkdir -p "$HOME/.local/bin"
  local os arch
  os="$(uname -s | tr '[:upper:]' '[:lower:]')"
  case "$(uname -m)" in
    x86_64) arch=amd64 ;;
    arm64|aarch64) arch=arm64 ;;
    *) die "unsupported architecture $(uname -m) for automatic kind install — install kind manually: https://kind.sigs.k8s.io/docs/user/quick-start/#installation" ;;
  esac
  curl -fsSL -o "$HOME/.local/bin/kind" "https://kind.sigs.k8s.io/dl/latest/kind-${os}-${arch}"
  chmod +x "$HOME/.local/bin/kind"
  export PATH="$HOME/.local/bin:$PATH"
  require_cmd kind
}

# kind talks to docker by default and only uses podman when
# KIND_EXPERIMENTAL_PROVIDER says so — requiring one runtime or the other
# isn't enough, the choice has to be handed to kind explicitly.
select_container_runtime() {
  if command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1; then
    log "using docker as kind's container runtime"
    return
  fi
  if command -v podman >/dev/null 2>&1 && podman info >/dev/null 2>&1; then
    log "docker not usable — using podman as kind's container runtime"
    export KIND_EXPERIMENTAL_PROVIDER=podman
    return
  fi
  die "need a running container runtime: install docker, or podman with a started machine ('podman machine start')"
}

# install_gatekeeper installs OPA Gatekeeper (a pinned release, see
# GATEKEEPER_URL above) so the policy e2e leg
# (TestE2E_PolicyReconciler_RealGatekeeper_RejectsViolatingPods) can apply
# PolicyReconciler's real ConstraintTemplates/Constraints and have them
# evaluated by a real admission webhook, not just persisted as objects the
# way the fake-clientset unit tests in internal/reconciler/policy_test.go do.
install_gatekeeper() {
  log "installing OPA Gatekeeper ${GATEKEEPER_VERSION}"
  kubectl apply -f "$GATEKEEPER_URL"
  kubectl wait --for=condition=Established --timeout=60s crd --all
  kubectl -n gatekeeper-system wait --for=condition=Available deployment --all --timeout=180s

  # The controller-manager Deployment being Available doesn't mean the
  # validating webhook's self-signed CA has been generated and patched into
  # the ValidatingWebhookConfiguration yet — that happens a few seconds after
  # the pod starts. Poll for a non-empty caBundle instead of a fixed sleep;
  # the e2e test itself also retries PolicyReconciler.Reconcile and the
  # subsequent pod-admission checks, so this is a best-effort head start
  # rather than a hard requirement.
  log "waiting for Gatekeeper's validating webhook CA bundle to be provisioned"
  local deadline=$((SECONDS + 120))
  until [[ -n "$(kubectl get validatingwebhookconfiguration gatekeeper-validating-webhook-configuration \
        -o jsonpath='{.webhooks[0].clientConfig.caBundle}' 2>/dev/null)" ]]; do
    [[ $SECONDS -lt $deadline ]] || { log "gatekeeper webhook CA bundle not observed within 2m — continuing anyway, the e2e test retries"; break; }
    sleep 3
  done
}

run_kind_leg() {
  require_cmd kubectl
  require_cmd go
  select_container_runtime
  install_kind_if_missing

  if kind get clusters 2>/dev/null | grep -qx "$CLUSTER_NAME"; then
    log "reusing existing kind cluster '$CLUSTER_NAME' (delete it with: kind delete cluster --name $CLUSTER_NAME)"
  else
    log "creating kind cluster '$CLUSTER_NAME'"
    kind create cluster --name "$CLUSTER_NAME" --wait 120s
  fi

  local kubeconfig
  kubeconfig="$(mktemp)"
  kind get kubeconfig --name "$CLUSTER_NAME" > "$kubeconfig"
  export KUBECONFIG="$kubeconfig"

  log "installing Knative Serving CRDs + core + Kourier"
  kubectl apply -f "$KNATIVE_SERVING_CRDS_URL"
  kubectl wait --for=condition=Established --timeout=60s crd --all
  kubectl apply -f "$KNATIVE_SERVING_CORE_URL"
  kubectl apply -f "$KOURIER_URL"
  kubectl patch configmap/config-network -n knative-serving --type merge \
    -p '{"data":{"ingress-class":"kourier.ingress.networking.knative.dev"}}'
  kubectl -n knative-serving wait --for=condition=Available deployment --all --timeout=180s
  kubectl -n kourier-system wait --for=condition=Available deployment --all --timeout=180s

  install_gatekeeper

  log "running e2e suite against kind (KUBECONFIG=$kubeconfig)"
  (cd "$REPO_ROOT" && go test -tags e2e ./test/e2e/... -v -timeout 20m)

  log "kind leg done. Cluster '$CLUSTER_NAME' left running — delete with: kind delete cluster --name $CLUSTER_NAME"
}

run_crc_leg_if_available() {
  if ! command -v crc >/dev/null 2>&1; then
    log "crc (OpenShift Local) not installed — skipping the OpenShift Route scenario. Install: https://developers.redhat.com/products/openshift-local/overview"
    return
  fi
  if ! crc status 2>/dev/null | grep -qi "OpenShift:.*Running"; then
    log "crc found but not running (start it with: crc start) — skipping the OpenShift Route scenario"
    return
  fi

  log "crc is running — pointing at it for the OpenShift Route scenario"
  local kubeconfig
  kubeconfig="$(crc oc-env 2>/dev/null | grep -o '/[^"]*kubeconfig[^"]*' || true)"
  if [[ -z "$kubeconfig" ]]; then
    kubeconfig="$HOME/.crc/machines/crc/kubeconfig"
  fi
  [[ -f "$kubeconfig" ]] || die "could not locate crc's kubeconfig — run 'crc console --credentials' to check your setup"

  export KUBECONFIG="$kubeconfig"

  # The OpenShift Route test deploys a Knative Service first, so CRC needs
  # Knative too. A stock CRC has route.openshift.io but no serving.knative.dev;
  # without Knative the test skips itself cleanly (its own KnativeAvailable
  # guard). Pass --install-serverless to install OpenShift Serverless so the
  # leg actually runs — heavy (operator subscription, several minutes, notable
  # RAM/CPU on single-node CRC), hence opt-in.
  if ! kubectl api-resources --api-group=serving.knative.dev 2>/dev/null | grep -q .; then
    if [[ "$INSTALL_SERVERLESS" == "1" ]]; then
      install_openshift_serverless
    else
      log "crc has no Knative Serving — the OpenShift Route test will skip itself. Pass --install-serverless to install OpenShift Serverless and actually run it."
    fi
  fi

  log "running the OpenShift-only e2e test against CRC"
  (cd "$REPO_ROOT" && go test -tags e2e ./test/e2e/... -run TestE2E_RouteReconciler_OnOpenShift -v -timeout 10m)
}

# install_openshift_serverless subscribes to the OpenShift Serverless operator
# and creates a KnativeServing instance, then waits for it to come up. Assumes
# KUBECONFIG already points at CRC with cluster-admin (crc's default kubeadmin).
install_openshift_serverless() {
  log "installing OpenShift Serverless operator on CRC (this takes several minutes)"
  kubectl apply -f - <<'EOF'
apiVersion: operators.coreos.com/v1alpha1
kind: Subscription
metadata:
  name: serverless-operator
  namespace: openshift-serverless
spec:
  channel: stable
  name: serverless-operator
  source: redhat-operators
  sourceNamespace: openshift-marketplace
---
apiVersion: v1
kind: Namespace
metadata:
  name: openshift-serverless
EOF
  log "waiting for the Serverless operator CSV to succeed"
  local deadline=$((SECONDS + 600))
  until kubectl get csv -n openshift-serverless 2>/dev/null \
      | grep -i serverless | grep -qi Succeeded; do
    [[ $SECONDS -lt $deadline ]] || die "OpenShift Serverless operator did not become ready within 10m"
    sleep 10
  done

  log "creating the KnativeServing instance"
  kubectl create namespace knative-serving 2>/dev/null || true
  kubectl apply -f - <<'EOF'
apiVersion: operator.knative.dev/v1beta1
kind: KnativeServing
metadata:
  name: knative-serving
  namespace: knative-serving
EOF
  kubectl wait --for=condition=Ready knativeserving/knative-serving \
    -n knative-serving --timeout=300s
}

INSTALL_SERVERLESS=0

parse_args() {
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --install-serverless) INSTALL_SERVERLESS=1 ;;
      -h|--help)
        printf 'usage: %s [--install-serverless]\n\n' "$(basename "$0")"
        printf '  --install-serverless  install OpenShift Serverless on a running CRC so\n'
        printf '                        the OpenShift Route e2e leg actually runs (heavy;\n'
        printf '                        default is to skip that leg if Knative is absent).\n'
        exit 0 ;;
      *) die "unknown argument: $1 (see --help)" ;;
    esac
    shift
  done
}

main() {
  parse_args "$@"
  run_kind_leg
  run_crc_leg_if_available
  log "all done"
}

main "$@"
