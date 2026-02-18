# Qubmango CLI

Qubmango CLI is a command-line tool that allows governors to interact with the Qubmango governance system. It provides commands for reviewing and approving Manifest Signing Requests (MSRs), viewing the history of Manifest Change Approvals (MCAs), and managing the CLI configuration.

## Prerequisites

### Secrets

#### Personal PGP keys

CLI requires a PGP private key to sign MSRs and git commits. The public part of the key must be in the MRT's `spec.governors.members` list, otherwise signature will be ignored by the Qubmango. This keys should be generated and stored locally by the user in a file in a secure location. The path to this file will be provided during the CLI configuration.

#### Personal SSH keys

CLI requires an SSH private key to connect to the Git repository. The key must be added to deployment keys of the target Git repository and have `read` and `write` access there. This key should be generated and stored locally by the user in a file in a secure location. The path to this file will be provided during the CLI configuration.

#### MRT PGP public key

CLI requires the PGP public key of the Manifest Request Template (MRT) to verify the signatures of MSRs (e.g. `spec.pgp.publicKey`). This key should be provided by the repository administrators and stored locally by the user in a file. The path to this file will be provided during the CLI configuration.

### Other prerequisites

User also must know:
- the SSH URL of the Git repository to be governed
- the path to the governance folder in the Git repository
- the names of the MSR and MCA resources referenced in the MRT

## Setup

1. Install the CLI by running:

```bash
curl -L https://github.com/AlwaysSayNo/quorum-based-manifests-governance/releases/latest/download/qubmango -o qubmango
chmod +x qubmango
sudo mv qubmango /usr/local/bin/
```

> Note: or build from source by cloning the repository and running `go build -o qubmango ./cli` in the root directory of the project and running:
> ```bash
> go mod tidy
> go build -o qubmango
> ```

2. Configure the CLI by running:

```bash
qubmango config add-repo <alias> \
    <url> \
    <ssh-key-path> \
    <pgp-key-path> \
    <governance-key-path> \
    <governance-folder-path> \
    <msr-name> \
    <mca-name>
```

Description of the parameters:
- `alias`: An alias for the repository configuration, used to reference this repository in other CLI commands.
- `url`: The SSH URL of the Git repository to be governed.
- `ssh-key-path`: The local file path to the SSH private key used for connecting to the Git repository.
- `pgp-key-path`: The local file path to the PGP private key used for signing MSRs and git commits.
- `governance-key-path`: The local file path to the PGP public key of the Manifest Request Template (MRT).
- `governance-folder-path`: The path to the Qubmango governance folder within the Git repository.
- `msr-name`: The name of the Manifest Signing Request (MSR) resource referenced in the MRT.
- `mca-name`: The name of the Manifest Change Approval (MCA) resource referenced in the MRT.

If 
- the MRT is stored in the GitHub repository with name `git-repository` in organization `org` 
- the MRT references MSR and MCA with names `msr-name` and `mca-name` respectively
- the governance folder is at path `/governance/.qubmango`
- pgp and ssh keys are stored locally at paths `/keys/pgp-private.key` and `/keys/ssh-private.key` respectively
- and user downloaded and saved the MRT public PGP key at path `/keys/mrt-pgp-public.key`

the CLI command will look like this:

```bash
qubmango config add-repo git-repo \
    git@github.com:org/git-repository.git \
    /keys/pgp-private.key \
    /keys/ssh-private.key \
    /keys/mrt-pgp-public.key \
    /governance/.qubmango \
    msr-name \
    mca-name
```

3. Configure the CLI user information by running:

```bash
qubmango config set-user <user-name> <user-email>
```

For example:

```bash
qubmango config set-user "Nazar Grynko" "nazar.grynko@qubmango.com"
```

## CLI commands

### Help

To view the list of available commands and their descriptions, run:

```bash
qubmango --help
```

Or to view the help for a specific command, run:

```bash
qubmango <command> --help
```

### Review Manifest Signing Requests (MSR)

To review pending MSR, run the following command:

```bash
qubmango get msr --repo <repo-alias>
```

This command will display the details of the latest MSR for the specified repository alias. It will include:

- if the MSR is trusted (i.e. MSR signature is valid for the MRT public key)
- if changed files are trusted (i.e. the amount of files and their information in MSR matches the information in the Git repository)
- current MSR metadata
- list of changed files and their details
- current state of the MSR and the tree of approvals

### Review changed files

To review the changed files in the `git-diff` format, run the following command:

```bash
qubmango get file-diff --repo <repo-alias>
```

It will display the list of changed files in the latest MSR for the specified repository alias in the `git-diff` format.

### Approve the MSR

To approve the MSR, run the following command:

```bash
qubmango sign msr <msr-version> --repo <repo-alias>
```

It will sign the MSR with the PGP private key specified in the repository configuration for the given repository alias and push the signature to the Git repository. The `<msr-version>` parameter is used to specify which version of the MSR to sign. Only the latest MSR version can be approved. This is just a safety measure to prevent approving outdated MSRs.

### Review Manifest Change Approvals (MCA) history

To review the history of MCAs, run the following command:

```bash
qubmango history mca --repo <repo-alias>
```

This command will display list of all MCAs saved in the Git repository.

### Config CLI

#### Show current configuration

To view the current CLI configuration, run:

```bash
qubmango config show
```

This displays all configured repositories, user information, and the currently active repository.

#### Edit repository configuration

To modify an existing repository configuration, use:

```bash
qubmango config edit-repo <alias> [flags]
```

Available flags:
- `--url`: SSH URL of the repository
- `--ssh-key-path`: Absolute path to the SSH private key
- `--pgp-key-path`: Absolute path to the PGP private key
- `--governance-key-path`: Absolute path to the MRT's PGP public key
- `--governance-folder-path`: Path to the governance folder in the repository
- `--msr-name`: Name of the MSR resource
- `--mca-name`: Name of the MCA resource

Example - updating only the SSH key path:

```bash
qubmango config edit-repo git-repo --ssh-key-path /new/path/to/ssh-key
```

#### Remove repository configuration

To remove a repository configuration, run:

```bash
qubmango config remove-repo <alias>
```

This will delete the repository configuration from the CLI.

#### Set default repository

To set a default repository (so you don't need to specify `--alias` in every command), run:

```bash
qubmango config use-repo <alias>
```

To unset the current repository:

```bash
qubmango config use-repo ""
```

#### Change known_hosts path

By default, the CLI uses `/root/.ssh/known_hosts` as the path to the `known_hosts` file for verifying SSH host keys when connecting to Git repositories. To change this path, run:

```bash
qubmango config set-known-hosts <path-to-known-hosts>
```

## Common Issues

**Issue**: Fails to connect to the Git repository with an error related to SSH authentication or host key verification.
- **Solution**: Make sure that the SSH private key specified in the repository configuration is correct and has the necessary permissions to access the Git repository. Also, ensure that the host key of the Git server is added to the `known_hosts` file at the path specified in the CLI configuration.

**Issue**: MSR signatures are not recognized as valid.
- **Solution**: Ensure that the cloned public PGP key of the MRT is correct and matches the one specified in the MRT `spec.pgp.publicKey` (e.g. doesn't have extra spaces or newlines).