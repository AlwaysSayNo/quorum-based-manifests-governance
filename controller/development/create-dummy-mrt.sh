#!/usr/bin/env bash

if [[ -n $(git status --porcelain) ]]; then
    echo "Error: local repository has uncommitted changes. Please commit or stash them first."
    exit 1
fi

# Pull changes to avoid conflicts
git pull origin main

# Make a dummy change to create MRT
echo "apiVersion: governance.nazar.grynko.com/v1alpha1
kind: ManifestRequestTemplate

metadata:
  name: mrt-sample
  namespace: test-app

spec:
  version: 1
  publicKey: |
    -----

  argoCDApplication:
    name: test-app
    namespace: argocd

  location:
    folder: manifest-signing-requests

  msr:
    name: msr-sample
    namespace: test-app
  
  mca:
    name: mca-sample
    namespace: test-app

  governors:
    notificationChannels:
      - slack:
          groupID: C87654321
    members:
      - alias: voter1
        publicKey: |
          -----
        notificationChannel:
          slack:
            userGroupID: C12345678
      - alias: voter2
        publicKey: |
          -----

  require:
    all: true
    require:
      - signer: $owner
      - atLeast: 2
        require:
          - signer: $voter1
          - signer: $voter2" > app-manifests/mrt.yaml

git add .
git commit -m "Create dummy MRT"
git push origin main
echo "Pushed new change to GitHub. Argo CD will now sync."