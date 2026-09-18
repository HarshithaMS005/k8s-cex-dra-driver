#!/usr/bin/env bash
# Helper functions shared across e2e scripts.

fail()        { echo "ERROR: $*" >&2; exit 1; }
info()        { echo; echo "======== $* ========"; }
step()        { echo; echo "-------- $* --------"; }
need_cmd()    { command -v "$1" >/dev/null 2>&1 || fail "missing command: $1"; }
overlay_dir() { echo "${REPO_DIR}/deploy/kustomize/overlays/${OVERLAY}"; }
crd_exists()  { kubectl get crd "$1" >/dev/null 2>&1; }

# Portable base64 with no wrapping (GNU base64 -w0 vs BSD/macOS).
b64encode() {
  if printf x | base64 -w0 >/dev/null 2>&1; then
    printf '%s' "$1" | base64 -w0
  else
    printf '%s' "$1" | base64 | tr -d '\n'
  fi
}

# Host podman uses to reach the kubectl port-forward of the in-cluster registry.
# On macOS, 127.0.0.1 inside Podman Machine is the VM, not the Mac, so default
# to host.containers.internal. Override with REGISTRY_PUSH_HOST if needed.
registry_push_host() {
  if [[ -n "${REGISTRY_PUSH_HOST:-}" ]]; then
    echo "${REGISTRY_PUSH_HOST}"
    return
  fi
  if [[ "$(uname -s)" == "Darwin" ]]; then
    echo "host.containers.internal"
  else
    echo "127.0.0.1"
  fi
}
# Return 0 if kubectl get still finds the object(s).
still_there() { kubectl get "$@" >/dev/null 2>&1; }
# Delete and wait up to DELETE_WAIT seconds. Caller must then require_* / assert.
kdel() {
  kubectl delete "$@" --ignore-not-found=true --wait=true --timeout="${DELETE_WAIT}s" || true
}
items_exist() {
  kubectl get "$@" --no-headers 2>/dev/null | grep -q '[^[:space:]]'
}
