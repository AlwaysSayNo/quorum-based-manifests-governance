# Setup

## Requirements

### Software

Ensure, that you have on your computer next software installed with the following minimum versions:

- **Go** `v1.25.5` (https://golang.org/dl/)
- **Docker** `v29.1.3` (https://docs.docker.com/get-docker/)
- **Kubectl** `v1.35.0` (https://kubernetes.io/docs/tasks/tools/)
- **Minikube** `v1.37.0` (https://minikube.sigs.k8s.io/docs/start/)
- **Git** `v2.47.3` (https://git-scm.com/downloads)

You can find the installation instructions for these tools in their official documentation.

Also, for proceeding the next steps, you need to install **go-task** tool `v3.46.3` for executing the tasks defined in the Taskfile and **yq** tool `v4.52.2` for processing YAML files. You can install it by running the following commands:

```bash
go install github.com/go-task/task/v3/cmd/task@latest
go install github.com/mikefarah/yq/v4@latest
task --version
yq --version
```

### Secrets

You need have 5 PGP and 5 SSH keys for the governance process, along with a Slack App and its token. The requirements for these secrets are described below:

#### PGP Keys

You need to create 5 PGP keys using the following values (one for operator, three one for owner, two for trusted voters (Voter1, Voter2) and one for untrusted voter (Voter3)):

| Actor | Type | Expiration | Bytes | Name | Email | Passphrase |
|-------|------|------------|-------|------|-------|------------|
| Qubmango | RSA | Never | 2048 | Qubmango Governance Operator | noreply@qubmango.com | pgp-passphrase |
| Owner | RSA | Never | 2048 | Owner Person | owner@qubmango.com | owner-pgp-passphrase |
| Voter1 | RSA | Never | 2048 | Voter1 Person | voter1@qubmango.com | voter1-pgp-passphrase |
| Voter2 | RSA | Never | 2048 | Voter2 Person | voter2@qubmango.com | voter2-pgp-passphrase |
| Voter3 | RSA | Never | 2048 | Voter3 Person | voter3@qubmango.com | voter3-pgp-passphrase |

#### SSH Keys

You need to create 5 SSH keys using the following values (one for operator, three one for owner, two for trusted voters (Voter1, Voter2) and one for untrusted voter (Voter3)):

| Actor | Type | Expiration | Bytes | Passphrase |
|-------|------|------------|-------|------------|
| Qubmango | ED25519 | Never | 256 | ssh-passphrase | 
| Owner | ED25519 | Never | 256 | owner-ssh-passphrase |
| Voter1 | ED25519 | Never | 256 | voter1-ssh-passphrase |
| Voter2 | ED25519 | Never | 256 | voter2-ssh-passphrase |
| Voter3 | ED25519 | Never | 256 | voter3-ssh-passphrase |

For these SSH keys you should also add the public part of the key as a Deploy Key to your GitHub repository with `read` and `write` permissions. You can follow the instructions [here](https://docs.github.com/en/authentication/connecting-to-github-with-ssh/managing-deploy-keys#set-up-deploy-keys) in '*Set up deploy keys*' section.

#### Slack App

For this step you should have a Slack workspace, where you have permissions to create a Slack App. You can follow the instructions [here](https://docs.slack.dev/app-management/quickstart-app-settings/#creating) in '*1. Creating an app*' section. You should choose:

- **From scratch**
- **App Name**: Qubmango Governance Bot
- **Workspace**: *your workspace*
- **Create App**

Following the '*2. Requesting scopes*' section you should should choose only `chat:write` scope from 'Bot Token Scopes' and install the app to the workspace by following the '*3. Installing your app to your workspace*' section. In the end, you should have the `Bot User OAuth Token`, which you will use as a secret in the governance process. You can find it in 'OAuth & Permissions' section of your Slack App settings. Save this token.

### Repository

#### Create GitHub Repository

Create an empty repository on your GitHub account and pull it to your local computer. You can follow the instructions [here](https://docs.github.com/en/get-started/quickstart/create-a-repo) in '*Create a repository*' section. The repository should have *Pull Request* functionality, which should not be enforced. Open the root folder of the cloned repository in your terminal and run the following command to add the remote of this repository:

```bash
git remote add upstream
```

#### Prepare Repository Files

Copy the `Taskfile.yaml` file and folders `app-manifests` and `secrets` from the `kubernetes/examples/0. Setup/` location to the root folder of your cloned repository. Don't commit these files yet.

Create a `secrets/template` folder, in which you will save the manifests of your secrets. You can create it by running the following command:

```bash
mkdir -p secrets/template
```

#### Provide the required secrets

In the `secrets` folder there are a number of empty files. You should fill these files with the content of the secrets you created. The correspondence between the secret files and the created secrets is described in the table below:

| Actor | Secret Type | Secret File |
|-------|-------------|-------------|
| Qubmango | PGP Key | pgp-private-key.asc |
| Qubmango | PGP Key | pgp-public-key.asc |
| Qubmango | SSH Key | ssh-private-key |
| Qubmango | Slack Token | slack-token.txt |
| Owner | PGP Key | owner-pgp-public-key.asc |
| Voter1 | PGP Key | voter1-pgp-public-key.asc |
| Voter2 | PGP Key | voter2-pgp-public-key.asc |

#### Taskfile.yaml variables

`Taskfile.yaml` file has some predefined environment variables, but you should provide the following ones:

- **HTTP_REPO_URL** - the HTTPS URL of your GitHub repository.
- **SSH_REPO_URL** - the SSH URL of your GitHub repository.
- **REPO_NAME** - the name of your GitHub repository.
- **SLACK_CHANNEL_ID** - the ID of the Slack channel, where the governance notifications will be sent.

#### Project structure

The end structure of your repository should look like this:

```
git-repository-root:
├── Taskfile.yaml
├── app-manifests:
│   ├── app.yaml
│   └── mrt.yaml
└── secrets:
    ├── tempaltes:
    ├── owner-pgp-public-key.asc
    ├── pgp-private-key.asc
    ├── pgp-public-key.asc
    ├── slack-token.txt
    ├── ssh-private-key
    ├── voter1-pgp-public-key.asc
    └── voter2-pgp-public-key.asc
```

## Executing the Setup

Start executing the next commands from the root folder of your cloned repository, where `Taskfile.yaml` is located. 

### 1. Setup Minikube

1. Start Minikube (if you don't have sufficient resources, you can adjust the CPU and memory flags):

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

### 2. Install Cert-Manager

1. Install the Cert-Manager CRDs and other resources:

```bash
task 2-cert-manager:1-install
```

2. Verify that the Cert-Manager pods are running:

```bash
task 2-cert-manager:2-verify
```

### 3. Install Argo CD

1. Install Argo CD CRDs and other resources:

```bash
task 3-argo-cd:1-install
```

2. Verify that the Argo CD pods are running:

```bash
task 3-argo-cd:2-verify
```

### 4. Setup manifests

In this step, we will setup the secrets and required manifests in the repository with information provided during the setup.

1. Setup the Slack secret:

```bash
task 4-setup-manifests:1-slack
```

2. Setup the Qubmango PGP secret:

```bash
task 4-setup-manifests:2-pgp
```

3. Setup the Qubmango SSH secret:

```bash
task 4-setup-manifests:3-ssh
```

4. Setup the Argo CD Application manifest:

```bash
task 4-setup-manifests:4-application
```

5. Setup the Manifest Request Template manifest:

```bash
task 4-setup-manifests:5-mrt
```

6. Setup the other manifest, which will be governed, namely a simple deployment with nginx with its configmap and namespace:

```bash
task 4-setup-manifests:6-namespace
task 4-setup-manifests:7-configmap
task 4-setup-manifests:8-deployment
```

### 5. Apply secrets to the cluster

Apply the created secrets to the Minikube cluster:

```bash
task 5-secrets-apply:1-slack
task 5-secrets-apply:2-create-pgp
task 5-secrets-apply:3-create-ssh
```

### 6. Commit changes to the repository

Delete the secrets folder along with the `Taskfile.yaml`, as it is no longer needed:

```bash
rm -rf secrets
rm Taskfile.yaml
```

Finally, commit all the created files to your GitHub repository, except the mrt.yaml file, which will be used in the next steps:

```bash
git add -- ':!app-manifests/mrt.yaml'
git commit -m "Setup governance repository"
git push origin main
```

### 7. Apply the Argo CD Application manifest

Apply the Argo CD Application manifest to the cluster, which will create the Application in Argo CD and trigger the synchronization.

```bash
task 6-app-manifests-apply:1-application
```

## CLI setup

Every governor (Owner, Voter1, Voter2 and Voter3) should have the `qubmango` CLI tool installed and configured on their local machine. You can find the installation instruction and setup instruction in the [CLI README](../../cli/README.md) file. They should provide the same repository URL, which you used in the `Taskfile.yaml` file, and the corresponding own PGP, SSH private and operator PGP public key paths for authentication. E.g. after download they would execute the following command:

```bash
qubmango config add-repo \
    repo-name \ # The name of the repository, e.g. "governance-repo"
    repo-ssh-url \ # The SSH URL of the repository, e.g. "git@github.com:organization/governance-repo.git"
    own-ssh-private-key-path \ # The path to the own SSH private key, e.g. "/home/user/.ssh/id_ed25519"
    own-pgp-private-key-path \ # The path to the own PGP private key, e.g. "/home/user/.pgp/owner-pgp-private-key.asc"
    operator-pgp-public-key-path \ # The path to the operator PGP public key, e.g. "/home/user/.pgp/qubmango-pgp-public-key.asc"
    in-repo-governance-folder-path # The path to the governance folder in the repository, e.g. "app-manifests/.qubmango"
```

## Final Notes

After the setup is completed, you should have a basics for the governance process: Argo CD, Cert-Manager are installed in the repository, along with the required manifests and secrets. So far, cluster is not provisioned with the Qubmango application.

The final structure of your repository should look like this:

```
git-repository-root:
└── app-manifests:
    ├── app.yaml
    ├── configmap.yaml
    ├── mrt.yaml
    ├── namespace.yaml
    └── nginx-deploy.yaml
```

You can find the example of the final repository at `kubernetes/examples/0. Setup (End result)/` folder along with the used secrets (all of them are just examples without real business value).