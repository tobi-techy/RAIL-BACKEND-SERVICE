#!/bin/bash

# ============================================================================
# Security Check Script for RAIL Backend
# ============================================================================
# This script validates that critical security settings are properly configured
# and warns about default/insecure values before deployment.
#
# Usage: ./scripts/security/check-config.sh [environment]
#   environment: development, staging, production (default: current ENVIRONMENT value)
# ============================================================================

# Don't exit on error - we want to check all issues
# set -e

# Colors for output
RED='\033[0;31m'
YELLOW='\033[1;33m'
GREEN='\033[0;32m'
NC='\033[0m' # No Color

# Default environment
ENVIRONMENT="${1:-${ENVIRONMENT:-development}}"

echo "========================================="
echo "RAIL Backend Security Configuration Check"
echo "Environment: ${ENVIRONMENT}"
echo "========================================="
echo ""

ERRORS=0
WARNINGS=0

# Function to print error
error() {
    echo -e "${RED}✗ ERROR: $1${NC}"
    ((ERRORS++))
}

# Function to print warning
warning() {
    echo -e "${YELLOW}⚠ WARNING: $1${NC}"
    ((WARNINGS++))
}

# Function to print success
success() {
    echo -e "${GREEN}✓ $1${NC}"
}

# Function to safely extract value (handles non-zero exit codes)
safe_extract() {
    echo "$1" | grep -o "$2" || echo ""
}

# Check if .env file exists
if [ ! -f .env ]; then
    error ".env file not found. Please create it from .env.example"
    exit 1
fi

# Load environment variables
set -a
source .env
set +a

echo "1. Checking Database Security..."
echo "----------------------------------------"

# Check SSL mode for production
if [ "$ENVIRONMENT" = "production" ]; then
    if [[ "$DATABASE_URL" != *"sslmode=require"* ]] && [[ "$DATABASE_URL" != *"sslmode=verify-ca"* ]] && [[ "$DATABASE_URL" != *"sslmode=verify-full"* ]]; then
        error "Database SSL is not enabled in production. DATABASE_URL must use sslmode=require or stricter"
    else
        success "Database SSL is enabled"
    fi
else
    if [[ "$DATABASE_URL" == *"sslmode=disable"* ]]; then
        warning "Database SSL is disabled (acceptable for $ENVIRONMENT)"
    else
        success "Database SSL mode: $(safe_extract "$DATABASE_URL" 'sslmode=[^&]*')"
    fi
fi

echo ""
echo "2. Checking Encryption Secrets..."
echo "----------------------------------------"

# Check JWT_SECRET
if [ "$JWT_SECRET" = "your-super-secret-jwt-key-change-in-production-min-32-chars" ] || \
   [ "$JWT_SECRET" = "" ]; then
    if [ "$ENVIRONMENT" = "production" ]; then
        error "JWT_SECRET is using default value or is empty"
    else
        warning "JWT_SECRET is using default value (acceptable for $ENVIRONMENT, change before deployment)"
    fi
else
    JWT_LENGTH=${#JWT_SECRET}
    if [ $JWT_LENGTH -lt 32 ]; then
        error "JWT_SECRET is too short ($JWT_LENGTH characters, minimum 32 required)"
    else
        success "JWT_SECRET length: $JWT_LENGTH characters"
    fi
fi

# Check ENCRYPTION_KEY
if [ "$ENCRYPTION_KEY" = "your-32-byte-encryption-key-!!" ] || \
   [ "$ENCRYPTION_KEY" = "" ]; then
    if [ "$ENVIRONMENT" = "production" ]; then
        error "ENCRYPTION_KEY is using default value or is empty"
    else
        warning "ENCRYPTION_KEY is using default value (acceptable for $ENVIRONMENT, change before deployment)"
    fi
else
    ENC_LENGTH=${#ENCRYPTION_KEY}
    if [ $ENC_LENGTH -ne 32 ]; then
        error "ENCRYPTION_KEY must be exactly 32 bytes (currently $ENC_LENGTH characters)"
    else
        success "ENCRYPTION_KEY length: 32 bytes"
    fi
fi

echo ""
echo "3. Checking Password Policy..."
echo "----------------------------------------"

# Check password minimum length
PASSWORD_MIN_LENGTH=${PASSWORD_MIN_LENGTH:-8}
if [ "$PASSWORD_MIN_LENGTH" -lt 12 ]; then
    if [ "$ENVIRONMENT" = "production" ]; then
        error "Password minimum length is less than 12 (currently: $PASSWORD_MIN_LENGTH)"
    else
        warning "Password minimum length is $PASSWORD_MIN_LENGTH (recommended: 12)"
    fi
else
    success "Password minimum length: $PASSWORD_MIN_LENGTH"
fi

echo ""
echo "4. Checking External API Keys..."
echo "----------------------------------------"

# Check for missing API keys in production
if [ "$ENVIRONMENT" = "production" ]; then
    MISSING_KEYS=()

    [ -z "$CIRCLE_API_KEY" ] && MISSING_KEYS+=("CIRCLE_API_KEY")
    [ -z "$ALPACA_API_KEY" ] && MISSING_KEYS+=("ALPACA_API_KEY")
    [ -z "$ALPACA_API_SECRET" ] && MISSING_KEYS+=("ALPACA_API_SECRET")
    [ -z "$BRIDGE_API_KEY" ] && MISSING_KEYS+=("BRIDGE_API_KEY")
    [ -z "$SUMSUB_APP_TOKEN" ] && MISSING_KEYS+=("SUMSUB_APP_TOKEN")
    [ -z "$SUMSUB_SECRET_KEY" ] && MISSING_KEYS+=("SUMSUB_SECRET_KEY")

    if [ ${#MISSING_KEYS[@]} -gt 0 ]; then
        error "Missing production API keys: ${MISSING_KEYS[*]}"
    else
        success "All required API keys are set"
    fi
else
    success "API key check skipped for $ENVIRONMENT"
fi

echo ""
echo "5. Checking Environment Flags..."
echo "----------------------------------------"

# Check debug mode
if [ "$ENVIRONMENT" = "production" ]; then
    if [ "$DEBUG_MODE" = "true" ]; then
        error "DEBUG_MODE is enabled in production"
    else
        success "DEBUG_MODE is disabled"
    fi

    if [ "$ENABLE_SWAGGER" = "true" ]; then
        warning "Swagger is enabled in production (consider disabling)"
    else
        success "Swagger is disabled"
    fi

    if [ "$ENABLE_PPROF" = "true" ]; then
        error "pprof is enabled in production (exposes internal metrics)"
    else
        success "pprof is disabled"
    fi
else
    success "Debug flags check skipped for $ENVIRONMENT"
fi

echo ""
echo "6. Checking .gitignore..."
echo "----------------------------------------"

if grep -q "^\.env$" .gitignore; then
    success ".env is in .gitignore"
else
    error ".env is not in .gitignore (risk of committing secrets!)"
fi

if grep -q "\*\.key$" .gitignore && grep -q "\*\.pem$" .gitignore; then
    success "Key files are in .gitignore"
else
    error "Key files (*.key, *.pem) are not properly ignored"
fi

echo ""
echo "========================================="
echo "Security Check Summary"
echo "========================================="

if [ $ERRORS -eq 0 ] && [ $WARNINGS -eq 0 ]; then
    echo -e "${GREEN}✓ All security checks passed!${NC}"
    exit 0
elif [ $ERRORS -eq 0 ]; then
    echo -e "${YELLOW}⚠ $WARNINGS warning(s) found. Review before deployment.${NC}"
    exit 0
else
    echo -e "${RED}✗ $ERRORS error(s) and $WARNINGS warning(s) found. Fix before deployment!${NC}"
    exit 1
fi
