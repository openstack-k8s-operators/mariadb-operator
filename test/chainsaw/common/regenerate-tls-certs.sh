#!/bin/bash
set -e

echo "This script will regenerate the TLS certificates in tls-certificate.yaml"
echo "Prerequisites:"
echo "  - oc configured with an OpenShift cluster"
echo "  - cert-manager installed in the cluster"
echo "  - openstack namespace/project exists"
echo ""

# Extract the commented cert-manager resources
TEMP_FILE=$(mktemp)
sed -n '5,69s/^# //p' tls-certificate.yaml > "$TEMP_FILE"

echo "Extracted cert-manager resources to $TEMP_FILE"
echo ""
echo "Deleting any existing secrets..."
oc delete secret root-secret galera-cert -n openstack --ignore-not-found=true

echo ""
echo "Applying cert-manager resources..."

# Apply the resources
oc apply -f "$TEMP_FILE"

echo "Waiting for certificates to be ready..."
echo "  - Waiting for root-secret (CA certificate)..."
oc wait --for=condition=ready certificate/selfsigned-ca -n openstack --timeout=60s

echo "  - Waiting for galera-cert certificate..."
oc wait --for=condition=ready certificate/galera-cert -n openstack --timeout=60s

echo ""
echo "Certificates are ready! Extracting secret data..."

# Get the secret data
CA_CRT=$(oc get secret root-secret -n openstack -o jsonpath='{.data.ca\.crt}')
TLS_CRT=$(oc get secret galera-cert -n openstack -o jsonpath='{.data.tls\.crt}')
TLS_KEY=$(oc get secret galera-cert -n openstack -o jsonpath='{.data.tls\.key}')

echo ""
echo "Certificate validity periods:"
echo "  CA Certificate:"
echo "$CA_CRT" | base64 -d | openssl x509 -noout -dates | sed 's/^/    /'
echo ""
echo "  Galera Certificate:"
echo "$TLS_CRT" | base64 -d | openssl x509 -noout -dates | sed 's/^/    /'
echo ""

echo ""
echo "Creating new hardcoded secret..."
echo "---"
cat <<EOF
apiVersion: v1
kind: Secret
metadata:
  name: galera-cert
data:
  tls-ca-bundle.pem: $CA_CRT
  tls.crt: $TLS_CRT
  tls.key: $TLS_KEY # notsecret
EOF

echo ""
echo "---"
echo ""
echo "To update tls-certificate.yaml:"
echo "1. Copy the secret output above"
echo "2. Replace the existing Secret resource (lines 71-78) in tls-certificate.yaml"
echo ""
echo "Cleaning up cert-manager resources..."
oc delete -f "$TEMP_FILE" --ignore-not-found=true

rm "$TEMP_FILE"
echo "Done!"
