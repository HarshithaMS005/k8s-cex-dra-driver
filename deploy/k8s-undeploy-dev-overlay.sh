#!/usr/bin/env bash
set -euo pipefail

# Tear down the dev deployment created by k8s-deploy-dev-overlay.sh:
# delete the Kustomize overlay's objects (Namespace, ServiceAccount, ClusterRole,
# ClusterRoleBinding, DaemonSet, DeviceClasses) and, optionally, drop the dev
# image from the node's containerd.
#
# Runs ON the same s390x kubeadm node as the deploy script, with a working,
# authenticated kubectl. The inverse of `kubectl apply -k` is `kubectl delete -k`,
# which removes every rendered object - cluster-scoped ones included.
#
# dev-alpha renders a superset of every dev overlay's objects (dev-unpriv and
# dev-priv render the same set. dev-alpha adds the feature DeviceClasses).
# Deleting the superset with --ignore-not-found tears down any dev deploy
# without orphaning anything, so this script does not prompt for the variant
# and defaults to dev-alpha. Override with OVERLAY=... if needed.
#
# The image import is left in place by default: it is node-local state, not part
# of the k8s deployment, and removing it mid-rollout is racy. Set REMOVE_IMAGE=1
# to also drop it from containerd once the objects are gone.
#
# Override via env: OVERLAY, NAMESPACE, REMOVE_IMAGE, IMAGE_REPO, IMAGE_TAG,
# CONTAINERD_NS. Skip the confirmation prompt with -y/--yes or FORCE=1.

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
# Default to the superset overlay so a teardown catches every dev deploy's
# objects (see header). Honor an explicit OVERLAY.
OVERLAY="${OVERLAY:-$SCRIPT_DIR/kustomize/overlays/dev-alpha}"

NAMESPACE="${NAMESPACE:-cex-dra-driver}"

# Only consulted when REMOVE_IMAGE=1. Must match the images: entry in
# deploy/kustomize/overlays/dev-unpriv/kustomization.yaml exactly.
REMOVE_IMAGE="${REMOVE_IMAGE:-}"
IMAGE_REPO="${IMAGE_REPO:-localhost/cex-dra-kubeletplugin}"
IMAGE_TAG="${IMAGE_TAG:-dev}"
IMAGE_REF="${IMAGE_REPO}:${IMAGE_TAG}"
CONTAINERD_NS="${CONTAINERD_NS:-k8s.io}"

FORCE="${FORCE:-}"

log() { printf '==> %s\n' "$*"; }
die() {
  printf 'error: %s\n' "$*" >&2
  exit 1
}

usage() {
  cat <<EOF
Usage: $(basename "$0") [-y|--yes]

Tear down the dev deployment: delete the Kustomize overlay's objects and,
with REMOVE_IMAGE=1, drop the dev image from containerd.
Run on the s390x node where it was deployed, with a working kubectl.

Flags:
  -y, --yes      skip the confirmation prompt (same as FORCE=1)

Env overrides:
  OVERLAY        kustomize overlay     (default: dev-alpha, the superset)
  NAMESPACE      k8s namespace         (default: ${NAMESPACE})
  REMOVE_IMAGE   also drop the image   (default: keep; set 1 to remove)
  IMAGE_REPO     image repository      (default: ${IMAGE_REPO})
  IMAGE_TAG      image tag             (default: ${IMAGE_TAG})
  CONTAINERD_NS  containerd namespace  (default: ${CONTAINERD_NS})
EOF
}

case "${1:-}" in
  -h | --help)
    usage
    exit 0
    ;;
  -y | --yes)
    FORCE=1
    ;;
  "") ;;
  *)
    die "unknown argument: $1 (try --help)"
    ;;
esac

[[ -d "$OVERLAY" ]] || die "overlay not found: $OVERLAY"

# --- preflight ---

command -v kubectl >/dev/null 2>&1 || die "kubectl not found"

# --- confirm ---

# Deleting is destructive. Prompt unless forced. With no TTY (CI / piped stdin)
# proceed without blocking.
if [[ -z "$FORCE" && -t 0 ]]; then
  read -r -p "Delete the dev deployment (overlay ${OVERLAY##*/}, namespace $NAMESPACE)? [y/N] " reply || reply=""
  case "$reply" in
    [Yy]*) ;;
    *) die "aborted" ;;
  esac
fi

# --- delete ---

log "deleting overlay ${OVERLAY##*/}"
# --ignore-not-found keeps the teardown idempotent: re-running after a partial
# teardown is a no-op, not an error. Namespace deletion may block on finalizers,
# so let kubectl wait it out.
kubectl delete -k "$OVERLAY" --ignore-not-found=true

# --- optionally drop the image from containerd ---

if [[ -n "$REMOVE_IMAGE" ]]; then
  command -v ctr >/dev/null 2>&1 || die "ctr not found (needed for REMOVE_IMAGE)"
  # ctr talks to the containerd socket, which needs root.
  SUDO=""
  if [[ "${EUID:-$(id -u)}" -ne 0 ]]; then
    SUDO="sudo"
  fi
  log "removing image $IMAGE_REF from containerd namespace '$CONTAINERD_NS'"
  $SUDO ctr -n "$CONTAINERD_NS" images rm "$IMAGE_REF" || log "image $IMAGE_REF not present, nothing to remove"
fi

log "done"
