# Qubmango

[![LinkedIn](https://img.shields.io/badge/LinkedIn-Nazar%20Grynko-0A66C2?logo=linkedin&logoColor=white)](https://www.linkedin.com/in/nazar-grynko-354b90247/)
[![Releases](https://img.shields.io/github/v/release/AlwaysSayNo/quorum-based-manifests-governance?label=Latest%20release&logo=github)](https://github.com/AlwaysSayNo/quorum-based-manifests-governance/releases)

<img src="qubmango-logo.png" alt="Qubmango logo" width="128" />

Qubmango is Kubernetes-native governance application for GitOps workflows, which enables secure management of Kubernetes manifests through a quorum-based approval process.

The project consists of two parts: Admission Controller and CLI.

## Qubmango Admission Controller

The Qubmango Admission Controller is a Kubernetes admission controller that intercepts manifest changes in a Git repository and enforces a quorum-based approval process before allowing the changes to be applied to the cluster. It makes sure that any changes to the Kubernetes manifests go through a defined approval process, establishing cryptographic trust and traceability for changes made to the cluster.

It allows to block unapproved manifests coming from the Git repository, notify governors about them and manage asynchronous batch approvals for manifest changes.

For more details, see the [kubernetes/README.md](kubernetes/README.md).

## Qubmango CLI

The Qubmango CLI is a command-line tool that allows governors to interact with the Qubmango in-cluster system. It provides commands for reviewing and approving signing requests (MSRs), as well as viewing the history of changes (MCAs).

For more details, see the [cli/README.md](cli/README.md).