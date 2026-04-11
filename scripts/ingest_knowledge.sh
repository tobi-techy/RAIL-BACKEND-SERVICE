#!/bin/bash
# Ingest all knowledge documents into the RAG knowledge base.
#
# Usage:
#   ./scripts/ingest_knowledge.sh <API_BASE_URL> <INTERNAL_API_KEY>
#
# Example:
#   ./scripts/ingest_knowledge.sh https://api.userail.money "$INTERNAL_API_KEY"

set -e

API_BASE="${1:?Usage: $0 <API_BASE_URL> <INTERNAL_API_KEY>}"
TOKEN="${2:?Usage: $0 <API_BASE_URL> <INTERNAL_API_KEY>}"
KNOWLEDGE_DIR="$(dirname "$0")/../knowledge"

if [ ! -d "$KNOWLEDGE_DIR" ]; then
  echo "Error: knowledge directory not found at $KNOWLEDGE_DIR"
  exit 1
fi

echo "Ingesting knowledge documents from $KNOWLEDGE_DIR"
echo "API: $API_BASE/internal/knowledge/ingest"
echo ""

for file in "$KNOWLEDGE_DIR"/*.txt; do
  source_name=$(basename "$file" .txt)
  echo "→ Ingesting: $source_name ($(wc -c < "$file" | tr -d ' ') bytes)"

  response=$(curl -s -w "\n%{http_code}" \
    -X POST "$API_BASE/internal/knowledge/ingest" \
    -H "Authorization: Bearer $TOKEN" \
    -F "source=$source_name" \
    -F "file=@$file")

  http_code=$(echo "$response" | tail -1)
  body=$(echo "$response" | sed '$d')

  if [ "$http_code" = "200" ]; then
    chunks=$(echo "$body" | grep -o '"chunks":[0-9]*' | cut -d: -f2)
    echo "  ✓ Success — $chunks chunks"
  else
    echo "  ✗ Failed (HTTP $http_code): $body"
  fi
done

echo ""
echo "Done."
