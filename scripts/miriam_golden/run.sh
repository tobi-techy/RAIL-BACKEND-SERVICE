#!/usr/bin/env bash
# Golden eval harness for Miriam Operator Core 10.
# Requires: EVAL_ENABLED=true, EVAL_TOKEN set, server running, USER_ID of a funded test user.
#
# Usage:
#   EVAL_TOKEN=... USER_ID=... ./scripts/miriam_golden/run.sh
#   EVAL_URL=http://localhost:8080/api/v1/eval/miriam EVAL_TOKEN=... USER_ID=... ./scripts/miriam_golden/run.sh
set -euo pipefail

EVAL_URL="${EVAL_URL:-http://localhost:8080/api/v1/eval/miriam}"
EVAL_TOKEN="${EVAL_TOKEN:?EVAL_TOKEN is required}"
USER_ID="${USER_ID:?USER_ID of a funded test user is required}"
SCENARIOS="$(cd "$(dirname "$0")" && pwd)/scenarios.json"

if ! command -v jq >/dev/null 2>&1; then
  echo "jq is required" >&2
  exit 1
fi

fail=0
pass=0
total=$(jq 'length' "$SCENARIOS")

echo "Miriam golden eval → $EVAL_URL ($total scenarios)"
echo "user_id=$USER_ID"
echo

conv_id=""

for i in $(seq 0 $((total - 1))); do
  id=$(jq -r ".[$i].id" "$SCENARIOS")
  msg=$(jq -r ".[$i].message" "$SCENARIOS")
  expect_pending=$(jq -r ".[$i].expect.pending_action // empty" "$SCENARIOS")
  must_not=$(jq -r ".[$i].expect.must_not_contain // empty" "$SCENARIOS")

  body=$(jq -n \
    --arg uid "$USER_ID" \
    --arg msg "$msg" \
    --arg cid "$conv_id" \
    '{user_id:$uid, message:$msg} + (if $cid != "" then {conversation_id:$cid} else {} end)')

  resp=$(curl -sS -X POST "$EVAL_URL" \
    -H "Content-Type: application/json" \
    -H "X-Eval-Token: $EVAL_TOKEN" \
    -d "$body" || true)

  if ! echo "$resp" | jq -e . >/dev/null 2>&1; then
    echo "FAIL  $id — invalid JSON response"
    echo "  $resp"
    fail=$((fail + 1))
    continue
  fi

  conv_id=$(echo "$resp" | jq -r '.conversation_id // empty')
  content=$(echo "$resp" | jq -r '.content // empty')
  quality=$(echo "$resp" | jq -r '.quality.pass // false')
  pending=$(echo "$resp" | jq -r '.pending_action.action // empty')
  latency=$(echo "$resp" | jq -r '.latency_ms // 0')

  ok=1
  if [[ "$quality" != "true" ]]; then
    ok=0
    failures=$(echo "$resp" | jq -c '.quality.failures // []')
    echo "FAIL  $id — quality gate ($failures)  ${latency}ms"
  fi
  if [[ -n "$expect_pending" && "$pending" != "$expect_pending" ]]; then
    ok=0
    echo "FAIL  $id — expected pending_action=$expect_pending got=${pending:-none}  ${latency}ms"
  fi
  if [[ -n "$must_not" && "$content" == *"$must_not"* ]]; then
    ok=0
    echo "FAIL  $id — response contained forbidden: $must_not  ${latency}ms"
  fi

  if [[ $ok -eq 1 ]]; then
    echo "PASS  $id  ${latency}ms  pending=${pending:-none}"
    pass=$((pass + 1))
  else
    echo "  content: ${content:0:160}"
    fail=$((fail + 1))
  fi
done

echo
echo "Result: $pass passed, $fail failed of $total"
if [[ $fail -gt 0 ]]; then
  exit 1
fi
