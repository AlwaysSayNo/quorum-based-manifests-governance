#!/bin/bash
set -e

# Directory to store certs
CERT_DIR="/tmp/k8s-webhook-server/serving-certs"
mkdir -p ${CERT_DIR}

SERVICE_NAME="webhook-service"
NAMESPACE="controller-system"

echo "Generating local self-signed certificates..."

# 1. Create a config file for OpenSSL to define the SANs
# It includes host.minikube.internal for Minikube access
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
openssl genrsa -out ${CERT_DIR}/ca.key 2048
openssl req -x509 -new -nodes -key ${CERT_DIR}/ca.key -subj "/CN=LocalDevCA" -days 365 -out ${CERT_DIR}/ca.crt

# 3. Generate Server Key and CSR
openssl genrsa -out ${CERT_DIR}/tls.key 2048
openssl req -new -key ${CERT_DIR}/tls.key -out ${CERT_DIR}/server.csr -subj "/CN=${SERVICE_NAME}.${NAMESPACE}.svc" -config ${CERT_DIR}/csr.conf

# 4. Sign the CSR with the CA
openssl x509 -req -in ${CERT_DIR}/server.csr -CA ${CERT_DIR}/ca.crt -CAkey ${CERT_DIR}/ca.key -CAcreateserial -out ${CERT_DIR}/tls.crt -days 365 -extensions v3_req -extfile ${CERT_DIR}/csr.conf

echo "Certificates generated at ${CERT_DIR}"

# 5. Patch the Webhook Configurations in the cluster with the new CA Bundle
CA_BUNDLE=$(cat ${CERT_DIR}/ca.crt | base64 | tr -d '\n')

# Find the name of the validating webhook. Assume there is only one relevant to this project.
VALIDATING_CONF=$(kubectl get validatingwebhookconfigurations -o name | grep "validating-webhook-configuration" | head -n 1)

if [ -n "$VALIDATING_CONF" ]; then
    echo "Patching $VALIDATING_CONF with new CA Bundle..."
    
    # Loop through indices 0 to 9 (arbitrary limit) and try to patch. 
    # Kubernetes will ignore patches for indices that don't exist or return error (which we suppress).
    for i in {0..5}; do
        kubectl patch $VALIDATING_CONF --type='json' -p="[{\"op\": \"replace\", \"path\": \"/webhooks/$i/clientConfig/caBundle\", \"value\": \"$CA_BUNDLE\"}]" 2>/dev/null || true
    done
    echo "ValidatingWebhookConfiguration patched."
else
    echo "Warning: No ValidatingWebhookConfiguration found to patch."
fi

# Find and patch MutatingWebhookConfiguration
MUTATING_CONF=$(kubectl get mutatingwebhookconfigurations -o name | grep "mutating-webhook-configuration" | head -n 1)

if [ -n "$MUTATING_CONF" ]; then
    echo "Patching $MUTATING_CONF with new CA Bundle..."
    for i in {0..5}; do
        kubectl patch $MUTATING_CONF --type='json' -p="[{\"op\": \"replace\", \"path\": \"/webhooks/$i/clientConfig/caBundle\", \"value\": \"$CA_BUNDLE\"}]" 2>/dev/null || true
    done
    echo "MutatingWebhookConfiguration patched."
else
    echo "Warning: No MutatingWebhookConfiguration found to patch."
fi