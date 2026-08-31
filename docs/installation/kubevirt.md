---
title: KubeVirt integration
weight: 3
description: >
  Assigning CEX queues to virtual machines with dynamic resource
  allocation (DRA)
---

This page covers the KubeVirt side of CEX passthrough: configure KubeVirt, create a ResourceClaimTemplate, reference it from a VirtualMachineInstance, verify in the guest.

## Prerequisites

- The node and cluster [prerequisites](prerequisites.md) met.
- KubeVirt 1.9.0 or later.
- The CEX DRA driver installed ([Driver installation](driver.md)) with the `ap-queue.virtual-machine.ibm.com` DeviceClass.
- The [virtctl](https://kubevirt.io/user-guide/user_workloads/virtctl_client_tool/) client for the SSH step under Verification.
- The KubeVirt CR configured as below: two feature gates and an externally-provided mediated device entry.

### Feature gates

Both gates are required, and neither implies the other: KubeVirt's create webhook rejects a VMI that sets `spec.domain.devices.hostDevices` without `HostDevices`, DRA-backed or not.
As of 1.9.0, `HostDevices` is Alpha and `HostDevicesWithDRA` is Beta, so both are opt-in.

```yaml
spec:
  configuration:
    developerConfiguration:
      featureGates:
        - HostDevices
        - HostDevicesWithDRA
```

### Mediated device ownership

KubeVirt must not manage vfio-ap mediated devices itself - the DRA driver creates and owns them.
Two settings in the KubeVirt CR:

```yaml
spec:
  configuration:
    mediatedDevicesConfiguration:
      mediatedDeviceTypes: []
    permittedHostDevices:
      mediatedDevices:
        - mdevNameSelector: "VFIO AP Passthrough Device"
          resourceName: "ibm.com/crypto-express"
          externalResourceProvider: true
```

- An empty `mediatedDeviceTypes` stops KubeVirt's built-in mediated device (mdev) management from creating vfio-ap devices on its own.
- The `permittedHostDevices` entry with `externalResourceProvider: true` is load-bearing: virt-handler periodically sweeps `/sys/bus/mdev/devices` and destroys every mdev whose type is not on its keep-list, with no check for who created it.
  Without this entry the driver's mediated device is torn out from under a running VM - silently: the guest's Adjunct Processor (AP) bus empties while the VMI stays Ready.
  This holds for every KubeVirt version through 1.9.0.

## Overview

The pieces fit together as follows:

1. The DRA driver publishes AP queues as ResourceSlice devices and, when a claim is allocated, prepares one vfio-ap mediated device covering all the claim's queues.
1. The VMI declares the claim under `spec.resourceClaims` and maps it into the guest under `spec.domain.devices.hostDevices`, one entry per claim.
1. KubeVirt attaches the mediated device to the guest, whose AP bus then shows the queues at their allocated adapter-domain positions.

The same flow drawn out actor by actor, from the claim reaching the API server to the queue appearing in the guest, is under [Claim lifecycle](../architecture/index.md#claim-lifecycle).
It is the quickest way to place a VMI that is stuck in `Scheduled`.

## Creating a ResourceClaimTemplate

Use the virtual-machine DeviceClass.
Select the card mode the guest needs:

```yaml
apiVersion: resource.k8s.io/v1
kind: ResourceClaimTemplate
metadata:
  namespace: default
  name: single-cca-ap-queue-vm
spec:
  spec:
    devices:
      requests:
        - name: cca-ap-queue
          exactly:
            deviceClassName: ap-queue.virtual-machine.ibm.com
            allocationMode: ExactCount
            count: 1
            selectors:
              - cel:
                  expression: device.attributes["cex.ibm.com"].type == "cca"
```

Selectors, constraints, and claim configuration work as described in [Requesting queues](../usage/usage.md).

## Creating a VMI with a CEX queue

The claim is declared once under `spec.resourceClaims` and consumed under `spec.domain.devices.hostDevices`.
`claimName` refers to the entry in `resourceClaims`, and `requestName` to the request inside the claim:

```yaml
apiVersion: kubevirt.io/v1
kind: VirtualMachineInstance
metadata:
  namespace: default
  name: vmi-cex
spec:
  domain:
    devices:
      disks:
        - name: containerdisk
          disk:
            bus: virtio
        - name: cloudinit
          disk:
            bus: virtio
      hostDevices:
        - name: cex-cca
          claimName: claim0
          requestName: cca-ap-queue
      rng: {}
    resources:
      requests:
        memory: 1Gi
      limits:
        memory: 1Gi
  volumes:
    - name: containerdisk
      containerDisk:
        image: quay.io/containerdisks/fedora:latest
    - name: cloudinit
      cloudInitNoCloud:
        userData: |-
          #cloud-config
          user: fedora
          ssh_authorized_keys:
            - ssh-ed25519 AAAA... you@example.com
          packages:
            - s390utils-base
  resourceClaims:
    - name: claim0
      resourceClaimTemplateName: single-cca-ap-queue-vm
```

The cloud-init volume seeds the `fedora` user with an SSH key so the verification step below can log in.
Replace the `ssh_authorized_keys` entry with your own public key (for example the output of `cat ~/.ssh/id_ed25519.pub`).
It also installs `s390utils-base` on first boot: the stock containerdisk lacks the `lszcrypt` the verification step runs, and until the install finishes `lszcrypt` reports `command not found`.

The `containerDisk` volume itself needs the image-volume prerequisites listed under [Prerequisites](prerequisites.md), which a stock Kubernetes 1.34 cluster does not meet.

The same pattern holds for a `VirtualMachine`: the fields sit under `spec.template.spec` instead.

For a claim with several requests (multi-type), keep a single `hostDevices` entry and point `requestName` at any one of the request names.
The claim's single mediated device already carries every allocated queue, so the guest sees all of them regardless of which request the entry names.
Adding one entry per request fails: libvirt accepts only one vfio-ap host device per guest, and the VMI sticks in `Scheduled` with `SyncFailed` events reporting `Only one hostdev of model vfio-ap is supported`.

## Verification

The VMI is running and the claim allocated:

```bash
kubectl get vmi vmi-cex
kubectl get resourceclaims
```

Inside the guest, the queue appears on the AP bus:

```bash
virtctl ssh fedora@vmi/vmi-cex -c 'lszcrypt'
```

The adapter and domain shown by `lszcrypt` match the `apid` and `apqi` attributes of the allocated ResourceSlice device.

If the queue disappears from the guest while the VMI stays Ready, check the `permittedHostDevices` entry above - that is the virt-handler mdev sweep reclaiming an unprotected mediated device.
