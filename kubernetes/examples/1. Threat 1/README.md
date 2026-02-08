# Threat 1: Manifest Tampering

### Objectives

Verify that the **Qubmango** detects and rejects changes to the manifests that have not passed through the governance workflow.

### Scenario

A malicious actor (Owner) manually modifies the `nginx-deployment.yaml` changing the image from `1.14.2` to `latest` and pushes directly to the `main` branch, bypassing the signing process. We expect **Qubmango** to detect the change but prevent **ArgoCD** from applying it.

### Prerequisites

1. Complete the steps in the [0. Setup README](../0.%20Setup/README.md).
2. Copy this scenario's `Taskfile.yaml` into the root of your test git repository (the repository **ArgoCD** and **Qubmango** are watching).

## Execution

### 0. Start governance

1. Install the governance resources to the cluster by applying the provided manifest:

```bash
task 0-setup:1-apply-governance-init
```

2. Start the governance process by committing/pushing the `MRT` manifest and triggering an ArgoCD refresh.

```bash
task 0-setup:2-start-governance
```

2. Verify that governance resources (`MRT`, `MSR`, `MCA`) exist, and capture the initial ArgoCD `Application` pin (`targetRevision`) for later comparison.

```bash
task 0-setup:3-verify-governance
```

### 1. Malicious changes

As a malicious administrator, we will modify the manifest locally and push it directly to the remote repository.

1. Modify the `nginx-deploy.yaml` image to `nginx:latest`:

```bash
task 1-malicious-changes:1-change-image
```

2. Commit and push the malicious change directly to `main`:

```bash
task 1-malicious-changes:2-commit-push
```

### 2. Verify defense

The **Qubmango** controller should detect the new commit in the repository. Because this commit lacks a valid `MSR` signed by the required quorum, the controller should not create an `MCA`. Consequently, `ArgoCD` should not update the `Application`.

1. Verify the controller detected the unapproved changed:

```bash
task 2-verify-system:1-wait-detection
```

2. Verify that admission controller blocked the changes.

```bash
task 3-verify-state:1-verify-no-update
```

## Outcomes

The malicious change is detected by the controller, but not applied to the cluster. The `Application` remains in sync with the last known approved commit, and the cluster state is unchanged.

## Cleanup

To clean up the resources created during this test scenario, run the following command:

1. Clean the repository by reverting the malicious change, removing the MRT file:

```bash
task 4-cleanup:1-repo
```

2. Delete the MRT from the cluster. ArgoCD will be automatically reset to track HEAD and the controller will clean up associated MSR/MCA. 

```bash
task 4-cleanup:2-cluster
```

3. Force ArgoCD to sync to the clean state:

```bash
task 4-cleanup:3-sync
```

4. Finally, clean up the governance initialization from the cluster:

```bash
task 4-cleanup:4-governance-init
```

5. Restore the MRT file for the next scenario:

```bash
task 4-cleanup:5-restore-mrt
```