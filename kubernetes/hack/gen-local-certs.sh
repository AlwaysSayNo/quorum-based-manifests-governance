#!/bin/bash
set -e

# Directory to store certs
CERT_DIR="/tmp/k8s-webhook-server/serving-certs"
mkdir -p ${CERT_DIR}

SERVICE_NAME="webhook-service"
NAMESPACE="controller-system"

echo "Generating local self-signed certificates"

# 1. Create a config file for OpenSSL to define the SANs
# Includes host.minikube.internal for Minikube access
cat > ${CERT_DIR}/csr.conf <<EOF
[req]
req_extensions = v3_req
distinguished_name = req_distinguished_name
[req_distinguished_name]
[ v3_req ]
basicConstraints = CA:FALSE
keyUsage = nonRepudiation, digitalSignature, keyEncipherment
extendedKeyUsage = serverAuth
subjectAltName = @alt_names
[alt_names]
DNS.1 = localhost
DNS.2 = host.minikube.internal
DNS.3 = ${SERVICE_NAME}
DNS.4 = ${SERVICE_NAME}.${NAMESPACE}
DNS.5 = ${SERVICE_NAME}.${NAMESPACE}.svc
IP.1 = 127.0.0.1
EOF

# 2. Generate CA
openssl genrsa -out ${CERT_DIR}/ca.key 2048 2>/dev/null
openssl req -x509 -new -nodes -key ${CERT_DIR}/ca.key -subj "/CN=LocalDevCA" -days 365 -out ${CERT_DIR}/ca.crt 2>/dev/null

# 3. Generate Server Key and CSR
openssl genrsa -out ${CERT_DIR}/tls.key 2048 2>/dev/null
openssl req -new -key ${CERT_DIR}/tls.key -out ${CERT_DIR}/server.csr -subj "/CN=${SERVICE_NAME}.${NAMESPACE}.svc" -config ${CERT_DIR}/csr.conf 2>/dev/null

# 4. Sign the CSR with the CA
openssl x509 -req -in ${CERT_DIR}/server.csr -CA ${CERT_DIR}/ca.crt -CAkey ${CERT_DIR}/ca.key -CAcreateserial -out ${CERT_DIR}/tls.crt -days 365 -extensions v3_req -extfile ${CERT_DIR}/csr.conf 2>/dev/null

echo "Certificates generated at ${CERT_DIR}"

# 5. Patch the Webhook Configurations in the cluster with the new CA Bundle
# This function takes a Kind (Validating/Mutating) and a name-grep pattern
patch_webhooks() {
    local KIND=$1
    local PATTERN=$2
    local CA_BUNDLE=$(cat ${CERT_DIR}/ca.crt | base64 | tr -d '\n')

    # Find the specific resource name
    local RESOURCE_NAME=$(kubectl get $KIND -o name | grep "$PATTERN" | head -n 1)

    if [ -z "$RESOURCE_NAME" ]; then
        return
    fi

    echo "- Patching $RESOURCE_NAME"
    
    # 1. Get the current JSON
    # 2. Use jq to iterate over all webhooks and set clientConfig.caBundle
    # 3. Apply the result back to the cluster
    kubectl get $RESOURCE_NAME -o json | \
    jq --arg ca "$CA_BUNDLE" '.webhooks[].clientConfig.caBundle = $ca' | \
    kubectl apply -f - >/dev/null

    echo "Success"
}

# 4. Execute Patching
echo "Patching cluster webhook configurations"
patch_webhooks "validatingwebhookconfigurations" "validating-webhook-configuration"
patch_webhooks "mutatingwebhookconfigurations" "mutating-webhook-configuration"