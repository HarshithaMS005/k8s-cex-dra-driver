#!/usr/bin/env bash
set -euo pipefail

# Build the cex-dra-kubeletplugin image, import it into the node's containerd,
# and (re)deploy the dev Kustomize overlay.
#
# Runs ON an s390x kubeadm node that has this repo checked out and a working,
# authenticated kubectl. There is no SSH or registry: the image is built with
# the local engine and imported straight into containerd's k8s.io namespace, so
# the DaemonSet picks it up with imagePullPolicy: IfNotPresent and no pull.
#
# The dev overlay pins a fixed :dev tag, so re-importing the same tag does not
# change the pod spec. `kubectl apply` alone would not restart anything. The
# script therefore rolls the DaemonSet explicitly at the end.
#
# Override via env: OVERLAY_VARIANT (priv|unpriv|alpha), OVERLAY, IMAGE_REPO,
# IMAGE_TAG, ENGINE (podman|docker), CONTAINERD_NS, NAMESPACE, DAEMONSET.

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
# Overlay selection (resolved below): OVERLAY_VARIANT=priv|unpriv|alpha picks a
# dev overlay by name. OVERLAY=<dir> overrides with an explicit path. With
# neither set, prompt on a TTY, else default to the hardened unpriv overlay.
OVERLAY_VARIANT="${OVERLAY_VARIANT:-}"
OVERLAY="${OVERLAY:-}"

# Must match the images: entry in
# deploy/kustomize/overlays/dev-unpriv/kustomization.yaml exactly (podman tags
# local builds as localhost/...). Change both together.
IMAGE_REPO="${IMAGE_REPO:-localhost/cex-dra-kubeletplugin}"
IMAGE_TAG="${IMAGE_TAG:-dev}"
IMAGE_REF="${IMAGE_REPO}:${IMAGE_TAG}"

CONTAINERD_NS="${CONTAINERD_NS:-k8s.io}"
NAMESPACE="${NAMESPACE:-cex-dra-driver}"
DAEMONSET="${DAEMONSET:-cex-dra-driver}"

log() { printf '==> %s\n' "$*"; }
die() {
  printf 'error: %s\n' "$*" >&2
  exit 1
}

usage() {
  cat <<EOF
Usage: $(basename "$0")

Build the plugin image, import it into containerd, and redeploy the dev overlay.
Run on an s390x node with this repo checked out and a working kubectl.

Env overrides:
  OVERLAY_VARIANT dev overlay variant   (priv|unpriv|alpha; default: prompt, unpriv if no TTY)
  OVERLAY         explicit overlay dir  (default: derived from OVERLAY_VARIANT)
  IMAGE_REPO      image repository      (default: ${IMAGE_REPO})
  IMAGE_TAG       image tag             (default: ${IMAGE_TAG})
  ENGINE          podman | docker       (default: autodetect)
  CONTAINERD_NS   containerd namespace  (default: ${CONTAINERD_NS})
  NAMESPACE       k8s namespace         (default: ${NAMESPACE})
  DAEMONSET       DaemonSet name        (default: ${DAEMONSET})
EOF
}

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  usage
  exit 0
fi

# --- choose overlay ---

# Resolve the overlay, most specific first:
#   OVERLAY set          -> explicit dir, used as-is (advanced escape hatch)
#   OVERLAY_VARIANT set  -> dev-<variant>, no prompt (priv|unpriv|alpha)
#   neither, on a TTY    -> prompt, defaulting to unpriv
#   neither, no TTY (CI) -> default to unpriv without blocking
if [[ -z "$OVERLAY" ]]; then
  variant="$OVERLAY_VARIANT"
  if [[ -z "$variant" ]]; then
    variant="unpriv"
    if [[ -t 0 ]]; then
      read -r -p "Deploy which dev overlay - [U]npriv (hardened, default), [p]riv (privileged), or [a]lpha (all alpha features)? " reply || reply=""
      case "$reply" in
        [Pp]*) variant="priv" ;;
        [Aa]*) variant="alpha" ;;
        *) variant="unpriv" ;;
      esac
    fi
  fi
  case "$variant" in
    priv | unpriv | alpha) ;;
    *) die "invalid OVERLAY_VARIANT '$variant' (want priv, unpriv, or alpha)" ;;
  esac
  OVERLAY="$SCRIPT_DIR/kustomize/overlays/dev-${variant}"
fi
log "using overlay ${OVERLAY##*/}"

# --- preflight ---

if [[ "$(uname -m)" != "s390x" ]]; then
  log "warning: host arch is $(uname -m), not s390x; the built image will not run on s390x nodes"
fi

ENGINE="${ENGINE:-}"
if [[ -z "$ENGINE" ]]; then
  if command -v podman >/dev/null 2>&1; then
    ENGINE=podman
  elif command -v docker >/dev/null 2>&1; then
    ENGINE=docker
  else
    die "no container engine found (need podman or docker)"
  fi
fi
command -v "$ENGINE" >/dev/null 2>&1 || die "engine '$ENGINE' not found"
command -v ctr >/dev/null 2>&1 || die "ctr not found (containerd CLI required to import the image)"
command -v kubectl >/dev/null 2>&1 || die "kubectl not found"

# ctr talks to the containerd socket, which needs root.
SUDO=""
if [[ "${EUID:-$(id -u)}" -ne 0 ]]; then
  SUDO="sudo"
fi

# --- build ---

log "building $IMAGE_REF with $ENGINE"
# The build context carries no .git, so the build identity is computed here
# and passed through. See the Dockerfile's VERSION arg.
VERSION="$(git -C "$REPO_ROOT" describe --tags --always --dirty --long 2>/dev/null || echo dev)"
"$ENGINE" build -t "$IMAGE_REF" -f "$REPO_ROOT/Dockerfile" --build-arg "VERSION=$VERSION" "$REPO_ROOT"

# --- import into containerd ---

TARBALL="$(mktemp -t cex-dra-image.XXXXXX.tar)"
trap 'rm -f "$TARBALL"' EXIT

log "saving image to $TARBALL"
"$ENGINE" save "$IMAGE_REF" -o "$TARBALL"

log "importing into containerd namespace '$CONTAINERD_NS'"
$SUDO ctr -n "$CONTAINERD_NS" images import "$TARBALL"
$SUDO ctr -n "$CONTAINERD_NS" images ls | grep -- "$IMAGE_REF" ||
  die "image $IMAGE_REF not visible in containerd after import"

# --- deploy ---

log "applying dev overlay"
kubectl apply -k "$OVERLAY"

log "rolling DaemonSet $NAMESPACE/$DAEMONSET to pick up the new image"
kubectl -n "$NAMESPACE" rollout restart "daemonset/$DAEMONSET"
kubectl -n "$NAMESPACE" rollout status "daemonset/$DAEMONSET" --timeout=120s

log "done"
