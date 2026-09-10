#!/usr/bin/env bash
# VM lifecycle and claim helpers for e2e tests.
# Sourced by cex-dra.sh; requires SCRIPT_DIR, TEST_NS, VM_NAME, FEDORA_DISK,
# and helper/deploy/vm-check functions to be in scope.

# Ensure SSH key, resolve CEX type, and create the test namespace.
# Exports CTYPE and REQ for use by the manifest step.
_vm_prepare() {
  ensure_ssh_key
  CTYPE="$(cex_type_from_cluster)"
  REQ="${CTYPE}-ap-queue"
  echo "Selecting cex.ibm.com/type == \"${CTYPE}\""
  kubectl create namespace "${TEST_NS}" --dry-run=client -o yaml | kubectl apply -f -
}

# Render the VMI template and apply it to the cluster.
_vm_apply_manifest() {
  CTYPE="${CTYPE}" \
  REQ="${REQ}" \
  TEST_NS="${TEST_NS}" \
  VM_NAME="${VM_NAME}" \
  FEDORA_DISK="${FEDORA_DISK}" \
  SSH_PUB="${SSH_PUB}" \
    envsubst < "${SCRIPT_DIR}/vmi-cex-claim.yaml.tmpl" | kubectl apply -f -
}

# Poll until the VMI reaches phase=Running or phase=Failed.
_vm_poll_phase() {
  echo "Waiting for VMI phase=Running (image pull + boot)..."
  local i phase ready
  for i in $(seq 1 90); do
    phase="$(kubectl get vmi "${VM_NAME}" -n "${TEST_NS}" -o jsonpath='{.status.phase}' 2>/dev/null || true)"
    ready="$(kubectl get vmi "${VM_NAME}" -n "${TEST_NS}" -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' 2>/dev/null || true)"
    echo "  t=${i}0s phase=${phase} Ready=${ready}"
    [[ "$phase" == "Running" ]] && break
    [[ "$phase" == "Failed" ]] && { kubectl describe vmi "${VM_NAME}" -n "${TEST_NS}" || true; fail "VMI Failed"; }
    sleep 10
  done
}

# Print final resource state and assert the VMI is Running.
_vm_assert_running() {
  kubectl get vmi "${VM_NAME}" -n "${TEST_NS}"
  kubectl get resourceclaims -n "${TEST_NS}"
  kubectl get vmi "${VM_NAME}" -n "${TEST_NS}" -o jsonpath='{.status.phase}' | grep -qx Running \
    || fail "VMI not Running"
}

start_vm() {
  info "Start VM with one CEX queue"
  _vm_prepare
  _vm_apply_manifest
  _vm_poll_phase
  _vm_assert_running
}
