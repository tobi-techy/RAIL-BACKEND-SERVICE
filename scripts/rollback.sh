#!/bin/bash
set -e

NAMESPACE="${NAMESPACE:-production}"
RELEASE_NAME="${RELEASE_NAME:-rail-service}"

echo "Rolling back $RELEASE_NAME in namespace $NAMESPACE..."

# Get current revision
CURRENT_REVISION=$(helm list -n $NAMESPACE -o json | jq -r ".[] | select(.name==\"$RELEASE_NAME\") | .revision")
echo "Current revision: $CURRENT_REVISION"

# Rollback to previous revision
if [ "$CURRENT_REVISION" -gt 1 ]; then
    PREVIOUS_REVISION=$((CURRENT_REVISION - 1))
    echo "Rolling back to revision $PREVIOUS_REVISION..."
    helm rollback $RELEASE_NAME $PREVIOUS_REVISION -n $NAMESPACE --wait --timeout 5m
    echo "Rollback completed successfully"
else
    echo "Cannot rollback: already at first revision"
    exit 1
fi

# Verify rollback
echo "Verifying rollback..."
kubectl rollout status deployment/$RELEASE_NAME -n $NAMESPACE --timeout=5m

echo "Rollback verification completed"
