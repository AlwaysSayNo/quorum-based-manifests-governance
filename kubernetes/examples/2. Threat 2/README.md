
# Threat 2: Weak approval process

### Objectives

Verify that **Qubmango** enforces the quorum policy from the **MRT** and refuses deployment when the policy tree is not fully satisfied.

### Scenario

An insider attempts to push a change through an insecure approval process by colluding with an unauthorized voter.

**Policy**: `ALL(Owner, ANY(Voter1, Voter2))`. A valid approval requires the Owner signature and at least one signature from the trusted voter group (Voter1 or Voter2).

**Attack**: The Owner creates a feature branch, upgrades nginx to `1.28.2`, opens a Pull Request, gets it approved by **Voter3**, which is not part of the quorum policy, and merges it to `main`. When the controller creates a new `MSR` for that commit, the Owner signs it. The system must reject this as insufficient because the trusted voter branch (`ANY(Voter1, Voter2)`) is still unsatisfied.

### Prerequisites

1. Complete the steps in the [0. Setup README](../0.%20Setup/README.md).
2. Copy this scenario's `Taskfile.yaml` into the root of your test git repository (the repository **ArgoCD** and **Qubmango** are watching).
3. Ensure the `qubmango` CLI is installed and configured for:
- **Owner** identity (repo config points to Owner SSH/PGP keys)

Notes:
- This scenario simulates the “PR merged with a weak approver” by creating a feature branch, creating a PR, approving as Voter3 and merging it to `main`. The key expected behavior is that *Git approvals do not bypass the cryptographic quorum policy*.

## Execution

### 0. Start governance

1. Run the following command to start the governance process by pushing the initial manifest to the remote repository and triggering the Argo CD sync:

```bash
task 0-setup:1-start-governance
```

2. Verify that the governance resources (MRT, MSR, MCA) are created in the cluster and the **ArgoCD** `Application` is pinned to the initial Commit SHA:

```bash
task 0-setup:2-verify-governance
```

### 1. Collusive change

1. Create a feature branch and upgrade nginx to `1.28.2`:

```bash
task 1-collusion:1-branch-and-change
```

2. Push the feature branch to the remote repository:

```bash
task 1-collusion:2-push-branch
```

3. Create a Pull Request from the feature branch into `main`, approve it as **Voter3**, and merge it.

This reflects the real-world weakness: the code passes a basic Git approval process, but should still be blocked by the cryptographic quorum policy.

4. After the PR is merged, update your local `main` branch:

```bash
task 1-collusion:3-pull-main
```

### 2. Verify detection

1. Wait for the controller to detect the new commit and create the `MSR`:

```bash
task 2-verify-system:1-wait-detection
```

1. Get the `MSR` details:
```bash
task 2-verify-system:2-show-msr
```

### 3. Attempt to bypass quorum

1. Sign the MSR as **Owner** using the `qubmango` CLI configured with the Owner's credentials:

```bash
task 3-signatures:1-owner-sign
```

### 4. Verify defense

1. Wait for a few minutes to allow the controller to process the new signature and evaluate the policy:

```bash
task 4-verify-state:1-verify-msr-in-progress
```

2. 

## Outcomes

- Owner’s signature is accepted.
- A GitHub PR approval by Voter3 does **not** satisfy `ANY(Voter1, Voter2)`.
- (Optional) Even if Voter3 attempts to sign, it still does **not** satisfy `ANY(Voter1, Voter2)`.
- No new `MCA` is issued, and ArgoCD `targetRevision` is not updated.

## Cleanup

```bash
task 5-cleanup:1-repo
task 5-cleanup:2-cluster
task 5-cleanup:3-sync
```

