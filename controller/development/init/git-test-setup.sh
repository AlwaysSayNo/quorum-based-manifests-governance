#!/usr/bin/env bash

repo_url=$1
repo_name=$2

# check if git_url is provided
if [ -z "$repo_url" ]; then
    echo "Usage: $0 <git-repo-url>"
    exit 1
elif [ -z "$repo_name" ]; then
    echo "Usage: $0 <git-repo-url> <git-repo-name>"
    exit 1
fi

# clone repo to development folder
cd $(dirname "$0")
cd ..
git clone $repo_url 
cd $repo_name

# Create manifests directory
mkdir -p app-manifests

# Create an initial dummy manifest
echo "apiVersion: v1
kind: ConfigMap
metadata:
  name: my-config
data:
  version: v1" > app-manifests/config.yaml

echo "apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: test-app
  namespace: argocd
spec:
  project: default
  source:
    # The URL of your new public GitHub repository
    repoURL: #{REPO_URL}#
    targetRevision: HEAD
    path: app-manifests
  destination:
    server: https://kubernetes.default.svc
    namespace: test-app
  syncPolicy:
    automated:
      prune: true
      selfHeal: true" > app-manifests/app.yaml

sed -i'' -e "s|#{REPO_URL}#|$repo_url|g" app-manifests/app.yaml

# Push init commit
git add .
git commit -m "Initial commit"
git push origin main
