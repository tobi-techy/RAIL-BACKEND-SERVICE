#!/bin/bash
set -euo pipefail

# ═══════════════════════════════════════════════════════════════════════════════
# Rail Backend — Full Deploy to New AWS Account (885160773772)
# ═══════════════════════════════════════════════════════════════════════════════
# Prerequisites:
#   1. AWS CLI configured: aws configure --profile rail-new
#   2. Account verified and active
#   3. Run from repo root: ./scripts/deploy-new-account.sh
# ═══════════════════════════════════════════════════════════════════════════════

export AWS_PROFILE=rail-new
export AWS_REGION=us-east-1
ACCOUNT_ID=885160773772
ENV=prod
REPO_NAME=rail-backend
CLUSTER_NAME=rail-${ENV}
DB_PASSWORD="${TF_VAR_db_password:?Set TF_VAR_db_password before running}"

echo "═══ Rail Deploy — Account $ACCOUNT_ID ═══"
echo ""

# ── Step 1: Create Terraform state bucket ─────────────────────────────────────
echo "▸ Step 1: Creating Terraform state bucket..."
STATE_BUCKET="rail-terraform-state-${ACCOUNT_ID}"
if ! aws s3api head-bucket --bucket "$STATE_BUCKET" 2>/dev/null; then
  aws s3 mb "s3://${STATE_BUCKET}" --region "$AWS_REGION"
  aws s3api put-bucket-versioning \
    --bucket "$STATE_BUCKET" \
    --versioning-configuration Status=Enabled
  echo "  ✓ Created $STATE_BUCKET"
else
  echo "  ✓ Bucket already exists"
fi

# ── Step 2: Terraform init & apply ────────────────────────────────────────────
echo ""
echo "▸ Step 2: Applying Terraform infrastructure..."
cd infra/terraform

terraform init -reconfigure \
  -backend-config="bucket=${STATE_BUCKET}" \
  -backend-config="key=terraform.tfstate" \
  -backend-config="region=${AWS_REGION}"

terraform workspace select "$ENV" 2>/dev/null || terraform workspace new "$ENV"

terraform apply -var-file=production.tfvars -auto-approve

# Capture outputs
ALB_DNS=$(terraform output -raw alb_dns 2>/dev/null || echo "")
RDS_ENDPOINT=$(terraform output -raw rds_endpoint 2>/dev/null || echo "")
REDIS_ENDPOINT=$(terraform output -raw redis_endpoint 2>/dev/null || echo "")
ECR_URL=$(terraform output -raw ecr_url 2>/dev/null || echo "")

echo "  ✓ Infrastructure created"
echo "    ALB:   $ALB_DNS"
echo "    RDS:   $RDS_ENDPOINT"
echo "    Redis: $REDIS_ENDPOINT"
echo "    ECR:   $ECR_URL"

cd ../..

# ── Step 3: Store secrets in SSM ──────────────────────────────────────────────
echo ""
echo "▸ Step 3: Storing secrets in SSM Parameter Store..."
echo "  Run: ./infra/store-secrets-prod.sh"
echo "  (Edit the file first with your actual secret values)"
echo ""
read -p "  Press Enter after secrets are stored..."

# ── Step 4: Build & push Docker image ─────────────────────────────────────────
echo ""
echo "▸ Step 4: Building and pushing Docker image..."

ECR_REPO="${ACCOUNT_ID}.dkr.ecr.${AWS_REGION}.amazonaws.com/${REPO_NAME}"
GIT_SHA=$(git rev-parse HEAD)
IMAGE_TAG="${ECR_REPO}:${GIT_SHA}"
IMAGE_LATEST="${ECR_REPO}:${ENV}-latest"

# Login to ECR
aws ecr get-login-password --region "$AWS_REGION" | \
  docker login --username AWS --password-stdin "${ACCOUNT_ID}.dkr.ecr.${AWS_REGION}.amazonaws.com"

# Build
docker build --platform linux/amd64 -t "$IMAGE_TAG" -t "$IMAGE_LATEST" .

# Push
docker push "$IMAGE_TAG"
docker push "$IMAGE_LATEST"
echo "  ✓ Pushed $IMAGE_TAG"

# ── Step 5: Update ECS task definition & deploy ───────────────────────────────
echo ""
echo "▸ Step 5: Deploying to ECS..."

# Get current task def, update image
TASK_DEF=$(aws ecs describe-task-definition --task-definition "rail-${ENV}" --query 'taskDefinition')
NEW_TASK_DEF=$(echo "$TASK_DEF" | \
  jq --arg IMG "$IMAGE_TAG" '.containerDefinitions[0].image = $IMG' | \
  jq 'del(.taskDefinitionArn, .revision, .status, .requiresAttributes, .compatibilities, .registeredAt, .registeredBy)')

NEW_ARN=$(aws ecs register-task-definition --cli-input-json "$NEW_TASK_DEF" \
  --query 'taskDefinition.taskDefinitionArn' --output text)

aws ecs update-service \
  --cluster "$CLUSTER_NAME" \
  --service "rail-${ENV}" \
  --task-definition "$NEW_ARN" \
  --force-new-deployment

echo "  ✓ Deployed $NEW_ARN"

# ── Step 6: Wait for healthy ──────────────────────────────────────────────────
echo ""
echo "▸ Step 6: Waiting for service to stabilize..."
aws ecs wait services-stable --cluster "$CLUSTER_NAME" --services "rail-${ENV}" || true

echo ""
echo "═══ Deploy complete ═══"
echo ""
echo "Next steps:"
echo "  1. Point api.userail.money DNS CNAME → $ALB_DNS"
echo "  2. Wait for ACM certificate validation (add the DNS record Terraform shows)"
echo "  3. Run: ./scripts/recover-bridge-users.sh"
echo ""
