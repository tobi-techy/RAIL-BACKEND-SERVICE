# Unosend Quick Start Guide

## 5-Minute Setup

### Step 1: Get API Key (2 minutes)

1. Go to [unosend.co/signup](https://unosend.co/signup)
2. Sign up with email
3. Click **Dashboard** → **API Keys**
4. Generate new API key
5. Copy key (format: `un_xxxxxxxx`)

### Step 2: Configure Environment (1 minute)

Add to your `.env` file:

```bash
EMAIL_PROVIDER=unosend
UNOSEND_API_KEY=un_your_api_key_here
EMAIL_FROM_EMAIL=noreply@yourdomain.com
EMAIL_FROM_NAME=RAIL
EMAIL_REPLY_TO=support@yourdomain.com
```

### Step 3: Verify Domain (2 minutes - Optional for Production)

1. In Unosend dashboard: **Settings** → **Domains**
2. Add your domain
3. Add DNS records (Unosend will show them):
   - DKIM record
   - SPF record
   - DMARC record
4. Click **Verify** once DNS propagates (~5-30 minutes)

### Step 4: Test Email Sending (1 minute)

```bash
# Start the server
go run ./cmd/main.go

# Send a test email via verification endpoint
curl -X POST http://localhost:8080/api/v1/auth/verify \
  -H "Content-Type: application/json" \
  -d '{"email": "your-email@example.com"}'

# Check Unosend dashboard for the sent email
# https://unosend.co/dashboard → Messages
```

## Environment Variables Explained

| Variable | Required | Example | Description |
|----------|----------|---------|-------------|
| `EMAIL_PROVIDER` | Yes | `unosend` | Email provider to use |
| `UNOSEND_API_KEY` | Yes | `un_abc123...` | Unosend API key (starts with `un_`) |
| `EMAIL_FROM_EMAIL` | Yes | `noreply@rail.com` | Sender email address |
| `EMAIL_FROM_NAME` | No | `RAIL` | Display name for sender |
| `EMAIL_REPLY_TO` | No | `support@rail.com` | Reply-to address |

## Supported Email Types

✅ Verification codes
✅ KYC status updates
✅ Welcome emails
✅ Login alerts
✅ Custom emails

All existing email templates work automatically.

## Switching Back to Other Providers

If needed, switch providers by changing environment variables only:

### Switch to Resend
```bash
EMAIL_PROVIDER=resend
RESEND_API_KEY=your_resend_key
```

### Switch to SendGrid
```bash
EMAIL_PROVIDER=sendgrid
EMAIL_API_KEY=your_sendgrid_key
```

### Switch to Mailpit (Development)
```bash
EMAIL_PROVIDER=mailpit
SMTP_HOST=localhost
SMTP_PORT=1025
```

No code changes required.

## Troubleshooting

### Email not received?

1. **Check Unosend dashboard**: https://unosend.co/dashboard → Messages
2. **Verify API key**: Should start with `un_`
3. **Check sender email**: Must match configured `EMAIL_FROM_EMAIL`
4. **Verify domain** (production): Add and verify domain in Unosend dashboard
5. **Check rate limits**: Max 100 requests/minute

### 401 Unauthorized Error

❌ Cause: Invalid API key
✅ Solution:
```bash
# Generate new key in Unosend dashboard
export UNOSEND_API_KEY=un_new_key_here
```

### 422 Unprocessable Entity Error

❌ Cause: Invalid request format
✅ Solution: Ensure these are set:
```bash
EMAIL_FROM_EMAIL=noreply@yourdomain.com
EMAIL_FROM_NAME=RAIL
```

### Domain not verified (Production)

❌ Cause: Sending domain not verified
✅ Solution:
1. Add domain in Unosend dashboard
2. Add DNS records
3. Wait for verification
4. Use verified domain in `EMAIL_FROM_EMAIL`

## Email Headers

When sending, the service automatically includes:

```
From: RAIL <noreply@yourdomain.com>
Reply-To: support@yourdomain.com
Content-Type: text/html; charset=UTF-8
Authorization: Bearer un_xxxxxxxx
```

## Rate Limits

- **Limit**: 100 requests per minute
- **Per**: Organization
- **Retry**: Use exponential backoff
- **Headers**: `X-RateLimit-Limit`, `X-RateLimit-Remaining`, `X-RateLimit-Reset`

## API Endpoint

```
POST https://www.unosend.co/api/v1/emails
Authorization: Bearer un_xxxxxxxx
Content-Type: application/json

{
  "from": "RAIL <noreply@yourdomain.com>",
  "to": ["user@example.com"],
  "subject": "Welcome to RAIL",
  "html": "<h1>Welcome</h1>",
  "text": "Welcome",
  "reply_to": "support@yourdomain.com"
}
```

## Response

```json
{
  "id": "550e8400-e29b-41d4-a716-446655440000",
  "from": "RAIL <noreply@yourdomain.com>",
  "to": ["user@example.com"],
  "subject": "Welcome to RAIL",
  "created_at": "2024-01-15T10:30:00.000Z"
}
```

## Next Steps

1. ✅ Configure `.env` with Unosend API key
2. ✅ Test email sending with verification endpoint
3. ✅ Verify domain (for production)
4. ✅ Deploy to staging
5. ✅ Monitor email delivery in Unosend dashboard

## Resources

- **Unosend Docs**: https://docs.unosend.co
- **API Reference**: https://docs.unosend.co/api-reference/emails
- **Dashboard**: https://unosend.co/dashboard
- **Full Migration Guide**: `docs/UNOSEND_MIGRATION.md`
- **Integration Summary**: `UNOSEND_INTEGRATION_SUMMARY.md`

## Support

If you need help:
1. Check Unosend docs: https://docs.unosend.co
2. Review troubleshooting: `docs/UNOSEND_MIGRATION.md#Troubleshooting`
3. Check API status: https://unosend.co/status
