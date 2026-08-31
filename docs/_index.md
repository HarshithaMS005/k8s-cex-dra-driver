---
title: Documentation
linkTitle: Docs
weight: 10
description: >
  User documentation for the CEX DRA driver
---

The documentation is organized as follows:

- [Quickstart](quickstart.md): the end-to-end happy path, from install to a VirtualMachine with a CEX queue.
- [Installation](installation/_index.md): install the driver and configure KubeVirt integration.
  - [Prerequisites](installation/prerequisites.md): support matrix and node and cluster requirements before the install.
  - [Driver installation](installation/driver.md): the Kustomize-based install, in detail.
  - [KubeVirt integration](installation/kubevirt.md): configure KubeVirt and pass CEX queues into virtual machines.
- [Concepts](concepts.md): the APQN device model, DeviceClasses, and the attribute taxonomy the driver publishes.
- [Architecture](architecture/index.md): how the driver, the scheduler, kubelet, and the AP bus fit together, in diagrams.
- [Usage](usage/_index.md): requesting and configuring queues.
  - [Requesting queues](usage/usage.md): ResourceClaims, CEL selectors, constraints, and claim configuration.
  - [Claim generator](usage/generator.md) (experimental): build a claim and workload manifest interactively from the queues your cluster publishes.
- [Reference](reference/_index.md): claim configuration, device attributes, driver options, and feature gates.
  - [Claim configuration](reference/claim-configuration.md): the `CryptoConfig` schema and the control-domain modes.
  - [DeviceClasses and attributes](reference/devices.md): the shipped DeviceClasses and the attributes published for every queue.
  - [Driver options](reference/driver-options.md): every flag and environment variable the kubelet plugin accepts.
  - [Feature gates](reference/feature-gates.md): gate semantics, the gates this release defines, and verifying the resolved map.

## License and source

The documentation is part of the CEX DRA driver repository and shares its license, the Apache License, Version 2.0.
The `LICENSE` file at the repository root carries the full text.
The documentation for a given driver release is these pages at that release tag.
