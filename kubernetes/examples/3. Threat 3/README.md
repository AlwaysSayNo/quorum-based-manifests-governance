
# Threat 3: Cluster-side trust

### Objective

Verify that the system establishes a strong, cluster-side root of trust between *cryptographic identity* and *policy authorization*. A cryptographically valid PGP signature must be ignored if the signing key is not explicitly listed in the `MRT` board.

### Scenario

An insider attempts to push a change through an insecure approval process by colluding with an unauthorized voter.

**Policy**: `ALL(Owner, ANY(Voter1, Voter2))`. A valid approval requires the Owner signature and at least one signature from the trusted voter group (Voter1 or Voter2).

**Attack**: The Owner creates a feature branch, upgrades nginx to `1.28.2`, opens a Pull Request, gets it approved by **Voter3**, which is not part of the quorum policy, and merges it to `main`. When the controller creates a new `MSR` for that commit, the Owner and Voter3 sign it. The system must reject this as insufficient because Voter3 is not part of the trusted voter branch (`ANY(Voter1, Voter2)`) and the quorum is still unsatisfied.

### Prerequisites

1. Complete the steps in the [0. Setup README](../0.%20Setup/README.md).
2. Copy this scenario's `Taskfile.yaml` into the root of your test git repository (the repository **ArgoCD** and **Qubmango** are watching).
3. Ensure the `qubmango` CLI is installed and configured for:
   - **Owner** identity (repo config points to Owner SSH/PGP keys)
   - **Voter3** identity (repo config points to Voter3 SSH/PGP keys)

Notes:
- The CLI binds signing identity to the configured SSH/PGP key paths. To sign as Voter3 you’ll need to reconfigure the CLI (or use a separate machine/session with Voter3 config).
- The PR approval/merge step is intentionally manual to reflect a real-world weak approval process.

## Execution

### 0. Start governance

1. Start the governance process by committing/pushing the `MRT` manifest and triggering an ArgoCD refresh.

```bash
task 0-setup:1-start-governance
```

2. Verify that governance resources (`MRT`, `MSR`, `MCA`) exist, and capture the initial ArgoCD `Application` pin (`targetRevision`) for later comparison.

```bash
task 0-setup:2-verify-governance
```

3. Print the policy tree from `app-manifests/mrt.yaml` so it’s visible while you run the scenario.

```bash
task 0-setup:3-verify-policy
```

### 1. Collusive change

1. Create a feature branch and upgrade nginx to `1.28.2`:

```bash
task 1-collusion:1-branch-and-change
```

2. Push the feature branch to the remote repository.

```bash
task 1-collusion:2-push-branch
```

3. In GitHub, create a Pull Request from the feature branch into `main`, approve it as **Voter3**, and merge it.

4. Pull the updated `main` branch locally:

```bash
task 1-collusion:3-pull-main
```

### 2. Verify detection

1. Wait for the controller to detect the new commit and create the `MSR`:

```bash
task 2-verify-system:1-wait-detection
```

2. Get the `MSR` details. The version should be incremented (e.g., from `0` to `1`):

```bash
task 2-verify-system:2-show-msr
```

### 3. Attempt to bypass quorum

1. Sign the MSR as **Owner**. This satisfies the `Owner` branch of the policy, but should still be insufficient.

```bash
task 3-signatures:1-owner-sign
```

2. Reconfigure the CLI to **Voter3** identity and sign as Voter3.

This signature is expected to be *ignored* by the policy because **Voter3** is not authorized by the `MRT`.

```bash
task 3-signatures:2-voter3-sign
```

### 4. Verify defense

1. Wait for a few minutes to allow the controller to process the new signature and verify the `MSR`:

```bash
task 4-verify-state:1-verify-msr-in-progress
```

2. Verify ArgoCD remains pinned to the original safe commit (no `MCA` was issued).

```bash
task 4-verify-state:2-verify-argocd-pinned
```

## Outcomes

- **Owner’s** signature is accepted.
- **Voter3’s** signature is not counted because **Voter3** is not authorized by the `MRT` policy board. The trusted voter branch `ANY(Voter1, Voter2)` remains unsatisfied, so the `MSR` stays **In Progress**.
- No new `MCA` is issued, and ArgoCD `targetRevision` is not updated.

## Cleanup

1. Revert the local repo state (revert nginx change and remove the `MRT` file), then push the cleanup commit.

```bash
task 5-cleanup:1-repo
```

2. Delete the `MRT` from the cluster.

```bash
task 5-cleanup:2-cluster
```

3. Force ArgoCD to refresh to converge back to the clean state.

```bash
task 5-cleanup:3-sync
```
