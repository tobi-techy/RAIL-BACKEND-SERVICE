#!/usr/bin/env bash
#
# onesignal_import.sh — Import RAIL users and device tokens into OneSignal.
#
# Step 1: Upsert each user (external_id + tags) via Create User API.
#         The Create User endpoint (POST /apps/{app_id}/users) acts as an upsert:
#         it creates new users and updates tags on existing ones (200/202).
#         Free plan limit: 3 tags per user (kyc_status, onboarding_status, country).
# Step 2: Import raw APNs/FCM device tokens via Add Device API (POST /api/v1/players).
#         Expo tokens (ExponentPushToken[...]) are skipped — OneSignal needs raw
#         push tokens, not Expo wrapper tokens. Those users will be registered by
#         the OneSignal SDK when they open the mobile app.
#
# Usage:
#   DATABASE_URL="postgres://..." ONESIGNAL_APP_ID="..." ONESIGNAL_API_KEY="..." \
#     bash scripts/onesignal_import.sh
#
set -uo pipefail

DB_URL="${DATABASE_URL:?DATABASE_URL is required}"
APP_ID="${ONESIGNAL_APP_ID:?ONESIGNAL_APP_ID is required}"
API_KEY="${ONESIGNAL_API_KEY:?ONESIGNAL_API_KEY is required}"

# Work around stale ~/.postgresql/root.crt that breaks Aiven SSL.
ROOT_CRT="$HOME/.postgresql/root.crt"
MOVED_CRT=""
if [[ -f "$ROOT_CRT" ]]; then
  mv "$ROOT_CRT" "${ROOT_CRT}.bak"
  MOVED_CRT=1
fi
cleanup() {
  if [[ -n "$MOVED_CRT" ]] && [[ -f "${ROOT_CRT}.bak" ]]; then
    mv "${ROOT_CRT}.bak" "$ROOT_CRT"
  fi
}
trap cleanup EXIT
export PGSSLROOTCERT=""

CREATE_URL="https://api.onesignal.com/apps/${APP_ID}/users"
PLAYERS_URL="https://onesignal.com/api/v1/players"

users_ok=0
users_fail=0
tokens_ok=0
tokens_fail=0

echo "=== OneSignal Import ==="
echo "App ID: $APP_ID"
echo ""

# ── Step 1: Upsert all users with tags ─────────────────────────────────────
echo "--- Step 1: Upserting user identities with tags (3 tags max on free plan) ---"

TMP_USERS=$(mktemp)
psql "$DB_URL" -t -A -F '|' -c "
  SELECT id, kyc_status, COALESCE(country, ''), onboarding_status
  FROM users
  WHERE is_active = true AND anonymized_at IS NULL
  ORDER BY created_at;
" > "$TMP_USERS" 2>&1 || true

while IFS='|' read -r id kyc_status country onboarding; do
  [[ -z "$id" ]] && continue
  body=$(printf '{"identity":{"external_id":"%s"},"properties":{"tags":{"kyc_status":"%s","onboarding_status":"%s","country":"%s"},"language":"en"}}' \
    "$id" "$kyc_status" "$onboarding" "$country")

  response=$(curl -s -w "\nHTTP_CODE:%{http_code}" -X POST "$CREATE_URL" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer ${API_KEY}" \
    -d "$body" 2>&1) || true

  http_code=$(echo "$response" | grep -o 'HTTP_CODE:[0-9]*' | cut -d: -f2)

  if [[ "$http_code" == "200" || "$http_code" == "202" ]]; then
    echo "  [OK]   ${id}  kyc=${kyc_status} onboarding=${onboarding}"
    users_ok=$((users_ok + 1))
  else
    body=$(echo "$response" | sed 's/HTTP_CODE:[0-9]*$//')
    echo "  [FAIL] ${id}  HTTP ${http_code}: ${body}"
    users_fail=$((users_fail + 1))
  fi
done < "$TMP_USERS"
rm -f "$TMP_USERS"

echo "  Users synced: ${users_ok}  Failed: ${users_fail}"
echo ""

# ── Step 2: Import raw APNs/FCM device tokens ──────────────────────────────
echo "--- Step 2: Importing raw APNs/FCM device tokens (skipping Expo tokens) ---"

TMP_TOKENS=$(mktemp)
psql "$DB_URL" -t -A -F '|' -c "
  SELECT dt.user_id, dt.token, dt.platform
  FROM device_tokens dt
  JOIN users u ON u.id = dt.user_id
  WHERE dt.is_active = true
    AND u.is_active = true
    AND u.anonymized_at IS NULL
    AND dt.token NOT LIKE 'ExponentPushToken[%'
  ORDER BY dt.user_id;
" > "$TMP_TOKENS" 2>&1 || true

while IFS='|' read -r user_id token platform; do
  [[ -z "$user_id" ]] && continue
  if [[ "$platform" == "ios" ]]; then
    device_type=0
  elif [[ "$platform" == "android" ]]; then
    device_type=1
  else
    echo "  [SKIP] ${user_id}  unknown platform: ${platform}"
    continue
  fi

  body=$(printf '{"app_id":"%s","device_type":%d,"identifier":"%s","external_user_id":"%s","notification_types":1}' \
    "$APP_ID" "$device_type" "$token" "$user_id")

  response=$(curl -s -w "\nHTTP_CODE:%{http_code}" -X POST "$PLAYERS_URL" \
    -H "Content-Type: application/json" \
    -H "Authorization: Basic ${API_KEY}" \
    -d "$body" 2>&1) || true

  http_code=$(echo "$response" | grep -o 'HTTP_CODE:[0-9]*' | cut -d: -f2)
  response_body=$(echo "$response" | sed 's/HTTP_CODE:[0-9]*$//')

  if [[ "$http_code" == "200" ]]; then
    player_id=$(echo "$response_body" | grep -o '"id":"[^"]*"' | head -1 | cut -d'"' -f4)
    echo "  [OK]   ${user_id}  token=${token:0:16}...  player=${player_id}"
    tokens_ok=$((tokens_ok + 1))
  else
    echo "  [FAIL] ${user_id}  token=${token:0:16}...  HTTP ${http_code}: ${response_body}"
    tokens_fail=$((tokens_fail + 1))
  fi
done < "$TMP_TOKENS"
rm -f "$TMP_TOKENS"

expo_count=$(psql "$DB_URL" -t -A -c "
  SELECT COUNT(*) FROM device_tokens dt
  JOIN users u ON u.id = dt.user_id
  WHERE dt.is_active = true AND u.is_active = true AND u.anonymized_at IS NULL
    AND dt.token LIKE 'ExponentPushToken[%';" 2>/dev/null || echo "0")

echo "  Tokens imported: ${tokens_ok}  Failed: ${tokens_fail}"
echo "  Expo tokens skipped: ${expo_count}"
echo ""
echo "=== Import Complete ==="
echo ""
echo "Notes:"
echo "  - Expo tokens can't be imported (OneSignal needs raw APNs/FCM tokens)."
echo "    Those users will be registered by the OneSignal SDK when they open the app."
echo "  - Ensure the mobile app calls OneSignal.login(userUUID) on login."
echo "  - Verify in OneSignal dashboard: Audience > Users"
