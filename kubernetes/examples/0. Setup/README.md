## Requirements

### Software

Ensure, that you have on your computer (table):

- Go v1.25.5 (https://golang.org/dl/)
- Docker v29.1.3 (https://docs.docker.com/get-docker/)
- Kubectl v1.35.0 (https://kubernetes.io/docs/tasks/tools/)
- Minikube v1.37.0 (https://minikube.sigs.k8s.io/docs/start/)
- Git v2.47.3 (https://git-scm.com/downloads)

You can find the installation instructions for these tools in their official documentation.

Also, for proceeding the next steps, you need to install go-task tool v3.21.0. You can install it by running the following command:

```bash
go install github.com/go-task/task/v3/cmd/task@latest
task --version
```

### Repository

Create an empty repository on your GitHub account and pull it to your local computer. You can follow the instructions [here](https://docs.github.com/en/get-started/quickstart/create-a-repo). Open the root folder of the cloned repository in your terminal and run the following command to add the remote of this repository:

```bash
git remote add upstream
```

## Executing the Setup

### Setup Minikube

1. Start Minikube with the following command:

```bash
minikube start --driver=docker --memory=8192 --cpus=4 # Adjust resources as needed
minikube status
```

2. Set the Docker environment to use Minikube's Docker daemon:

```bash
eval $(minikube -p minikube docker-env)
```

### Install Cert-Manager

1. Install the Cert-Manager Custom Resource Definitions (CRDs):

```bash
kubectl apply -f https://github.com/cert-manager/cert-manager/releases/download/v1.14.4/cert-manager.yaml
kubectl wait --for=condition=established --all -n cert-manager --timeout=300s || true
```

2. Verify that the Cert-Manager pods are running:

```bash
kubectl get pods --namespace cert-manager
```

### Install Argo CD

1. Create a namespace for Argo CD:

```bash
kubectl create namespace argocd || true
```

2. Install Argo CD in the created namespace:

```bash
kubectl create -n argocd -f https://raw.githubusercontent.com/argoproj/argo-cd/stable/manifests/install.yaml
kubectl wait --for=condition=ready pod --all -n argocd --timeout=300s || true
```

3. Verify that the Argo CD pods are running:

```bash
kubectl get pods -n argocd
```