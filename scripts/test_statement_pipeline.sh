#!/usr/bin/env bash
# =============================================================================
# Rail Bank Statement Pipeline - End-to-End Test Script
# =============================================================================
# Prerequisites:
#   - Docker running (docker-compose up -d)
#   - Server running (make run or go run cmd/main.go)
#   - A test PDF file
#
# Usage:
#   ./scripts/test_statement_pipeline.sh [OPTIONS]
#
# Options:
#   --host       API host (default: http://localhost:8080)
#   --token      JWT auth token (required)
#   --file       Path to test PDF/image (optional, creates a test file if missing)
#   --bank       Bank name (default: "Test Bank")
#   --v2         Use V2 endpoint (default: true)
#   --wait       Max seconds to wait for processing (default: 120)
# =============================================================================

set -euo pipefail

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

# Defaults
HOST="${HOST:-http://localhost:8080}"
TOKEN=""
FILE=""
BANK="Test Bank"
USE_V2=true
MAX_WAIT=120

# Parse args
while [[ $# -gt 0 ]]; do
  case $1 in
    --host) HOST="$2"; shift 2;;
    --token) TOKEN="$2"; shift 2;;
    --file) FILE="$2"; shift 2;;
    --bank) BANK="$2"; shift 2;;
    --v2) USE_V2=true; shift;;
    --v1) USE_V2=false; shift;;
    --wait) MAX_WAIT="$2"; shift 2;;
    *) echo "Unknown option: $1"; exit 1;;
  esac
done

if [[ -z "$TOKEN" ]]; then
  echo -e "${RED}ERROR: --token is required${NC}"
  echo "  Get a token by logging in: curl -X POST $HOST/v1/auth/login ..."
  exit 1
fi

# Header
echo -e "${BLUE}╔══════════════════════════════════════════════╗${NC}"
echo -e "${BLUE}║   Rail Statement Pipeline E2E Test          ║${NC}"
echo -e "${BLUE}╠══════════════════════════════════════════════╣${NC}"
echo -e "${BLUE}║ Host: ${NC}$HOST"
echo -e "${BLUE}║ Mode: ${NC}$([ "$USE_V2" = true ] && echo "V2 (multi-strategy)" || echo "V1 (legacy)")"
echo -e "${BLUE}╚══════════════════════════════════════════════╝${NC}"
echo

# Helper functions
api() {
  local method=$1 path=$2
  shift 2
  curl -s -X "$method" "$HOST$path" \
    -H "Authorization: Bearer $TOKEN" \
    -H "Accept: application/json" \
    "$@"
}

check_status() {
  local code
  code=$(curl -s -o /dev/null -w "%{http_code}" -X GET "$HOST/health")
  if [[ "$code" != "200" ]]; then
    echo -e "${RED}✗ Server not reachable at $HOST (status: $code)${NC}"
    exit 1
  fi
  echo -e "${GREEN}✓ Server is up${NC}"
}

# Create a minimal test PDF if no file provided
create_test_pdf() {
  local tmpfile="/tmp/rail_test_statement.pdf"
  # Minimal valid PDF with text content
  cat > "$tmpfile" << 'PDFEOF'
%PDF-1.4
1 0 obj<</Type/Catalog/Pages 2 0 R>>endobj
2 0 obj<</Type/Pages/Kids[3 0 R]/Count 1>>endobj
3 0 obj<</Type/Page/MediaBox[0 0 612 792]/Parent 2 0 R/Contents 4 0 R/Resources<</Font<</F1 5 0 R>>>>>>endobj
4 0 obj<</Length 314>>
stream
BT /F1 10 Tf
50 750 Td (BANK STATEMENT - Test Bank) Tj
50 730 Td (Account: 0123456789) Tj
50 710 Td (Period: 01/01/2025 - 31/01/2025) Tj
50 680 Td (Date        Description              Amount    Balance) Tj
50 660 Td (15/01/2025  Salary Credit           500000.00  500000.00) Tj
50 640 Td (18/01/2025  POS Purchase Shoprite     15000.00  485000.00) Tj
50 620 Td (20/01/2025  Transfer to Rent         200000.00  285000.00) Tj
50 600 Td (25/01/2025  Airtime MTN                2000.00  283000.00) Tj
50 580 Td (28/01/2025  Uber Trip                  5500.00  277500.00) Tj
ET
endstream
endobj
5 0 obj<</Type/Font/Subtype/Type1/BaseFont/Courier>>endobj
xref
0 6
0000000000 65535 f 
0000000009 00000 n 
0000000058 00000 n 
0000000115 00000 n 
0000000266 00000 n 
0000000632 00000 n 
trailer<</Size 6/Root 1 0 R>>
startxref
695
%%EOF
PDFEOF
  echo "$tmpfile"
}

# =============================================================================
# TEST 1: Health check
# =============================================================================
echo -e "${YELLOW}[1/7] Checking server health...${NC}"
check_status

# =============================================================================
# TEST 2: Prepare test file
# =============================================================================
echo -e "${YELLOW}[2/7] Preparing test file...${NC}"
if [[ -z "$FILE" ]]; then
  FILE=$(create_test_pdf)
  echo -e "  Using generated test PDF: $FILE"
else
  if [[ ! -f "$FILE" ]]; then
    echo -e "${RED}✗ File not found: $FILE${NC}"
    exit 1
  fi
  echo -e "  Using: $FILE"
fi
echo -e "${GREEN}✓ Test file ready ($(wc -c < "$FILE" | tr -d ' ') bytes)${NC}"

# =============================================================================
# TEST 3: Upload statement
# =============================================================================
echo -e "${YELLOW}[3/7] Uploading statement...${NC}"
if [[ "$USE_V2" = true ]]; then
  UPLOAD_PATH="/v1/ai/v2/statement/upload"
else
  UPLOAD_PATH="/v1/ai/statement/upload"
fi

UPLOAD_RESPONSE=$(curl -s -w "\n%{http_code}" -X POST "$HOST$UPLOAD_PATH" \
  -H "Authorization: Bearer $TOKEN" \
  -F "file=@$FILE" \
  -F "bank_name=$BANK")

HTTP_CODE=$(echo "$UPLOAD_RESPONSE" | tail -1)
BODY=$(echo "$UPLOAD_RESPONSE" | sed '$d')

if [[ "$HTTP_CODE" != "202" ]]; then
  echo -e "${RED}✗ Upload failed (HTTP $HTTP_CODE)${NC}"
  echo "$BODY" | python3 -m json.tool 2>/dev/null || echo "$BODY"
  exit 1
fi

UPLOAD_ID=$(echo "$BODY" | python3 -c "import sys,json; print(json.load(sys.stdin)['data']['upload_id'])" 2>/dev/null)
if [[ -z "$UPLOAD_ID" ]]; then
  echo -e "${RED}✗ Could not extract upload_id from response${NC}"
  echo "$BODY"
  exit 1
fi
echo -e "${GREEN}✓ Upload accepted: $UPLOAD_ID${NC}"

# =============================================================================
# TEST 4: Poll for completion
# =============================================================================
echo -e "${YELLOW}[4/7] Waiting for processing (max ${MAX_WAIT}s)...${NC}"
START_TIME=$(date +%s)
STATUS="pending"

while [[ "$STATUS" != "completed" && "$STATUS" != "failed" ]]; do
  ELAPSED=$(( $(date +%s) - START_TIME ))
  if [[ $ELAPSED -gt $MAX_WAIT ]]; then
    echo -e "${RED}✗ Timeout after ${MAX_WAIT}s (last status: $STATUS)${NC}"
    exit 1
  fi

  sleep 3
  STATUS_RESPONSE=$(api GET "/v1/ai/statement/$UPLOAD_ID/status")
  STATUS=$(echo "$STATUS_RESPONSE" | python3 -c "import sys,json; print(json.load(sys.stdin)['data']['status'])" 2>/dev/null || echo "unknown")
  printf "  [%ds] Status: %s\r" "$ELAPSED" "$STATUS"
done
echo

if [[ "$STATUS" == "failed" ]]; then
  ERROR_MSG=$(echo "$STATUS_RESPONSE" | python3 -c "import sys,json; d=json.load(sys.stdin)['data']; print(d.get('error_message','unknown'))" 2>/dev/null)
  echo -e "${RED}✗ Processing failed: $ERROR_MSG${NC}"
  exit 1
fi
echo -e "${GREEN}✓ Processing completed!${NC}"

# =============================================================================
# TEST 5: Check status details
# =============================================================================
echo -e "${YELLOW}[5/7] Verifying status details...${NC}"
TXN_COUNT=$(echo "$STATUS_RESPONSE" | python3 -c "import sys,json; print(json.load(sys.stdin)['data']['transaction_count'])" 2>/dev/null)
echo "  Transactions found: $TXN_COUNT"

if [[ "$TXN_COUNT" -eq 0 ]]; then
  echo -e "${RED}✗ No transactions extracted${NC}"
  exit 1
fi
echo -e "${GREEN}✓ $TXN_COUNT transactions stored${NC}"

# Show summary if available
echo "$STATUS_RESPONSE" | python3 -c "
import sys, json
data = json.load(sys.stdin)['data']
if 'summary' in data and data['summary']:
    s = data['summary']
    print(f\"  Bank: {s.get('bank_name', 'N/A')}\")
    print(f\"  Period: {s.get('period_start', '?')} to {s.get('period_end', '?')}\")
    print(f\"  Income: {s.get('currency', '')} {s.get('total_income', '0')}\")
    print(f\"  Spending: {s.get('currency', '')} {s.get('total_spending', '0')}\")
    if 'extraction_strategy' in s:
        print(f\"  Strategy: {s['extraction_strategy']} / Parser: {s.get('parser_used', 'N/A')}\")
" 2>/dev/null || true

# =============================================================================
# TEST 6: Fetch transactions
# =============================================================================
echo -e "${YELLOW}[6/7] Fetching transactions...${NC}"
TXN_RESPONSE=$(api GET "/v1/ai/statement/$UPLOAD_ID/transactions?limit=5")
TXN_FETCHED=$(echo "$TXN_RESPONSE" | python3 -c "import sys,json; print(len(json.load(sys.stdin)['data']['transactions']))" 2>/dev/null || echo "0")

if [[ "$TXN_FETCHED" -eq 0 ]]; then
  echo -e "${RED}✗ Could not fetch transactions${NC}"
  echo "$TXN_RESPONSE" | python3 -m json.tool 2>/dev/null || echo "$TXN_RESPONSE"
  exit 1
fi

echo -e "${GREEN}✓ Fetched $TXN_FETCHED transactions${NC}"
echo "$TXN_RESPONSE" | python3 -c "
import sys, json
txns = json.load(sys.stdin)['data']['transactions']
for t in txns[:5]:
    print(f\"  {t['transaction_date']}  {t['type']:6s}  {t['amount']:>12s}  {t['description'][:40]}\")
" 2>/dev/null || true

# =============================================================================
# TEST 7: List statements
# =============================================================================
echo -e "${YELLOW}[7/7] Listing user statements...${NC}"
LIST_RESPONSE=$(api GET "/v1/ai/statements")
STMT_COUNT=$(echo "$LIST_RESPONSE" | python3 -c "import sys,json; print(len(json.load(sys.stdin)['data']['statements']))" 2>/dev/null || echo "0")
echo -e "${GREEN}✓ User has $STMT_COUNT statement(s)${NC}"

# =============================================================================
# CLEANUP (optional)
# =============================================================================
echo
echo -e "${BLUE}╔══════════════════════════════════════════════╗${NC}"
echo -e "${BLUE}║           ALL TESTS PASSED ✓                ║${NC}"
echo -e "${BLUE}╠══════════════════════════════════════════════╣${NC}"
echo -e "${BLUE}║ Upload ID:  ${NC}$UPLOAD_ID"
echo -e "${BLUE}║ Txns:       ${NC}$TXN_COUNT"
echo -e "${BLUE}║ Duration:   ${NC}${ELAPSED}s"
echo -e "${BLUE}╚══════════════════════════════════════════════╝${NC}"
echo
echo "To delete: curl -X DELETE $HOST/v1/ai/statement/$UPLOAD_ID -H 'Authorization: Bearer \$TOKEN'"
