#!/bin/bash

set -x
set -e
set -u

# Write secret to a file to prevent printing secret in travis logs.
( set +x; echo -n "${GITHUB_WEBHOOK_SECRET}" > /tmp/gmx-webhook-secret )

# Create a k8s secret for the GitHub webhook shared secret.
kubectl create secret generic gmx-webhook-secret \
  --from-file=/tmp/gmx-webhook-secret \
  --dry-run -o json | kubectl apply -f -
