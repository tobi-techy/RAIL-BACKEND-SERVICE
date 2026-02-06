#!/bin/bash

# Generate secure secrets for RAIL Backend deployment
# Usage: ./scripts/secrets/generate-secrets.sh [--output .env]

set -e

OUTPUT_FILE="${1:-.env}"

echo "=========================================="
echo "RAIL Backend Secret Generation"
echo "=========================================="
echo "Output: $OUTPUT_FILE"
echo ""

# Generate secrets
JWT_SECRET=$(openssl rand -base64 32)
ENCRYPTION_KEY=$(openssl rand -base64 32 | head -c 32)
ALPACA_WEBHOOK_SECRET=$(openssl rand -base64 32)
CIRCLE_WEBHOOK_SECRET=$(openssl rand -base64 32)
BRIDGE_WEBHOOK_SECRET=$(openssl rand -base64 32)
SESSION_KEY=$(openssl rand -base64 32)

echo "Generated secrets:"
echo "  JWT_SECRET: ${JWT_SECRET:0:10}...${JWT_SECRET: -5}"
echo "  ENCRYPTION_KEY: ${ENCRYPTION_KEY:0:10}...${ENCRYPTION_KEY: -5}"
echo "  ALPACA_WEBHOOK_SECRET: ${ALPACA_WEBHOOK_SECRET:0:10}...${ALPACA_WEBHOOK_SECRET: -5}"
echo "  CIRCLE_WEBHOOK_SECRET: ${CIRCLE_WEBHOOK_SECRET:0:10}...${CIRCLE_WEBHOOK_SECRET: -5}"
echo "  BRIDGE_WEBHOOK_SECRET: ${BRIDGE_WEBHOOK_SECRET:0:10}...${BRIDGE_WEBHOOK_SECRET: -5}"
echo "  SESSION_KEY: ${SESSION_KEY:0:10}...${SESSION_KEY: -5}"
echo ""

# Create .env file if it doesn't exist, or backup existing
if [ -f "$OUTPUT_FILE" ]; then
    echo "Backing up existing $OUTPUT_FILE to ${OUTPUT_FILE}.bak"
    cp "$OUTPUT_FILE" "${OUTPUT_FILE}.bak"
fi

# Generate .env file
cat > "$OUTPUT_FILE" << 'ENVFILE'
# ============================================
# RAIL Backend Service - Environment Variables
# ============================================
# ⚠️  GENERATED AUTOMATICALLY - DO NOT COMMIT TO VERSION CONTROL
# Copy .env.example and fill in values for local development
# For production, use AWS Secrets Manager or similar secure storage
# ============================================

# ============================================
# Application Configuration
# ============================================
ENVIRONMENT=development
PORT=8080
BASE_URL=http://localhost:8080

# ============================================
# Database Configuration
# ============================================
DATABASE_URL=postgres://postgres:postgres@localhost:5432/rail_service_dev?sslmode=disable
DATABASE_SSL_MODE=disable

# ============================================
# Redis Configuration
# ============================================
REDIS_HOST=localhost
REDIS_PORT=6379
REDIS_PASSWORD=
REDIS_DB=0
REDIS_CLUSTER_MODE=false

# ============================================
# Rate Limiting Configuration
# ============================================
RATE_LIMIT_ENABLED=true
RATE_LIMIT_GLOBAL_LIMIT=10000
RATE_LIMIT_GLOBAL_WINDOW=60
RATE_LIMIT_IP_LIMIT=500
RATE_LIMIT_IP_WINDOW=60
RATE_LIMIT_USER_LIMIT=200
RATE_LIMIT_USER_WINDOW=60
RATE_LIMIT_FAIL_OPEN=true
RATE_LIMIT_RESPONSE_HEADERS=true

# ============================================
# Security & Authentication
# ============================================
# JWT Secret - MUST be changed in production (min 32 characters, generate with: openssl rand -base64 32)
JWT_SECRET=@@JWT_SECRET@@

# Encryption Key - MUST be exactly 32 bytes for AES-256 (generate with: openssl rand -base64 32)
ENCRYPTION_KEY=@@ENCRYPTION_KEY@@

# Session key for additional encryption
SESSION_KEY=@@SESSION_KEY@@

# Password Policy (ENFORCE these in production)
PASSWORD_MIN_LENGTH=12
PASSWORD_REQUIRE_UPPERCASE=true
PASSWORD_REQUIRE_LOWERCASE=true
PASSWORD_REQUIRE_NUMBERS=true
PASSWORD_REQUIRE_SPECIAL=true
PASSWORD_EXPIRATION_DAYS=90
PASSWORD_HISTORY_COUNT=5

# ============================================
# Webhook Secrets (for signature verification)
# ============================================
# Generate with: openssl rand -base64 32
ALPACA_WEBHOOK_SECRET=@@ALPACA_WEBHOOK_SECRET@@
CIRCLE_WEBHOOK_SECRET=@@CIRCLE_WEBHOOK_SECRET@@
BRIDGE_WEBHOOK_SECRET=@@BRIDGE_WEBHOOK_SECRET@@

# ============================================
# Circle Configuration (Payments)
# ============================================
# Sandbox: Use Circle's sandbox environment
CIRCLE_API_KEY=your-circle-sandbox-api-key
CIRCLE_ENVIRONMENT=sandbox
CIRCLE_BASE_URL=https://api.circle.com
CIRCLE_ENTITY_SECRET_CIPHERTEXT=your-entity-secret-ciphertext
CIRCLE_DEFAULT_WALLET_SET_ID=
CIRCLE_DEFAULT_WALLET_SET_NAME=RAIL-WalletSet
CIRCLE_SUPPORTED_CHAINS=SOL-DEVNET,ETH-SEPOLIA,MATIC-MUMBAI

# ============================================
# Alpaca Configuration (Trading)
# ============================================
# Sandbox: https://broker-api.sandbox.alpaca.markets
# Production: https://broker-api.alpaca.markets
ALPACA_API_KEY=your-alpaca-sandbox-api-key
ALPACA_API_SECRET=your-alpaca-sandbox-secret
ALPACA_BASE_URL=https://broker-api.sandbox.alpaca.markets
ALPACA_DATA_BASE_URL=https://data.sandbox.alpaca.markets
ALPACA_ENVIRONMENT=sandbox

# ============================================
# Bridge Configuration (Banking)
# ============================================
# Sandbox: https://api.bridge.xyz/sandbox
# Production: https://api.bridge.xyz
BRIDGE_API_KEY=your-bridge-sandbox-api-key
BRIDGE_BASE_URL=https://api.bridge.xyz
BRIDGE_ENVIRONMENT=sandbox
BRIDGE_TIMEOUT=30
BRIDGE_MAX_RETRIES=3
BRIDGE_SUPPORTED_CHAINS=ETH,AVAX,SOL

# ============================================
# KYC Provider (Sumsub)
# ============================================
KYC_PROVIDER=sumsub
KYC_API_KEY=your-sumsub-app-token
KYC_API_SECRET=your-sumsub-secret-key
KYC_BASE_URL=https://api.sumsub.com
KYC_LEVEL_NAME=basic-kyc

# ============================================
# Email Service (Resend)
# ============================================
EMAIL_PROVIDER=resend
RESEND_API_KEY=re_xxxxxxxxxxxxx
EMAIL_FROM_EMAIL=no-reply@rail-service.com
EMAIL_FROM_NAME=RAIL Service
EMAIL_BASE_URL=http://localhost:3000

# ============================================
# AI Providers
# ============================================
OPENAI_API_KEY=sk-your-openai-key
GEMINI_API_KEY=your-gemini-api-key
AI_PRIMARY_PROVIDER=openai

# ============================================
# Secrets Provider
# ============================================
# Options: "env", "aws_secrets_manager"
SECRETS_PROVIDER=env
AWS_SECRETS_REGION=us-east-1
AWS_SECRETS_PREFIX=rail/

# ============================================
# Security Enhancements
# ============================================
BCRYPT_COST=12
ACCESS_TOKEN_TTL=900      # 15 minutes
REFRESH_TOKEN_TTL=604800  # 7 days
ENABLE_TOKEN_BLACKLIST=true
CHECK_PASSWORD_BREACHES=true
CAPTCHA_THRESHOLD=3

# Admin security
ADMIN_BOOTSTRAP_TOKEN=
DISABLE_ADMIN_CREATION=false

# ============================================
# Monitoring & Observability
# ============================================
OTEL_COLLECTOR_URL=localhost:4317
OTEL_SERVICE_NAME=rail-backend

# ============================================
# Multi-Region (Production)
# ============================================
# PRIMARY_REGION=us-east-1
# SECONDARY_REGION=eu-west-1
# READ_REPLICA_EU_WEST_1_HOST=
# READ_REPLICA_AP_NORTHEAST_1_HOST=
ENVFILE

# Replace placeholders with actual values
sed -i '' "s|@@JWT_SECRET@@|${JWT_SECRET}|g" "$OUTPUT_FILE"
sed -i '' "s|@@ENCRYPTION_KEY@@|${ENCRYPTION_KEY}|g" "$OUTPUT_FILE"
sed -i '' "s|@@SESSION_KEY@@|${SESSION_KEY}|g" "$OUTPUT_FILE"
sed -i '' "s|@@ALPACA_WEBHOOK_SECRET@@|${ALPACA_WEBHOOK_SECRET}|g" "$OUTPUT_FILE"
sed -i '' "s|@@CIRCLE_WEBHOOK_SECRET@@|${CIRCLE_WEBHOOK_SECRET}|g" "$OUTPUT_FILE"
sed -i '' "s|@@BRIDGE_WEBHOOK_SECRET@@|${BRIDGE_WEBHOOK_SECRET}|g" "$OUTPUT_FILE"

echo ""
echo "=========================================="
echo "✅ Secrets generated successfully!"
echo "=========================================="
echo ""
echo "Next steps:"
echo "1. Update sandbox API keys (Circle, Alpaca, Bridge)"
echo "2. Run: ./scripts/security/check-config.sh development"
echo "3. Start the service: go run cmd/main.go"
echo ""
echo "⚠️  IMPORTANT: Never commit $OUTPUT_FILE to version control!"
echo "   Add to .gitignore if not already present"
