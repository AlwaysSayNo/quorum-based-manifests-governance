# Qubmango In-Cluster Application

Qubmango is a Kubernetes operator that provides quorum-based governance for GitOps manifests. It integrates with Argo CD to enforce approval workflows before manifest changes are applied to your cluster.

## Prerequisites

Before installing Qubmango, ensure your cluster has the following:

### Required

- **Kubernetes** `v1.28` or higher
- **Argo CD** `v3.2` or higher

### Certificate Management

Qubmango uses webhooks that require TLS certificates. You have two options:

#### Option 1: Cert-Manager (Recommended)

Install **Cert-Manager** `v1.13` or higher:

```bash
kubectl apply -f https://github.com/cert-manager/cert-manager/releases/download/v1.13.0/cert-manager.yaml
kubectl wait --for=condition=ready pod --all -n cert-manager --timeout=300s || true
```

Qubmango will automatically use Cert-Manager to provision certificates for its webhooks.

#### Option 2: Manual Certificate Management

If you prefer not to use Cert-Manager, you must manually provision and manage CA certificates and TLS certificates for Qubmango webhooks. See the [Kubernetes Admission Webhooks documentation](https://kubernetes.io/docs/reference/access-authn-authz/extensible-admission-controllers/) for details on certificate requirements.

## Installation

Install Qubmango by applying the installation manifest from the official repository:

```bash
kubectl apply -f https://github.com/AlwaysSayNo/quorum-based-manifests-governance/releases/latest/download/install.yaml
kubectl wait --for=condition=ready pod --all -n controller-system --timeout=300s || true
```

This creates all the resources needed to run the Qubmango operator inside the `controller-system` namespace.

## Starting Governance Process

### 1. Prepare Git Repository

Ensure you have a Git repository containing the Kubernetes manifests you want to govern. For example:

```
git-repository/
└── app-manifests/
    ├── namespace.yaml
    ├── deployment.yaml
    ├── service.yaml
    └── application.yaml
```

### 2. Argo CD Application

The Argo CD `Application` manifest that points to the repository must be provisioned in the repository and applied to the cluster. Minimal example of the `Application` manifest:

```yaml
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: application-my
  namespace: argocd
spec:
  project: default
  source:
    # The URL of the public GitHub repository
    repoURL: https://github.com/org/git-repository.git
    # The branch Argo CD should track
    targetRevision: HEAD
    # The path within the repository, Argo CD should monitor
    path: app-manifests
    # # Optional: If application would have spec.source.path empty,
    # # we would need to exclude Qubmango's operational folder from syncing.
    # # For this example, the monitored folder is "app-manifests", and Qubmango's folder is ".qubmango",
    # # so we don't need to exclude anything. Otherwise, uncomment this section and adjust the path.
    # directory:
    #   exclude: governanceFolderPath/.qubmango
  destination:
    # The Kubernetes cluster URL
    server: https://kubernetes.default.svc
    # The namespace within the cluster where the application will be deployed
    namespace: app-my
  # Sync policy configuration
  syncPolicy:
    automated:
      prune: true
      selfHeal: true
    syncOptions:
      # Don't override manual changes made to certain fields (see Argo CD documentation for details)
      - RespectIgnoreDifferences=true
  # Fields to ignore during synchronization. Qubmango will update the targetRevision field,
  # so we need to ignore it to prevent Argo CD from constantly trying to revert the change.
  ignoreDifferences:
    - group: argoproj.io
      kind: Application
      name: application-my # Matches its own name
      namespace: argocd # Matches its own namespace
      jsonPointers:
        - /spec/source/targetRevision
```

> Note:
> 1. `Application` must have namespace `argocd`. Qubmango doesn't support governing `Applications` deployed in other namespaces so far.
> 2. When `spec.source.path` is empty, it means that Argo will monitor the whole project. Since, after governance project start, Qubmango needs to create its operational folder in `governanceFolderPath/.qubmango`. In this case, you need to exclude `governanceFolderPath/.qubmango` from Argo CD synchronization to prevent it from trying to deploy Qubmango's operational manifests to the cluster. If your `Application` already has a non-empty `spec.source.path`, and your `governanceFolderPath` is outside of it, then you don't need to exclude anything, since Argo CD will only monitor the specified path and won't interfere with Qubmango's folder.
> 3. After the governance process is established, Qubmango will manage the `targetRevision` field of the `Application`. That's why we need to ignore differences on this field, otherwise Argo CD will constantly try to revert the changes made by Qubmango and the governance process won't work.

If you haven't already applied the `Application` manifest to the cluster, do:

```bash
kubectl apply -f application.yaml
```

### 3. Secrets Creation and Provisioning

Qubmango requires three types of secrets for the governance process:

#### SSH Secret

Create a Kubernetes Secret containing the SSH private key for Git repository access:

```bash
kubectl create secret generic ssh-secret-my \
  --from-file=ssh-privateKey=/path/to/ssh-private-key \
  --from-literal=passphrase="ssh-key-passphrase" \
  --namespace=my-app-namespace
```

> Note:
> 1. If your SSH key doesn't have a passphrase, you can omit the `passphrase` field or set it to an empty string.
> 2. The SSH private key must be added as a deploy key to the Git repository with `read` and `write` permissions, so that Qubmango can create branches and commits for the governance process.

#### PGP Secret

Create a Kubernetes Secret containing the PGP private key for signing governance artifacts and Git commits:

```bash
kubectl create secret generic pgp-secret-my \
  --from-file=privateKey=/path/to/pgp-private-key.asc \
  --from-literal=passphrase="pgp-key-passphrase" \
  --namespace=my-app-namespace
```

> Note:
> 1. If your PGP key doesn't have a passphrase, you can omit the `passphrase` field or set it to an empty string.

#### Slack Secret

If you want Slack notifications for governance events, create a Secret with Slack Bot token:

```bash
kubectl create secret generic slack-secret-my \
  --from-literal=token="xoxb-your-slack-bot-token" \
  --namespace=my-app-namespace
```

> Note:
> 1. To create a Slack Bot:
>       1. Follow the https://api.slack.com/apps
>       2. It must have the `chat:write` scope under OAuth & Permissions
>       3. It must be installed to the target workspace

### 4. Create Manifest Request Template (MRT)

The MRT is the governance policy document. It defines:
- Git repository configuration
- PGP/SSH secrets for authentication
- Argo CD Application to manage
- Governance folder path
- Governor board and approval rules
- Notification channels

Here's a minimal MRT example:

```yaml
apiVersion: governance.nazar.grynko.com/v1alpha1
# Governance policy type
kind: ManifestRequestTemplate
metadata:
  # Policy name
  name: my-app-mrt
  # Policy namespace
  namespace: my-app-namespace
spec:
  # Incremental version of the policy. Should be bumped up on every change.
  # Initial value must be more than 0.
  version: 1
  gitRepository:
    ssh:
      # Git SSH URL (must be the same as in Argo CD Application, but only SSH format)
      url: git@github.com:org/git-repository.git
      # Kubernetes SSH secret reference
      secretsRef:
        name: ssh-secret-my
        namespace: my-app-namespace
  pgp:
    # PGP public key for signature verification (ASCII-armored)
    publicKey: |
      -----BEGIN PGP PUBLIC KEY BLOCK-----
      ... operator's PGP public key ...
      -----END PGP PUBLIC KEY BLOCK-----
    # Kubernetes secret reference for PGP key
    secretsRef:
      name: pgp-secret-my
      namespace: my-app-namespace
  notifications:
    # Slack notification configuration
    slack:
      # Kubernetes secret reference for Slack token
      secretsRef:
        name: slack-secret-my
        namespace: my-app-namespace
  argoCD:
    # ArgoCD application reference (must be the same as the one created in Step 2)
    application:
      name: application-my
      namespace: argocd
  # Path in the Git repository for governance-related files. Qubmango will use it to create operational folder with path `governanceFolderPath/.qubmango`.
  # This will be used to store MSR/MCA metadata and signatures.
  # So it must not interfere with Argo CD Application's monitored path (see 2. Argo CD Application setup).
  governanceFolderPath: governance
  # MSR reference (used on new MSR instances)
  msr:
    name: my-app-msr
    namespace: my-app-namespace
  # MCA reference (used on new MCA instances)
  mca:
    name: my-app-mca
    namespace: my-app-namespace
  governors:
    # Notification channels for governors
    notificationChannels:
      # Slack channel ID
      - slack:
          channelID: C01234567
    # Governance board members
    members:
      - alias: owner
        publicKey: |
          -----BEGIN PGP PUBLIC KEY BLOCK-----
          ... owner's PGP public key ...
          -----END PGP PUBLIC KEY BLOCK-----
      - alias: voter1
        publicKey: |
          -----BEGIN PGP PUBLIC KEY BLOCK-----
          ... voter1's PGP public key ...
          -----END PGP PUBLIC KEY BLOCK-----
  # Admission rules for MSR approval
  require:
    all: true
    require:
      - signer: owner
      - signer: voter1
```

### 5. Apply the MRT

Save the MRT manifest to repository (e.g., `app-manifests/mrt.yaml`) and push it:

```bash
git add app-manifests/mrt.yaml
git commit -m "Add governance policy"
git push origin main
```

Wait until Argo CD synchronizes the MRT manifest or apply it directly to the cluster (make sure that target namespace exists):

```bash
kubectl apply -f app-manifests/mrt.yaml
```

### 6. Governance Active

Once the MRT is applied to the cluster, Qubmango will:
1. Initialize the governance process
2. Create a `.qubmango` folder in your repository's `governanceFolderPath` (e.g., `governance/.qubmango`) to store governance artifacts
3. Create the initial MSR and MCA resources with `v0`, and push them with their signatures to the Git repository 
5. Take control of the Argo CD Application's `targetRevision`

From this point forward, all manifest changes in the repository will require approval through the governance process before being synced by Argo CD.

> Note:
> 1. Qubmango only blocks manifests submitted by Argo CD. If you have other automation, it won't work.
> 2. Qubmango doesn't block direct `kubectl apply` to the cluster, so if someone applies changes directly to the cluster, Qubmango won't be able to prevent it. This behavior will be improved later by adding the ability to block direct `kubectl apply` to the cluster and enforce all changes to go through the governance process.

## Governance

### Lifecycle

Once Qubmango is initialized, it will:
1. Monitor repository for changes
2. Create MSR for each change within the monitored Git folder (saves it in operational folder) and notify governors via communication channels
3. Governors review the MSR and submit their signatures for approval
4. Once the approval rules are satisfied, Qubmango creates an MCA (saves it in operational folder), notify governors and updates the Argo CD Application's `targetRevision` to trigger synchronization of the approved changes to the cluster

### Stopping the governance process

Stopping the governance process can be done by deleting the MRT resource in the Git repository. When Qubmango detected this change, created MSR, this request was approved by the governance board and Qubmango created MCA, Qubmango will stop the governance process and clean up all its artifacts. Argo CD Application will no longer be governed and its `targetRevision` field will be rolled back to the value before governance has started.

### Notes

#### Governance Folder

Before starting the governance process the governance folder must be empty or not exist. If there are any existing files in it, Qubmango will fail to initialize to prevent conflicts with existing governance artifacts.

In the end of governance process (when it's stopped), Qubmango will delete the governance folder and all its contents.

#### Altering MRT

If the MRT is altered in the Git repository, it should:

1. Have incremented version
2. Respect the immutable fields

Otherwise the change will be rejected and MSR won't be created. On this occasion, Qubmango will notify the governors.

#### Immutable fields

The following MRT fields are immutable and cannot be changed after the governance process has started:

- `spec.gitRepository.ssh` - Git repository SSH configuration (URL and secret reference)
- `spec.pgp` - PGP configuration (public key and secret reference)
- `spec.notifications.slack` - Slack notification configuration (can only be removed by setting to `null`, but cannot be modified)
- `spec.argoCD` - Argo CD Application reference
- `spec.msr` - MSR resource reference
- `spec.mca` - MCA resource reference
- `spec.governanceFolderPath` - Governance folder path in the repository

## Configuration Options

### MRT Configuration

The MRT resource supports the following configuration options:

#### Git Repository Configuration

```yaml
gitRepository:
  ssh:
    url: git@github.com:org/repo.git  # SSH URL of the repository
    secretsRef:
      name: ssh-secret # Name of the Kubernetes secret
      namespace: namespace # Namespace of the secret
```

#### PGP Configuration

```yaml
pgp:
  publicKey: | # PGP public key (ASCII-armored)
    -----BEGIN PGP PUBLIC KEY BLOCK-----
    ...
    -----END PGP PUBLIC KEY BLOCK-----
  secretsRef:
    name: pgp-secret # Name of the Kubernetes secret
    namespace: namespace # Namespace of the secret
```

#### Notification Configuration

```yaml
notifications:
  slack: # Slack notifications
    secretsRef:
      name: slack-secret
      namespace: namespace
```

> Note: other notification channels will be added later.

#### Argo CD Configuration

Qubmango must have reference to the underlying Argo CD `Application`:

```yaml
argoCD:
  application:
    name: app-name # Name of the Argo CD Application
    namespace: argocd # Namespace where Application is deployed
```

#### Governance Folder Path

The Governance folder must not interfere with Argo CD `Application` monitored path. For example, if Argo CD monitors `app-manifests`, the governance folder can be at `governance/.qubmango` or any other path outside of `app-manifests`. If `Application` monitors the whole repository (i.e., `spec.source.path` is empty), then you must set the governance folder to a subfolder (e.g., `.qubmango`) and exclude it from Argo CD synchronization to prevent conflicts.

```yaml
governanceFolderPath: path # Path in repo where .qubmango will be created
```

If omitted, defaults to the repository root.

#### Governor Configuration

```yaml
governors:
  notificationChannels: # List of channels for notifying governors
    - slack:
        channelID: C01234567
  members: # List of governors
    - alias: governor-name # Human-readable identifier
      publicKey: | # Governor's PGP public key
        -----BEGIN PGP PUBLIC KEY BLOCK-----
        ...
        -----END PGP PUBLIC KEY BLOCK-----
```

#### Approval Rules

The `require` field defines the quorum requirements using a tree structure:

**All governors must approve:**
```yaml
require:
  all: true
  require:
    - signer: owner
    - signer: voter1
    - signer: voter2
```

**At least N governors must approve:**
```yaml
require:
  atLeast: 2
  require:
    - signer: owner
    - signer: voter1
    - signer: voter2
```

**Nested rules (e.g., owner OR 2 out of 3 voters):**
```yaml
require:
  all: true
  require:
    - signer: owner
    - atLeast: 2
      require:
        - signer: voter1
        - signer: voter2
        - signer: voter3
```

**Note**: The `signer` field must reference a governor's `alias` from the `governors.members` list.

### Application flags

The behavior of the Qubmango operator can be customized using the following flags:

- `repositories-base-path` - Base path for cloning repositories within the pod (default: `/tmp/qubmango/git/repos`)
- `known-hosts-path` - Path to the `known_hosts` file for SSH authentication within the pod* (default: `/etc/ssh/ssh_known_hosts`)

## Common Issues

**Issue**: Webhook fails with certificate errors
- **Solution**: Ensure Cert-Manager is installed and running, or manually provision valid certificates

**Issue**: Git authentication fails
- **Solution**: Verify SSH secret contains valid private key in field and has been added as a deploy key to the repository

**Issue**: Repository domain is not in known_hosts
- **Solution**: The Qubmango by default supports `GitHub`, `GitLab` and `Bitbucket`. If you are using other Git provider, you need to add its domain to the `known_hosts` file specified by `known-hosts-path` flag.

## Development

### Run controller locally (without building and pushing Docker image)

To run Qubmango locally for development, you should:

1. Generate the CRDs and other resources:

```bash
make generate
make manifests
```

2. Install the artifacts into the cluster:

```bash
make install
```

3. Deploy the local configuration (this will create local configuration using Kustomize and deploy the controller to the cluster):

```bash
make deploy-local
```

4. Start the local controller (this will provision webhook-certs to the cert-manager; generate the local SA token and run the manager):

```bash
make run
```

5. If you want to remove the local configuration:

```bash
make undeploy-local
make uninstall
```

### Run controller locally (with building and pushing Docker image)

1. Generate the CRDs and other resources:

```bash
make generate
make manifests
```

2. Build new version of Docker image and push it to the registry specified by `IMG`:

```bash
make docker-build docker-push IMG=<some-registry>/qubmango-controller:tag
```

3. Install new CRDs into the cluster

```bash
make install
```

4. Deploy the Manager to the cluster with the image specified by `IMG`:

```bash
make deploy-local IMG=<some-registry>/qubmango-controller:tag
```
5. If you want to remove the local configuration:

```bash
make undeploy-local
make uninstall
```

### Distribution

1. Build new version of Docker image and push it to the registry specified by `IMG`:

```bash
make docker-build docker-push IMG=<some-registry>/qubmango-controller:tag
```

> Note:
> This image ought to be published in the personal registry you specified.
> And it is required to have access to pull the image from the working environment.
> Make sure you have the proper permission to the registry if the above commands don't work.

2. Build the installer for the image built and published in the registry:

```bash
make build-installer IMG=<some-registry>/qubmango-controller:tag
```

> Note:
> The makefile target mentioned above generates an 'install.yaml'
> file in the dist directory. This file contains all the resources built
> with Kustomize, which are necessary to install this project without its
> dependencies.

3. Using the installer

Users can just run 'kubectl apply -f <URL for YAML BUNDLE>' to install the project, i.e.:

```bash
kubectl apply -f https://raw.githubusercontent.com/<org>/qubmango-controller/<tag or branch>/dist/install.yaml
```