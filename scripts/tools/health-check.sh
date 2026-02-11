#!/bin/bash
set -e

ENDPOINT="${1:-http://localhost:8080}"
MAX_RETRIES="${2:-30}"
RETRY_INTERVAL="${3:-2}"

echo "Checking health of $ENDPOINT..."

for i in $(seq 1 $MAX_RETRIES); do
    if curl -sf "$ENDPOINT/health" > /dev/null; then
        echo "Service is healthy!"
        exit 0
    fi
    echo "Attempt $i/$MAX_RETRIES failed, retrying in ${RETRY_INTERVAL}s..."
    sleep $RETRY_INTERVAL
done

echo "Service failed to become healthy after $MAX_RETRIES attempts"
exit 1
