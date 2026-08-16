#!/bin/bash
#
# AtlasFlow deployment automation for Rail.
#
# Creates all AtlasFlow projects, configures runtime tiers, and triggers
# the first deployment. Run once after installing the CLI and connecting GitHub.
#
# Usage:
#   REPO=tobi-techy/RAIL-BACKEND-SERVICE ./deploy.sh
#
# Prerequisites:
#   1. atlasflow CLI installed: curl -fsSL https://atlasflow.com/install.sh | sh
#   2. atlasflow login --key af_live_...
#   3. atlasflow github connect (install the GitHub App on your repo)
#   4. Set environment variables in the AtlasFlow dashboard BEFORE pushing
#
set -euo pipefail

REPO="${REPO:-}"
if [ -z "$REPO" ]; then
  echo "ERROR: Set REPO env var, e.g. REPO=your-org/RAIL_BACKEND ./deploy.sh"
  exit 1
fi

# Check atlasflow CLI is installed
if ! command -v atlasflow &>/dev/null; then
  echo "ERROR: atlasflow CLI not found. Install: curl -fsSL https://atlasflow.com/install.sh | sh"
  exit 1
fi

echo "=== AtlasFlow Deployment for Rail ==="
echo "Repo: $REPO"
echo ""

# ─── Create Projects ──────────────────────────────────────────────────────────

create_project() {
  local name="$1"
  local root="$2"
  local dockerfile="$3"

  echo "Creating project: $name (root=$root, dockerfile=$dockerfile)"
  if atlasflow projects create \
    --name "$name" \
    --repo "$REPO" \
    --root "$root" \
    --dockerfile "$dockerfile" 2>&1; then
    echo "  ✓ $name created"
  else
    echo "  ! $name may already exist (continuing)"
  fi
}

# Go API is already live as rail-backend-service — do not create rail-api.
create_project "spectrum-bridge" "/cmd/spectrum-bridge" "Dockerfile"
create_project "rail-enrichment" "/services/enrichment" "Dockerfile"
create_project "rail-ocr" "/services/ocr" "Dockerfile"

echo ""

# ─── Configure Runtime Tiers ──────────────────────────────────────────────────

set_tier() {
  local project="$1"
  local tier="$2"

  echo "Setting $project → $tier tier"
  atlasflow projects environments update "$project" production --runtime-tier "$tier" 2>&1 || true
}

set_tier "rail-backend-service" "medium"  # 2 vCPU / 4 GB — main API + Miriam
set_tier "spectrum-bridge" "small"        # 1 vCPU / 2 GB — message bridge
set_tier "rail-enrichment" "small"        # 1 vCPU / 2 GB — ML inference
set_tier "rail-ocr" "large"               # 4 vCPU / 8 GB — PaddleOCR needs RAM

echo ""

# ─── Set Min Replicas for Production ──────────────────────────────────────────

set_replicas() {
  local project="$1"
  local replicas="$2"

  echo "Setting $project → $replicas min replicas"
  atlasflow projects environments update "$project" production --min-replicas "$replicas" 2>&1 || true
}

set_replicas "rail-backend-service" 2  # 2 replicas for zero-downtime
set_replicas "spectrum-bridge" 1       # 1 replica (stateful Spectrum iterator)
set_replicas "rail-enrichment" 1       # 1 replica (stateless)
set_replicas "rail-ocr" 1              # 1 replica (stateless, heavy)

echo ""

# ─── Summary ──────────────────────────────────────────────────────────────────

echo "=== Projects Created ==="
echo ""
echo "  rail-backend-service  → https://api.userail.money"
echo "  spectrum-bridge       → https://spectrum-bridge-tobi-omotade-2cd167ac.atlasflow.dev"
echo "  rail-enrichment       → https://rail-enrichment-tobi-omotade-2cd167ac.atlasflow.dev"
echo "  rail-ocr              → https://rail-ocr-tobi-omotade-2cd167ac.atlasflow.dev"
echo ""
echo "=== Next Steps ==="
echo ""
echo "  1. Set environment variables in the AtlasFlow dashboard"
echo "     See: deployments/atlasflow/env-vars-reference.md"
echo ""
echo "  2. Trigger first deployments:"
echo "     # Set spectrum-bridge secrets FIRST, then:"
echo "     atlasflow deployments create --project rail-enrichment"
echo "     atlasflow deployments create --project rail-ocr"
echo "     atlasflow deployments create --project spectrum-bridge"
echo ""
echo "  3. Point rail-backend-service at the new sidecars:"
echo "     ENRICHMENT_SERVICE_URL, DOCUMENT_OCR_SERVICE_URL, PLATFORM_BRIDGE_BASE_URL"
echo ""
echo "  4. Verify health:"
echo "     curl https://api.userail.money/health"
echo ""
