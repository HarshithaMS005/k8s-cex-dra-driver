#!/usr/bin/env bash
# Hardware preconditions (CEX/AP) and KubeVirt/virtctl setup helpers.
# Sourced by cex-dra.sh; requires helpers.sh to already be sourced.

# ---------------------------------------------------------------------------
# 3. Preconditions
# ---------------------------------------------------------------------------

# Run a shell snippet on the first schedulable node via a privileged debug pod.
# The host root filesystem is mounted at /host inside the container, and the
# snippet runs via "chroot /host bash -c ..." so that /sys, /dev, /proc, and
# all host binaries (lszcrypt, modprobe, …) are fully visible.
#
# Uses the create→wait→logs→delete flow instead of --attach to avoid the
# well-known race where kubectl loses the attach connection on fast-exiting pods.
#
# Usage: run_on_node <snippet>
# Exits non-zero (and prints the pod log) when the snippet exits non-zero.
# Resolve the first schedulable node and remove any leftover pod from a prior run.
# Prints the node name to stdout.
_ron_resolve_node() {
  local pod_name="$1"
  local node
  node="$(kubectl get nodes --no-headers -o custom-columns=NAME:.metadata.name | head -1)"
  [[ -n "$node" ]] || fail "no Kubernetes nodes found"
  kubectl delete pod "${pod_name}" --ignore-not-found=true --wait=true \
    --timeout=30s >/dev/null 2>&1 || true
  echo "$node"
}

# Base64-encode the snippet and apply the precondition pod template.
# Both images are already cached on the node:
#   quay.io/fedora/fedora:44        — bash + base64 for the init container
#   registry.k8s.io/pause:3.10.1   — sandbox image used by Kubernetes 1.36
_ron_create_pod() {
  local node="$1" pod_name="$2" snippet="$3"
  local encoded_snippet
  encoded_snippet="$(printf '%s' "$snippet" | base64 -w0)"
  NODE="${node}" POD_NAME="${pod_name}" ENCODED_SNIPPET="${encoded_snippet}" \
    envsubst < "${SCRIPT_DIR}/precondition-pod.yaml.tmpl" | kubectl apply -f - >/dev/null
  echo "  (pod ${pod_name} created on node ${node}; waiting for completion...)"
}

# Wait until the init container terminates (2-minute deadline).
# Requires kubectl >= 1.23 for --for=jsonpath support.
_ron_wait_pod() {
  local pod_name="$1" node="$2"
  if ! kubectl wait pod "${pod_name}" \
      --for='jsonpath={.status.initContainerStatuses[0].state.terminated}' \
      --timeout=120s 2>/dev/null; then
    kubectl delete pod "${pod_name}" --ignore-not-found=true --wait=false >/dev/null 2>&1 || true
    fail "timed out waiting for precondition pod on node ${node}"
  fi
}

# Print pod logs, capture exit code, delete pod, and fail on non-zero.
_ron_collect_and_cleanup() {
  local pod_name="$1" node="$2"
  echo
  kubectl logs "${pod_name}" -c "${pod_name}" 2>/dev/null || true
  local exit_code
  exit_code="$(kubectl get pod "${pod_name}" \
    -o jsonpath='{.status.initContainerStatuses[0].state.terminated.exitCode}' \
    2>/dev/null || echo 1)"
  kubectl delete pod "${pod_name}" --ignore-not-found=true --wait=false >/dev/null 2>&1 || true
  [[ "${exit_code}" == "0" ]] || fail "precondition check failed on node ${node} (exit code ${exit_code})"
}

run_on_node() {
  local snippet="$1" pod_name="cex-precond-check"
  local node="$(_ron_resolve_node "${pod_name}")"
  _ron_create_pod "${node}" "${pod_name}" "${snippet}"
  _ron_wait_pod   "${pod_name}" "${node}"
  _ron_collect_and_cleanup "${pod_name}" "${node}"
}

check_preconditions() {
  info "Preconditions (CEX, driver_override, vfio_ap)"

  local node
  node="$(kubectl get nodes --no-headers -o custom-columns=NAME:.metadata.name | head -1)"
  [[ -n "$node" ]] || fail "no Kubernetes nodes found"

  step "Running hardware checks on node ${node} via debug pod"

  run_on_node '
set -euo pipefail

echo "kernel: $(uname -r)"
echo

echo "--- lszcrypt ---"
lszcrypt || { echo "ERROR: lszcrypt failed" >&2; exit 1; }
echo
if ! lszcrypt | grep -Eq '"'"'^[0-9a-fA-F]{2}\.[0-9a-fA-F]{4}'"'"'; then
  echo "ERROR: no AP queue (CARD.DOM) visible; CEX is not available on this node" >&2
  exit 1
fi
[[ -d /sys/bus/ap ]] || { echo "ERROR: /sys/bus/ap missing" >&2; exit 1; }

echo "--- apmask / aqmask ---"
apmask="$(cat /sys/bus/ap/apmask)"
aqmask="$(cat /sys/bus/ap/aqmask)"
echo "apmask=${apmask}"
echo "aqmask=${aqmask}"
leftover="$(echo "${apmask#0x}${aqmask#0x}" | tr -d '"'"'fF'"'"')"
if [[ -n "$leftover" ]]; then
  echo "WARNING: masks are not all-f; preflight may fail"
  echo "  chzdev --type ap apmask=+0x00-0xff aqmask=+0x00-0xff"
fi

echo "--- driver_override ---"
overrides="$(find /sys/bus/ap /sys/devices/ap -name driver_override 2>/dev/null || true)"
if [[ -z "$overrides" ]]; then
  echo "ERROR: AP driver_override not found. Fedora 7.x should have it; RHCOS/RHEL 9 typically does not." >&2
  exit 1
fi
echo "$overrides"
echo "driver_override: OK"

echo "--- vfio_ap ---"
if [[ ! -e /sys/devices/vfio_ap/matrix ]]; then
  echo "vfio_ap not loaded; loading module"
  modprobe vfio_ap || { echo "ERROR: modprobe vfio_ap failed" >&2; exit 1; }
fi
[[ -e /sys/devices/vfio_ap/matrix ]] || { echo "ERROR: /sys/devices/vfio_ap/matrix missing" >&2; exit 1; }
echo "vfio_ap: OK"
ls /sys/class/mdev_bus >/dev/null 2>&1 || { echo "ERROR: /sys/class/mdev_bus missing" >&2; exit 1; }
echo "mdev: OK"
'

  kubectl get nodes -o wide
}

# ---------------------------------------------------------------------------
# 4–5. KubeVirt / virtctl helpers
# ---------------------------------------------------------------------------
fetch_latest_kubevirt_version() {
  local latest="$(curl -fsSL https://storage.googleapis.com/kubevirt-prow/release/kubevirt/kubevirt/stable.txt || true)"
  if [[ -z "$latest" ]]; then
    latest="$(curl -fsSL https://api.github.com/repos/kubevirt/kubevirt/releases/latest | grep -o '"tag_name": *"[^"]*"' | sed 's/.*": *"\(.*\)"/\1/')"
  fi
  [[ -n "$latest" ]] || fail "could not resolve KubeVirt version (set KUBEVIRT_VERSION=v1.9.0)"
  echo "$latest"
}

resolve_kubevirt_version() {
  if [[ -n "${KUBEVIRT_VERSION}" ]]; then
    echo "${KUBEVIRT_VERSION}"
    return
  fi
  local latest="$(fetch_latest_kubevirt_version)"
  # HostDevicesWithDRA needs >= 1.9
  case "$latest" in
    v1.[0-8].*) echo "v1.9.0" ;;
    *) echo "$latest" ;;
  esac
}

detect_arch() {
  local arch="$(uname -m)"
  case "$arch" in
    s390x) ;;
    x86_64) arch=amd64 ;;
    aarch64) arch=arm64 ;;
  esac
  echo "$arch"
}

download_virtctl() {
  local ver="$1" arch="$2" bin="$3"
  info "virtctl not on PATH; downloading virtctl ${ver} linux-${arch} to ${bin}"
  curl -fL -o "$bin" \
    "https://github.com/kubevirt/kubevirt/releases/download/${ver}/virtctl-${ver}-linux-${arch}"
  chmod +x "$bin"
  hash -r 2>/dev/null || true
  command -v virtctl >/dev/null 2>&1 || fail "virtctl downloaded to ${bin} but not on PATH"
  echo "virtctl: $(command -v virtctl)"
}

ensure_virtctl() {
  local ver="$1"
  export PATH="${WORK_DIR}:${PATH}"
  if command -v virtctl >/dev/null 2>&1; then
    local installed_ver
    installed_ver="$(virtctl version --client 2>/dev/null | grep -oE 'v[0-9]+\.[0-9]+\.[0-9]+' | head -1 || true)"
    if [[ "${installed_ver}" == "${ver}" ]]; then
      echo "virtctl: skip (already $(command -v virtctl) ${installed_ver})"
      return
    fi
    fail "virtctl version mismatch: have ${installed_ver} at $(command -v virtctl), need ${ver}. Please update virtctl to ${ver} and re-run."
  fi
  local arch="$(detect_arch)"
  mkdir -p "${WORK_DIR}"
  local bin="${WORK_DIR}/virtctl"
  download_virtctl "$ver" "$arch" "$bin"
}
