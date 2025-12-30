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

# Move to the development directory
cd $(dirname "$0")
cd ..
mkdir -p repos
cd repos

# clone repo to development folder
git clone $repo_url 
cd $repo_name

# Create manifests directory
mkdir -p app-manifests

# Create an initial dummy namespace
echo "apiVersion: v1
kind: Namespace
metadata:
  name: test-ns
  labels:
    name: test-ns" > app-manifests/namespace.yaml

# Create an initial dummy manifest
echo "apiVersion: v1
kind: ConfigMap
metadata:
  name: config-my
  namespace: test-ns
data:
  version: v1" > app-manifests/config.yaml

# Create Argo CD Application manifest
echo "apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: application-my
  namespace: argocd
spec:
  project: default
  source:
    # The URL of new public GitHub repository
    repoURL: #{REPO_URL}#
    targetRevision: HEAD
    path: app-manifests
  destination:
    server: https://kubernetes.default.svc
    namespace: test-app
  syncPolicy:
    automated:
      prune: true
      selfHeal: true
  ignoreDifferences:
    - group: argoproj.io
      kind: Application
      name: application-my  # Matches its own name and namespace
      namespace: argocd
      jsonPointers:
      - /spec/source/targetRevision" > app-manifests/app.yaml

# Replace placeholder with actual repo URL
sed -i'' -e "s|#{REPO_URL}#|$repo_url|g" app-manifests/app.yaml

# Initial commit and push
git add .
git commit -m "Initial commit"
git push origin main
