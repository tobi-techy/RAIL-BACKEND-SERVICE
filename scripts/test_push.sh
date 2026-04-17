#!/bin/bash
# Test push notifications via AWS SNS
# Usage: ./scripts/test_push.sh <endpoint_arn> [message]
#
# To find a user's endpoint ARN:
#   psql $DATABASE_URL -c "SELECT endpoint_arn, token, platform FROM device_tokens WHERE user_id = 'USER_ID' AND is_active = true"
#
# To test without an endpoint ARN (sends directly via SNS platform app):
#   ./scripts/test_push.sh --token <device_token>

set -e

REGION="${AWS_REGION:-us-east-1}"
IOS_PLATFORM_ARN=$(aws ssm get-parameter --region $REGION --name "/rail/prod/SNS_PUSH_IOS_PLATFORM_ARN" --with-decryption --query "Parameter.Value" --output text 2>/dev/null || echo "")

if [ -z "$IOS_PLATFORM_ARN" ]; then
  echo "Error: Could not fetch SNS_PUSH_IOS_PLATFORM_ARN from SSM"
  echo "Set it manually: export IOS_PLATFORM_ARN=arn:aws:sns:..."
  exit 1
fi

TITLE="${2:-🔥 Test Notification}"
BODY="${3:-This is a test push from Rail. If you see this, notifications work!}"

# Build the SNS message payload
build_payload() {
  local title="$1"
  local body="$2"

  APNS=$(cat <<EOF
{"aps":{"alert":{"title":"$title","body":"$body"},"sound":"default"},"data":{"type":"test"}}
EOF
)

  cat <<EOF
{
  "default": "$body",
  "APNS": "$(echo $APNS | sed 's/"/\\"/g')",
  "APNS_SANDBOX": "$(echo $APNS | sed 's/"/\\"/g')"
}
EOF
}

if [ "$1" = "--token" ]; then
  # Create a temporary endpoint from a device token
  TOKEN="$2"
  if [ -z "$TOKEN" ]; then
    echo "Usage: $0 --token <device_push_token>"
    exit 1
  fi

  echo "Creating SNS endpoint for token: ${TOKEN:0:20}..."
  ENDPOINT_ARN=$(aws sns create-platform-endpoint \
    --region $REGION \
    --platform-application-arn "$IOS_PLATFORM_ARN" \
    --token "$TOKEN" \
    --query "EndpointArn" --output text)

  echo "Endpoint ARN: $ENDPOINT_ARN"
  TITLE="${3:-🔥 Test Notification}"
  BODY="${4:-This is a test push from Rail. If you see this, notifications work!}"
elif [ -n "$1" ]; then
  ENDPOINT_ARN="$1"
else
  echo "Usage:"
  echo "  $0 <endpoint_arn> [title] [body]"
  echo "  $0 --token <device_push_token> [title] [body]"
  echo ""
  echo "Examples:"
  echo "  $0 arn:aws:sns:us-east-1:885160773772:endpoint/APNS/RailIOS/abc123"
  echo "  $0 --token abc123devicetoken456"
  echo "  $0 arn:aws:... '🎯 Challenge Complete!' 'You earned 50 XP'"
  echo ""
  echo "Find endpoint ARNs:"
  echo "  psql \$DATABASE_URL -c \"SELECT endpoint_arn FROM device_tokens WHERE is_active = true LIMIT 5\""
  exit 1
fi

echo ""
echo "Sending push notification..."
echo "  To: ${ENDPOINT_ARN:0:60}..."
echo "  Title: $TITLE"
echo "  Body: $BODY"
echo ""

PAYLOAD=$(build_payload "$TITLE" "$BODY")

RESULT=$(aws sns publish \
  --region $REGION \
  --target-arn "$ENDPOINT_ARN" \
  --message "$PAYLOAD" \
  --message-structure json \
  --output json 2>&1)

if echo "$RESULT" | grep -q "MessageId"; then
  MSG_ID=$(echo "$RESULT" | grep -o '"MessageId": "[^"]*"' | cut -d'"' -f4)
  echo "✅ Push sent successfully!"
  echo "   MessageId: $MSG_ID"
else
  echo "❌ Push failed:"
  echo "$RESULT"
  echo ""
  echo "Common issues:"
  echo "  - EndpointDisabled: device token is stale, user needs to re-register"
  echo "  - InvalidParameter: endpoint ARN is wrong"
  echo "  - AuthorizationError: IAM role missing sns:Publish permission"
fi
