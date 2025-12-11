#!/usr/bin/env bash

if [[ -n $(git status --porcelain) ]]; then
    echo "Error: local repository has uncommitted changes. Please commit or stash them first."
    exit 1
fi


# Pull changes to avoid conflicts
git pull origin main


# Make a dummy change to the config.yaml to simulate a developer change
TIMESTAMP=$(date +%s)
sed -i'' -e "s/version: .*/version: v${TIMESTAMP}/" app-manifests/config.yaml

git add .
git commit -m "Dummy change ${TIMESTAMP}"
git push origin main
echo "Pushed new change to GitHub. Argo CD will now sync."