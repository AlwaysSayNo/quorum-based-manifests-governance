#!/usr/bin/env bash

set -x

# ensure apt cache and python tooling are present (run as root inside devcontainer)
apt-get update
apt-get install -y --no-install-recommends python3 python3-venv python3-pip ca-certificates apt-transport-https gnupg

# install gcloud sdk
curl -fsSL https://packages.cloud.google.com/apt/doc/apt-key.gpg | gpg --dearmor -o /usr/share/keyrings/cloud.google.gpg
echo "deb [signed-by=/usr/share/keyrings/cloud.google.gpg] http://packages.cloud.google.com/apt cloud-sdk main" > /etc/apt/sources.list.d/google-cloud-sdk.list
apt-get update
apt-get install -y google-cloud-sdk

# download additional tools for gke auth
sudo apt-get install google-cloud-cli-gke-gcloud-auth-plugin

# install kind
curl -Lo /usr/local/bin/kind https://kind.sigs.k8s.io/dl/latest/kind-linux-amd64
chmod +x /usr/local/bin/kind

# install kubebuilder
curl -L -o kubebuilder https://go.kubebuilder.io/dl/latest/linux/amd64
chmod +x kubebuilder
mv kubebuilder /usr/local/bin/

# install kubectl
KUBECTL_VERSION=$(curl -L -s https://dl.k8s.io/release/stable.txt)
curl -LO "https://dl.k8s.io/release/$KUBECTL_VERSION/bin/linux/amd64/kubectl"
chmod +x kubectl
mv kubectl /usr/local/bin/kubectl

# create docker network for kind clusters to use
docker network create -d=bridge --subnet=192.168.1.0/24 kind | true

# install go
go install github.com/go-task/task/v3/cmd/task@latest

# install argocd cli
curl -sSL -o argocd-linux-amd64 https://github.com/argoproj/argo-cd/releases/latest/download/argocd-linux-amd64
install -m 555 argocd-linux-amd64 /usr/local/bin/argocd
rm argocd-linux-amd64

# verify installations
python3 --version
gcloud --version
gke-gcloud-auth-plugin --version
kind version
kubebuilder version
docker --version
go version
kubectl version --client
task --version
argocd version --client