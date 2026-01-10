#!/usr/bin/env bash

SECRETS_DIR=".secrets"
SECRET_FILE="slack-secret.yaml"

if [[ -d "$PWD/$SECRETS_DIR" && -f "$PWD/$SECRETS_DIR/$SECRET_FILE" ]]; then
    echo "Applying Slack secret..."
    kubectl apply -f "$PWD/$SECRETS_DIR/$SECRET_FILE"
else
    echo "Missing secret."
    echo "Create folder '$PWD/$SECRETS_DIR' and put '$PWD/$SECRETS_DIR/$SECRET_FILE' inside it."
    exit 1
fi