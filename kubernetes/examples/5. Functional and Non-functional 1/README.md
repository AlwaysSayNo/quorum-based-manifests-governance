# Functional and Non-functional Evaluation

## Experimental Setup

### Test Environment

Both setups use the same baseline infrastructure:

| Tool | Version |
|------|---------|
| Go | `v1.25.5` |
| Docker | `v29.1.3` |
| Kubectl | `v1.35.0` |
| Minikube | `v1.37.0` |
| Git | `v2.47.3` |

### 1. QuBManGo Setup

Located in: `../0. Setup/`

**Components**:
- Minikube cluster with cert-manager and ArgoCD
- QuBManGo controller with validating webhook
- ManifestRequestTemplate (MRT) with quorum policy: `ALL(Owner, ANY(Voter1, Voter2))`
- QuBManGo CLI tool configured for governors
- Automatic Slack notification integration

### 2. Competitor (Kyverno + k8s-manifest-sigstore) Setup

Located in: `./competitor/`

**Components**:
- Minikube cluster (no cert-manager or ArgoCD)
- Kyverno admission controller
- Kyverno `ClusterPolicy` requiring 2 signatures — Owner + one Voter
- kubectl-sigstore plugin for manifest signing
- Local OCI registry for signed bundles

**Constraints**:
- No CI/CD automation for bundling or notification
- Governors coordinate manually
- Standard terminal tools: `git`, `kubectl-sigstore`
- Uses long-lived cosign keys (fair comparison with QuBManGo's PGP keys)

## Test Scenario

### Trigger Event

A developer pushes a commit modifying three manifest files:

| Resource | Change |
|----------|--------|
| Deployment (`nginx-deploy`) | Image tag: `nginx:1.14.2` → `nginx:1.20.0` |
| Namespace (`test-ns`) | Annotation added: `environment: staging` |
| ConfigMap (`config-my`) | Value: `version: v0.1.0` → `version: v0.2.0` |

### Success Criteria

Two governors (**Owner** and **Voter1**) must:
1. Receive notification of changes
2. Review the modifications
3. Apply cryptographic signatures to authorize deployment

## Prerequisites

1. **QuBManGo**: Complete `../0. Setup/README.md` end-to-end. Ensure `qubmango` CLI is configured for Owner and Voter1.
2. **Competitor**: Complete `./competitor/README.md` setup. Ensure cosign key pairs exist in `./competitor/secrets/`.
3. Copy each scenario's `Taskfile.yaml` into the respective test repository root.

---

## QuBManGo — Execution Workflow

### Background: Developer pushes changes

The developer has already committed and pushed three manifest changes to the `main` branch:

```bash
task 6-changes:1-apply
task 6-changes:2-commit-push
```

The QuBManGo controller automatically detects the new commit and:
1. Creates a new `ManifestSigningRequest` with the updated version
2. Sends notifications to all configured channels (Slack)

### Phase 1: Notification

Each governor receives and reads the Slack notification.

**Phase total actions: n**

### Phase 2: Review

Each governor reviews the MSR and the file diff using the CLI.

**Step 1 — Review MSR status and policy requirements:**

```bash
qubmango get msr --repo <repository-alias>
```

Steps for action: n

**Step 2 — Review file diffs:**

```bash
qubmango get file-diff --repo <repository-alias>
```

Steps for action: n

**Phase total actions: 2n**

### Phase 3: Signing

Each governor signs the MSR. The CLI pushes the signature to the remote repository automatically.

```bash
qubmango sign msr <msr-version> --repo <repository-alias>
```

Steps for action: n

**Phase total actions: n**

### QuBManGo Summary

| Phase | Actions per n Governor | Total (n=2, k=1) |
|-------|---------------------|-------------|
| Notification | n | 2 |
| Review | 2n | 4 |
| Signing | n | 2 |
| **Total** | **4n** | **8** |

---

## Competitor — Execution Workflow

### Background: Developer pushes changes

The developer has already committed and pushed three manifest changes (the same changes as in QuBManGo) to the `main` branch:

```bash
task 6-changes:1-apply --taskfile ./competitor/Taskfile.yaml
task 6-changes:2-commit-push --taskfile ./competitor/Taskfile.yaml
```

Unlike QuBManGo, there is **no automatic detection or notification**.

The developer must manually bundle manifests and notify governors.

**Step 1 — Developer bundles manifests into an OCI image (and pushes to registry):**

`kubectl-sigstore sign` bundles the YAML files into an OCI image and pushes to the registry in one command.

```bash
kubectl-sigstore sign \
  -f app-manifests/nginx-deploy.yaml \
  -f app-manifests/namespace.yaml \
  -f app-manifests/configmap.yaml \
  -k ./secrets/developer-cosign.key \
  --image localhost:5000/manifests:test-bundle
```

Steps for action: 1

**Step 2 — Developer notifies governors via external channels:**

Developer must manually notify governors through k communication platforms (Slack).

Steps for action: k

**Step 3 — Governors read notifications:**

Each governor receives and reads the Slack notification.

**Phase total actions: 1 + k + n**

### Phase 2: Review

Each governor must manually pull the repository, find the last approved commit, and generate a diff.

**Step 1 — Governor pulls latest changes:**

```bash
git pull origin main
```

Steps for action: n

**Step 2 — Governor identifies the previous approved commit:**

Governor manually searches through git history to find the last approved commit hash. Governor must know/find the commit hash of the last approved state.

```bash
git log --oneline
```

Steps for action: n

**Step 3 — Governor reviews diff:**

```bash
git diff <prev-approved-commit> HEAD -- app-manifests/
```

Steps for action: n

**Step 4 — Governor OCI bundle:**

Governor pulls the signed bundle from the OCI registry to review the exact contents.

Steps for action: n

**Phase total actions: 4n**

### Phase 3: Signing

**Step 1 — Governor signs the OCI bundle:**

Each governor signs the OCI bundle using their private cosign key. The signature is automatically pushed to the OCI registry via `kubectl-sigstore sign`.

```bash
# Owner signs
kubectl-sigstore sign \
  -k ./secrets/owner-cosign.key \
  --image localhost:5000/manifests:test-bundle \
  --append-signature

# Voter1 signs
kubectl-sigstore sign \
  -k ./secrets/voter1-cosign.key \
  --image localhost:5000/manifests:test-bundle \
  --append-signature
```

Steps for action: n

**Step 2 — Developer applies the signed bundle:**

Once the required signatures are present in the OCI registry, an operator must manually apply the changes to the cluster. Kyverno will verify signatures before allowing the changes.

```bash
kubectl-sigstore apply -f localhost:5000/manifests:test-bundle
```

Phase total actions: 1

**Phase total actions: n + 1**

### Competitor Summary

| Phase | Actions per n Governor | Total (n=2, k=1) |
|-------|---------|-------------------|
| Notification | 1 + k + n | 4 |
| Review | 4n | 8 |
| Signing | n+1 | 3 |
| **Total** | **2 + k + 6n** | **15** |

---

## Results

### Action Count Comparison

| Approach | Notification | Review | Signing | **Total** |
|----------|-------------|--------|---------|-----------|
| **QuBManGo** | n | 2n | n | **4n = 8** |
| **Competitor** | 1 + k + n | 4n | n+1 | **2 + k + 6n = 15** |

## Cleanup

### QuBManGo

Revert developer changes in the test repository:

```bash
task 6-changes:3-revert
```

### Competitor

Revert developer changes and reset the cluster:

```bash
# In ./competitor/
task 7-changes:1-revert-git
task 7-cleanup:2-cluster
```

Full teardown (removes Kyverno, registry, stops Minikube):

```bash
# In ./competitor/
task 8-teardown:all
```
