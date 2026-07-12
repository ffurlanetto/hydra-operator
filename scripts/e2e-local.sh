#!/usr/bin/env bash
# scripts/e2e-local.sh — run hydra-operator's e2e suite against a real
# cluster on your own machine.
#
# Two independent legs, run in sequence:
#   1. kind (or k3d, if you already have it and not kind) + Knative Serving
#      + Kourier — the same lifecycle scenarios .github/workflows/e2e-kind.yml
#      runs in CI (deploy/rollout/canary/rollback, quota enforcement).
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

run_kind_leg() {
  require_cmd docker
  require_cmd kubectl
  require_cmd go
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
  log "running the OpenShift-only e2e test against CRC"
  (cd "$REPO_ROOT" && go test -tags e2e ./test/e2e/... -run TestE2E_RouteReconciler_OnOpenShift -v -timeout 10m)
}

main() {
  run_kind_leg
  run_crc_leg_if_available
  log "all done"
}

main "$@"
