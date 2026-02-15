# Competitor Setup

## Overview

This directory contains the setup for the competitor environment used in the usability evaluation. Competitor for multi-party manifest governance: **Kyverno** (policy enforcement) and **k8s-manifest-sigstore** (signing).

## Requirements

### Software

- **Go** `v1.25.5` (https://golang.org/dl/)
- **Docker** `v29.1.3` (https://docs.docker.com/get-docker/)
- **Kubectl** `v1.35.0` (https://kubernetes.io/docs/tasks/tools/)
- **Minikube** `v1.37.0` (https://minikube.sigs.k8s.io/docs/start/)
- **Git** `v2.47.3` (https://git-scm.com/downloads)
- **Helm** `v3.x` (https://helm.sh/docs/intro/install/)
- **kubectl-sigstore** `v0.5.x` (https://github.com/sigstore/k8s-manifest-sigstore)


Also, for proceeding the next steps, you need to install **go-task** tool `v3.46.3` for executing the tasks defined in the Taskfile and **yq** tool `v4.52.2` for processing YAML files. You can install it by running the following commands:

```bash
go install github.com/go-task/task/v3/cmd/task@latest
go install github.com/mikefarah/yq/v4@latest
task --version
yq --version
```

### Secrets

Since Cosign doesn't support PGP keys for signing, new cosign key pairs need to be generated for each governor:

| Governor | Private Key | Public Key |
|----------|-------------|------------|
| Owner | `secrets/owner-cosign.key` | `secrets/owner-cosign.pub` |
| Voter1 | `secrets/voter1-cosign.key` | `secrets/voter1-cosign.pub` |
| Voter2 | `secrets/voter2-cosign.key` | `secrets/voter2-cosign.pub` |

> Note: long-lived cosign key pairs are used instead of short-lived Sigstore keys to ensure a fair comparison with Qubmango's long-lived PGP keys.

## Setup

### 0. Create secrets

Generate cosign key pairs:

```bash
task 0-prepare:1-generate-cosign-keys
```

### 1. Minikube

1. Start a Minikube cluster (without cert-manager, ArgoCD):

```bash
task 1-minikube:1-start
```

2. Set the Docker environment to use Minikube's Docker daemon:

```bash
task 1-minikube:2-docker-env
```

3. Create all necessary namespaces in Minikube:

```bash
task 1-minikube:3-namespaces
```

### 2. Kyverno

Install and verify Kyverno:

```bash
task 2-kyverno:1-install
task 2-kyverno:2-verify
```

### 3. Kyverno ClusterPolicy

Create the policy enforcing `ALL(Owner, ANY(Voter1, Voter2))`. Public keys from `./secrets/` are embedded directly into the policy:

```bash
task 3-policy:1-create
task 3-policy:2-verify
```

### 4. OCI Registry

Start a local Docker OCI registry for storing signed manifest bundles:

```bash
task 4-registry:1-start
task 4-registry:2-verify
```

### 5. Application Manifests

Create the initial manifest files (same as `0. Setup (End result)`, without `app.yaml` and `mrt.yaml`):

```bash
task 5-manifests:1-create
```

## Teardown

```bash
task teardown:all
```

This removes Kyverno, namespaces, the ClusterPolicy, the OCI registry, and stops Minikube.
