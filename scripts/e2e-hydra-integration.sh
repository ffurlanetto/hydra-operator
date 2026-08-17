#!/usr/bin/env bash
# scripts/e2e-hydra-integration.sh — the real Hydra <-> hydra-operator loop,
# end to end, against a real cluster on your own machine (or CI).
#
# Unlike scripts/e2e-local.sh (which drives hydra-operator's reconcilers
# directly against synthetic objects it builds itself), this script runs:
#   - a real `hydra` binary (built from a checkout of github.com/ffurlanetto/hydra)
#   - a real cluster: kind + Knative Serving + Kourier
#   - a real `hydra-operator` binary, pointed at both
# and drives the whole thing purely through Hydra's public HTTP API (login,
# create org/cluster/project/namespace/definition/agent), the same surface
# `web/e2e/fakeOperator.ts` simulates in Hydra's own Playwright suite — the
# difference here is nothing is simulated: the operator really polls
# GET /operator/clusters/:id/desired-state, really reconciles a Knative
# Service, and really reports status back via the k8s-status/sync-status
# callbacks. See docs/testing/e2e.md tier 4 for the full picture, including
# why this script cannot be verified inside this repo's own sandboxed dev
# container (kind fails outright there; K3s hits a non-reproducible runc
# bug) — it's meant to run on real hardware: your laptop, or CI.
#
# Requires: docker, kubectl, go 1.25+, git, curl, jq, openssl. Installs kind
# for you if missing.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

CLUSTER_NAME="${HYDRA_E2E_INTEGRATION_CLUSTER_NAME:-hydra-operator-integration}"
HYDRA_REPO_PATH="${HYDRA_REPO_PATH:-$REPO_ROOT/../hydra}"
HYDRA_REPO_URL="${HYDRA_REPO_URL:-https://github.com/ffurlanetto/hydra.git}"
HYDRA_ADDR="127.0.0.1:8090"
HYDRA_BASE_URL="http://${HYDRA_ADDR}"
HYDRA_ADMIN_EMAIL="integration-admin@example.com"
HYDRA_ADMIN_PASSWORD="integration-admin-password-123"
WORKDIR="$(mktemp -d /tmp/hydra-operator-integration.XXXXXX)"
KNATIVE_SERVING_CRDS_URL="https://github.com/knative/serving/releases/latest/download/serving-crds.yaml"
KNATIVE_SERVING_CORE_URL="https://github.com/knative/serving/releases/latest/download/serving-core.yaml"
KOURIER_URL="https://github.com/knative/net-kourier/releases/latest/download/kourier.yaml"

HYDRA_PID=""
OPERATOR_PID=""

log() { printf '\n\033[1;36m==> %s\033[0m\n' "$1"; }
die() { printf '\033[1;31merror: %s\033[0m\n' "$1" >&2; exit 1; }
require_cmd() { command -v "$1" >/dev/null 2>&1 || die "'$1' is required but not found in PATH"; }

cleanup() {
  [[ -n "$OPERATOR_PID" ]] && kill "$OPERATOR_PID" 2>/dev/null || true
  [[ -n "$HYDRA_PID" ]] && kill "$HYDRA_PID" 2>/dev/null || true
}
trap cleanup EXIT

require_cmd docker
require_cmd kubectl
require_cmd go
require_cmd git
require_cmd curl
require_cmd jq
require_cmd openssl

install_kind_if_missing() {
  if command -v kind >/dev/null 2>&1; then return; fi
  log "kind not found — installing to \$HOME/.local/bin/kind"
  mkdir -p "$HOME/.local/bin"
  local os arch
  os="$(uname -s | tr '[:upper:]' '[:lower:]')"
  case "$(uname -m)" in
    x86_64) arch=amd64 ;;
    arm64|aarch64) arch=arm64 ;;
    *) die "unsupported architecture $(uname -m) for automatic kind install" ;;
  esac
  curl -fsSL -o "$HOME/.local/bin/kind" "https://kind.sigs.k8s.io/dl/latest/kind-${os}-${arch}"
  chmod +x "$HOME/.local/bin/kind"
  export PATH="$HOME/.local/bin:$PATH"
  require_cmd kind
}

# resolve_hydra_repo locates a `hydra` checkout to build the real binary
# from: HYDRA_REPO_PATH if it's already a git repo (e.g. a sibling checkout
# on your laptop), otherwise a shallow clone into the same path.
resolve_hydra_repo() {
  if [[ -d "$HYDRA_REPO_PATH/.git" ]]; then
    log "using existing hydra checkout at $HYDRA_REPO_PATH"
    return
  fi
  log "cloning $HYDRA_REPO_URL into $HYDRA_REPO_PATH"
  git clone --depth 1 "$HYDRA_REPO_URL" "$HYDRA_REPO_PATH"
}

build_hydra() {
  log "building hydra (make build)"
  (cd "$HYDRA_REPO_PATH" && make build)
}

build_operator() {
  log "building hydra-operator"
  (cd "$REPO_ROOT" && make build)
}

# setup_cluster stands up kind + Knative Serving + Kourier, the same as
# scripts/e2e-local.sh, plus a `kata-containers` RuntimeClass stub.
#
# Hydra's desired-state API always sets runtime_class_name=kata-containers
# on every container (ADR-026: Kata isolation, no exceptions in production).
# A bare kind/CI node has no real Kata runtime, so without a RuntimeClass
# object named "kata-containers" the pod would be rejected outright at
# admission (unknown RuntimeClass) and no amount of waiting would make the
# agent go Ready. Pointing that RuntimeClass at the node's ordinary `runc`
# handler is a deliberate, test-only shim: it satisfies the reference so the
# reconciliation loop can actually be observed end to end, without claiming
# any real sandboxing — production clusters must still run real Kata.
setup_cluster() {
  install_kind_if_missing

  if kind get clusters 2>/dev/null | grep -qx "$CLUSTER_NAME"; then
    log "reusing existing kind cluster '$CLUSTER_NAME'"
  else
    log "creating kind cluster '$CLUSTER_NAME'"
    kind create cluster --name "$CLUSTER_NAME" --wait 120s
  fi

  export KUBECONFIG="$WORKDIR/kubeconfig"
  kind get kubeconfig --name "$CLUSTER_NAME" > "$KUBECONFIG"

  log "installing Knative Serving CRDs + core + Kourier"
  kubectl apply -f "$KNATIVE_SERVING_CRDS_URL"
  kubectl wait --for=condition=Established --timeout=60s crd --all
  kubectl apply -f "$KNATIVE_SERVING_CORE_URL"
  kubectl apply -f "$KOURIER_URL"
  kubectl patch configmap/config-network -n knative-serving --type merge \
    -p '{"data":{"ingress-class":"kourier.ingress.networking.knative.dev"}}'
  # Knative's own admission webhook rejects spec.template.spec.runtimeClassName
  # outright unless this feature flag is explicitly enabled — off by default.
  # Since Hydra's desired-state API always sets runtime_class_name (ADR-026,
  # no exceptions), this isn't test-only like the RuntimeClass object below:
  # every real production cluster needs this same flag enabled, or every
  # single agent deploy fails identically with "must not set the field(s):
  # spec.template.spec.runtimeClassName" — see deploy/README.md.
  kubectl patch configmap/config-features -n knative-serving --type merge \
    -p '{"data":{"kubernetes.podspec-runtimeclassname":"enabled"}}'
  kubectl -n knative-serving wait --for=condition=Available deployment --all --timeout=180s
  kubectl -n kourier-system wait --for=condition=Available deployment --all --timeout=180s

  log "applying test-only kata-containers RuntimeClass shim (see script comment)"
  kubectl apply -f - <<'EOF'
apiVersion: node.k8s.io/v1
kind: RuntimeClass
metadata:
  name: kata-containers
handler: runc
EOF
}

start_hydra() {
  log "starting hydra ($HYDRA_BASE_URL)"
  local jwt_key
  jwt_key="$(openssl genrsa 4096 2>/dev/null | base64 -w0)"
  (
    cd "$HYDRA_REPO_PATH"
    HYDRA_SERVER_PORT=8090 \
    HYDRA_JWT_PRIVATE_KEY="$jwt_key" \
    HYDRA_ADMIN_USERNAME="$HYDRA_ADMIN_EMAIL" \
    HYDRA_ADMIN_PASSWORD="$HYDRA_ADMIN_PASSWORD" \
    HYDRA_PUBLIC_URL="$HYDRA_BASE_URL" \
    HYDRA_DATABASE_DSN="file:$WORKDIR/hydra.db?_journal=WAL&_timeout=5000&_foreign_keys=on" \
    HYDRA_LOG_FORMAT=console \
    nohup ./hydra > "$WORKDIR/hydra.log" 2>&1 &
    echo $! > "$WORKDIR/hydra.pid"
  )
  HYDRA_PID="$(cat "$WORKDIR/hydra.pid")"
  for _ in $(seq 1 30); do
    curl -sSf "$HYDRA_BASE_URL/health" > /dev/null 2>&1 && return
    sleep 1
  done
  cat "$WORKDIR/hydra.log" >&2
  die "hydra did not become healthy in time"
}

# api_setup drives Hydra's real API to create everything hydra-operator
# needs to have something to reconcile: org, cluster, registration token,
# project, namespace, agent definition. Mirrors web/e2e/helpers.ts +
# fakeOperator.ts's registration handshake, but through curl/jq instead of
# Playwright, and without ever simulating the operator side.
api_setup() {
  log "logging in as bootstrap admin"
  ADMIN_TOKEN="$(curl -sSf -X POST "$HYDRA_BASE_URL/auth/login" \
    -H 'Content-Type: application/json' \
    -d "{\"email\":\"$HYDRA_ADMIN_EMAIL\",\"password\":\"$HYDRA_ADMIN_PASSWORD\"}" | jq -r .access_token)"
  [[ -n "$ADMIN_TOKEN" && "$ADMIN_TOKEN" != "null" ]] || die "admin login failed"
  AUTH=(-H "Authorization: Bearer $ADMIN_TOKEN" -H 'Content-Type: application/json')

  log "creating org"
  ORG_ID="$(curl -sSf -X POST "$HYDRA_BASE_URL/api/v1/orgs/" "${AUTH[@]}" \
    -d '{"name":"integration-org"}' | jq -r .id)"

  log "creating cluster ref"
  CLUSTER_ID="$(curl -sSf -X POST "$HYDRA_BASE_URL/api/v1/orgs/$ORG_ID/clusters/" "${AUTH[@]}" \
    -d '{"name":"integration-cluster","api_url":"https://kind-integration.local"}' | jq -r .id)"

  log "minting registration token"
  REG_TOKEN="$(curl -sSf -X POST "$HYDRA_BASE_URL/api/v1/orgs/$ORG_ID/clusters/$CLUSTER_ID/registration-token" "${AUTH[@]}" \
    | jq -r .registration_token)"

  log "creating project"
  PROJECT_ID="$(curl -sSf -X POST "$HYDRA_BASE_URL/api/v1/orgs/$ORG_ID/projects/" "${AUTH[@]}" \
    -d '{"name":"integration-project"}' | jq -r .id)"

  log "creating namespace (cluster_id=$CLUSTER_ID)"
  NAMESPACE_ID="$(curl -sSf -X POST "$HYDRA_BASE_URL/api/v1/orgs/$ORG_ID/projects/$PROJECT_ID/namespaces/" "${AUTH[@]}" \
    -d "{\"name\":\"integration-ns\",\"k8s_namespace\":\"integration-ns\",\"cluster_id\":\"$CLUSTER_ID\"}" | jq -r .id)"

  log "creating agent definition"
  DEFINITION_ID="$(curl -sSf -X POST "$HYDRA_BASE_URL/api/v1/orgs/$ORG_ID/projects/$PROJECT_ID/definitions/" "${AUTH[@]}" \
    -d '{"name":"integration-def","image":"mirror.gcr.io/library/nginx:latest","mode":"serverless","cpu_limit":"500m","memory_limit":"512Mi","health_check_path":"/"}' \
    | jq -r .id)"
}

start_operator() {
  log "starting hydra-operator (cluster_id=$CLUSTER_ID)"
  (
    cd "$REPO_ROOT"
    HYDRA_URL="$HYDRA_BASE_URL" \
    HYDRA_CLUSTER_ID="$CLUSTER_ID" \
    HYDRA_REGISTRATION_TOKEN="$REG_TOKEN" \
    HYDRA_OPERATOR_KUBECONFIG="$KUBECONFIG" \
    HYDRA_METRICS_PORT=9095 \
    HYDRA_RECONCILER_SYNC_INTERVAL=5s \
    HYDRA_LOG_FORMAT=console \
    nohup ./hydra-operator > "$WORKDIR/hydra-operator.log" 2>&1 &
    echo $! > "$WORKDIR/operator.pid"
  )
  OPERATOR_PID="$(cat "$WORKDIR/operator.pid")"
}

# wait_namespace_synced polls Hydra (not Kubernetes) for the namespace this
# operator instance owns to flip to synced — proof the operator really
# reconciled a namespace object and really called back
# PUT /operator/namespaces/:id/sync-status, not a simulation.
# dump_cluster_state prints the real Kubernetes-side state (not just
# Hydra's view of it) so a failed run's logs actually show why, instead of
# just "it never got there" — Knative Service/Revision conditions, Pod
# status/events, and each Pod's container logs.
dump_cluster_state() {
  log "dumping cluster state for diagnosis (namespace: integration-ns)"
  kubectl get ksvc,revision,pod -n integration-ns -o wide 2>&1 || true
  echo "--- ksvc/revision describe ---"
  kubectl describe ksvc,revision -n integration-ns 2>&1 || true
  echo "--- pod describe ---"
  kubectl describe pod -n integration-ns 2>&1 || true
  echo "--- pod logs (all containers) ---"
  for pod in $(kubectl get pod -n integration-ns -o jsonpath='{.items[*].metadata.name}' 2>/dev/null); do
    echo "-- $pod --"
    kubectl logs -n integration-ns "$pod" --all-containers --prefix --tail=100 2>&1 || true
  done
  echo "--- recent namespace events ---"
  kubectl get events -n integration-ns --sort-by=.lastTimestamp 2>&1 || true
}

wait_namespace_synced() {
  log "waiting for hydra-operator to report the namespace synced"
  for i in $(seq 1 60); do
    status="$(curl -sSf "$HYDRA_BASE_URL/api/v1/orgs/$ORG_ID/projects/$PROJECT_ID/namespaces/$NAMESPACE_ID" "${AUTH[@]}" | jq -r .sync_status)"
    [[ "$status" == "synced" ]] && { log "namespace synced after ${i}x5s"; return; }
    sleep 5
  done
  cat "$WORKDIR/hydra-operator.log" >&2
  dump_cluster_state
  die "namespace never reported synced"
}

create_agent_and_wait_running() {
  log "creating agent"
  AGENT_ID="$(curl -sSf -X POST "$HYDRA_BASE_URL/api/v1/orgs/$ORG_ID/projects/$PROJECT_ID/namespaces/$NAMESPACE_ID/agents/" "${AUTH[@]}" \
    -d "{\"definition_id\":\"$DEFINITION_ID\",\"name\":\"integration-agent\",\"min_replicas\":1,\"max_replicas\":1,\"desired_replicas\":1}" \
    | jq -r .id)"

  log "waiting for hydra-operator to reconcile the agent to running (real Knative Service)"
  local status=""
  for i in $(seq 1 60); do
    status="$(curl -sSf "$HYDRA_BASE_URL/api/v1/agents/$AGENT_ID" "${AUTH[@]}" | jq -r .status)"
    if [[ "$status" == "running" ]]; then
      log "agent running after ${i}x5s — full real Hydra<->hydra-operator loop confirmed"
      return
    fi
    if [[ "$status" == "failed" ]]; then
      cat "$WORKDIR/hydra-operator.log" >&2
      dump_cluster_state
      die "agent reconciliation reported failed"
    fi
    sleep 5
  done
  log "last known agent status: $status"
  cat "$WORKDIR/hydra-operator.log" >&2
  dump_cluster_state
  die "agent never reached running"
}

main() {
  resolve_hydra_repo
  build_hydra
  build_operator
  setup_cluster
  start_hydra
  api_setup
  start_operator
  wait_namespace_synced
  create_agent_and_wait_running
  log "all done — leaving kind cluster '$CLUSTER_NAME' running (delete with: kind delete cluster --name $CLUSTER_NAME). Logs in $WORKDIR."
}

main "$@"
