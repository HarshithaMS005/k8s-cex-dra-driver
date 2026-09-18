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
# Usage: run_on_node <snippet>   — fail the e2e script if the snippet fails
#        _run_on_node <snippet>  — return the snippet exit code (cleanup)
_run_on_node() {
  local snippet="$1"
  local node pod_name="cex-e2e-node"
  node="$(kubectl get nodes --no-headers -o custom-columns=NAME:.metadata.name | head -1)"
  [[ -n "$node" ]] || fail "no Kubernetes nodes found"

  # Clean up any leftover pod from a previous failed run.
  kubectl delete pod "${pod_name}" --ignore-not-found=true --wait=true \
    --timeout=30s >/dev/null 2>&1 || true

  # The init container mounts the host root at /host and runs the snippet via
  # chroot so all host binaries (lszcrypt, modprobe, …) and /sys are visible.
  # Both images are already cached on the node:
  #   quay.io/fedora/fedora:44  — provides bash + base64 for the init container
  #   registry.k8s.io/pause:3.10.1 — sandbox image used by Kubernetes 1.36
  local encoded_snippet
  encoded_snippet="$(b64encode "$snippet")"

  kubectl run "${pod_name}" \
    --restart=Never \
    --image=registry.k8s.io/pause:3.10.1 \
    --overrides='{
      "spec": {
        "hostPID": true,
        "nodeSelector": {"kubernetes.io/hostname": "'"${node}"'"},
        "tolerations": [{"operator": "Exists"}],
        "initContainers": [{
          "name": "'"${pod_name}"'",
          "image": "quay.io/fedora/fedora:44",
          "command": [
            "bash", "-c",
            "echo '"${encoded_snippet}"' | base64 -d | chroot /host bash"
          ],
          "securityContext": {"privileged": true},
          "volumeMounts": [{"name": "host-root", "mountPath": "/host"}]
        }],
        "containers": [{
          "name": "pause",
          "image": "registry.k8s.io/pause:3.10.1"
        }],
        "volumes": [{"name": "host-root", "hostPath": {"path": "/"}}]
      }
    }' >/dev/null

  echo "  (pod ${pod_name} created on node ${node}; waiting for completion...)"

  # Wait up to 2 minutes for the init container to finish.
  local deadline=$(( $(date +%s) + 120 ))
  local phase init_state
  while true; do
    init_state="$(kubectl get pod "${pod_name}" \
      -o jsonpath='{.status.initContainerStatuses[0].state}' 2>/dev/null || true)"
    if echo "${init_state}" | grep -q '"terminated"'; then
      break
    fi
    if (( $(date +%s) >= deadline )); then
      kubectl delete pod "${pod_name}" --ignore-not-found=true \
        --wait=false >/dev/null 2>&1 || true
      echo "ERROR: timed out waiting for node pod on ${node}" >&2
      return 1
    fi
    sleep 2
  done

  # Print the output and capture the exit code.
  echo
  kubectl logs "${pod_name}" -c "${pod_name}" 2>/dev/null || true
  local exit_code
  exit_code="$(kubectl get pod "${pod_name}" \
    -o jsonpath='{.status.initContainerStatuses[0].state.terminated.exitCode}' \
    2>/dev/null || true)"
  [[ -n "$exit_code" ]] || exit_code=1

  kubectl delete pod "${pod_name}" --ignore-not-found=true \
    --wait=false >/dev/null 2>&1 || true

  return "${exit_code}"
}

run_on_node() {
  local node
  node="$(kubectl get nodes --no-headers -o custom-columns=NAME:.metadata.name | head -1)"
  if ! _run_on_node "$1"; then
    fail "node command failed on ${node:-unknown} (see pod log above)"
  fi
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
    aarch64|arm64) arch=arm64 ;;
  esac
  echo "$arch"
}

download_virtctl() {
  local ver="$1" os="$2" arch="$3" bin="$4"
  info "virtctl not on PATH; downloading virtctl ${ver} ${os}-${arch} to ${bin}"
  curl -fL -o "$bin" \
    "https://github.com/kubevirt/kubevirt/releases/download/${ver}/virtctl-${ver}-${os}-${arch}"
  chmod +x "$bin"
  hash -r 2>/dev/null || true
  command -v virtctl >/dev/null 2>&1 || fail "virtctl downloaded to ${bin} but not on PATH"
  echo "virtctl: $(command -v virtctl)"
}

ensure_virtctl() {
  local ver="$1"
  mkdir -p "${WORK_DIR}"
  export PATH="${WORK_DIR}:${PATH}"
  if command -v virtctl >/dev/null 2>&1; then
    local installed_ver
    installed_ver="$(virtctl version --client 2>/dev/null | grep -oE 'v[0-9]+\.[0-9]+\.[0-9]+' | head -1 || true)"
    if [[ "${installed_ver}" == "${ver}" ]]; then
      echo "virtctl: skip (already $(command -v virtctl) ${installed_ver})"
      return
    fi
    echo "virtctl: have ${installed_ver:-unknown} at $(command -v virtctl); downloading ${ver} into ${WORK_DIR}"
  fi
  local os arch bin
  os="$(detect_os)"
  arch="$(detect_arch)"
  bin="${WORK_DIR}/virtctl"
  download_virtctl "$ver" "$os" "$arch" "$bin"
}
