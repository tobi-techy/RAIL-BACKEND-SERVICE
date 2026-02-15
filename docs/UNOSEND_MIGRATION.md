# Unosend Email Provider Migration

## Overview

This document outlines the migration from Mailpit, Resend, SendGrid, and Mailtrap email providers to **Unosend** as the primary email service provider.

## Migration Summary

### Replaced Providers
- ✅ Mailpit (development email testing)
- ✅ Resend (transactional emails)
- ✅ SendGrid (transactional emails)
- ✅ Mailtrap (development/testing)

### New Provider
- ✅ **Unosend** (production-ready transactional emails)

## Changes Made

### 1. Email Service Adapter (`internal/infrastructure/adapters/email_service.go`)

#### Constants Updated
- Added: `unosendAPIBaseURL = "https://www.unosend.co/api/v1"`
- Kept: `resendAPIBaseURL` for backward compatibility

#### Provider Initialization
Added Unosend case in `NewEmailService()`:
```go
case "unosend":
    if strings.TrimSpace(config.APIKey) == "" {
        return nil, fmt.Errorf("unosend api key is required")
    }
    httpClient = &http.Client{Timeout: 30 * time.Second}
```

#### Email Dispatch
Added Unosend dispatcher in `sendEmail()`:
```go
case "unosend":
    return e.sendViaUnosend(ctxWithTimeout, to, subject, htmlContent, textContent)
```

#### Implementation: `sendViaUnosend()`

**Endpoint**: `POST https://www.unosend.co/api/v1/emails`

**Request Format**:
```json
{
  "from": "RAIL <noreply@yourdomain.com>",
  "to": ["user@example.com"],
  "subject": "Email Subject",
  "html": "<h1>HTML Content</h1>",
  "text": "Plain text content",
  "reply_to": "support@yourdomain.com"
}
```

**Authentication**: Bearer token in Authorization header
- Format: `Authorization: Bearer un_xxxxxxxx`

**Features Supported**:
- ✅ HTML and plain text content
- ✅ Reply-to addresses
- ✅ Sender name formatting
- ✅ Context-based timeouts (30 seconds)
- ✅ Detailed error logging
- ✅ HTTP status code validation

### 2. Configuration (`internal/infrastructure/config/config.go`)

#### Environment Variables
Added Unosend configuration priority:
```go
if unosendAPIKey := os.Getenv("UNOSEND_API_KEY"); unosendAPIKey != "" {
    viper.Set("email.api_key", unosendAPIKey)
    viper.Set("email.provider", "unosend")
}
```

#### Config Struct
Updated provider documentation:
```go
Provider string `mapstructure:"provider"` // "unosend", "sendgrid", "resend", "mailpit", "smtp"
```

#### Default Provider
Changed fallback for development environments:
```go
if strings.TrimSpace(config.Email.Provider) == "" && isDevEnvironment(config.Environment) {
    config.Email.Provider = "unosend"
}
```

### 3. Environment Configuration (`.env.example`)

```env
# Email Service Configuration
EMAIL_PROVIDER=unosend

# Unosend Configuration (Recommended for Production)
UNOSEND_API_KEY=un_your_api_key
EMAIL_FROM_EMAIL=noreply@yourdomain.com
EMAIL_FROM_NAME=RAIL
EMAIL_REPLY_TO=support@yourdomain.com
```

Alternative providers remain commented out for reference.

## Setup Instructions

### 1. Get Unosend API Key

1. Visit [unosend.co/dashboard](https://unosend.co/dashboard)
2. Sign up or log in
3. Navigate to API keys section
4. Generate new API key (format: `un_xxxxxxxxxx`)

### 2. Configure Environment

```bash
# Development
export EMAIL_PROVIDER=unosend
export UNOSEND_API_KEY=un_your_api_key
export EMAIL_FROM_EMAIL=noreply@yourdomain.com
export EMAIL_FROM_NAME=RAIL
export EMAIL_REPLY_TO=support@yourdomain.com
```

### 3. Domain Verification

1. In Unosend dashboard, add your sending domain
2. Verify DNS records (DKIM, SPF, DMARC)
3. Once verified, use the domain in `EMAIL_FROM_EMAIL`

### 4. Test Email Sending

```bash
# Run the application
go run ./cmd/main.go

# Test email sending (example via API)
curl -X POST http://localhost:8080/api/v1/auth/verify \
  -H "Content-Type: application/json" \
  -d '{"email": "test@example.com"}'
```

## API Specifications

### Unosend API Reference

**Base URL**: `https://www.unosend.co/api/v1`

**Authentication**:
- Header: `Authorization: Bearer un_xxxxxxxx`
- Content-Type: `application/json`

**Rate Limits**:
- 100 requests per minute per organization
- Returns rate limit headers: `X-RateLimit-Limit`, `X-RateLimit-Remaining`, `X-RateLimit-Reset`

**HTTP Status Codes**:
- `200`: Successful request
- `400`: Invalid request parameters
- `401`: Missing or invalid API key
- `403`: Insufficient permissions
- `422`: Unprocessable entity
- `429`: Rate limit exceeded
- `500`: Internal server error

**Email Endpoint**:
```
POST /v1/emails
```

**Request Body**:
```json
{
  "from": "string (required)",          // Email or "Name <email>"
  "to": "string | string[] (required)", // Recipient(s)
  "subject": "string (required)",       // Email subject
  "html": "string (optional)",          // HTML content
  "text": "string (optional)",           // Plain text content
  "cc": "string | string[] (optional)", // CC recipients
  "bcc": "string | string[] (optional)",// BCC recipients
  "reply_to": "string (optional)",      // Reply-to address
  "tags": "object (optional)"           // Custom tags for analytics
}
```

**Response**:
```json
{
  "id": "uuid",
  "from": "string",
  "to": ["string"],
  "subject": "string",
  "created_at": "ISO8601 timestamp"
}
```

## Backward Compatibility

The implementation maintains full backward compatibility:
- All previous email providers (SendGrid, Resend, Mailpit, Mailtrap) remain functional
- Provider selection is determined by environment variables
- Switching providers requires only environment variable changes
- No database migrations required

## Testing

### Unit Tests
The email service interface tests remain unchanged. All providers implement the same interface.

### Integration Tests
Existing integration tests that use email functionality will work with Unosend:
- Verification code emails
- KYC status emails
- Welcome emails
- Login alert emails

### Development Testing
For local development:
1. Use real Unosend API with test API key
2. Or switch back to Mailpit for quick local testing:
   ```bash
   export EMAIL_PROVIDER=mailpit
   export SMTP_HOST=localhost
   export SMTP_PORT=1025
   ```

## Performance Metrics

**Unosend Features**:
- ✅ Global infrastructure for fast delivery
- ✅ 99%+ inbox placement rate
- ✅ DKIM/SPF/DMARC authentication
- ✅ Real-time webhook notifications
- ✅ Email tracking and analytics
- ✅ Simple REST API

## Migration Checklist

- [x] Update `email_service.go` with Unosend provider
- [x] Add Unosend initialization logic
- [x] Implement `sendViaUnosend()` method
- [x] Update configuration (`config.go`)
- [x] Add environment variable support (`UNOSEND_API_KEY`)
- [x] Update `.env.example` with Unosend defaults
- [x] Maintain backward compatibility with other providers
- [x] Document API specifications
- [x] Create migration guide

## Troubleshooting

### Common Issues

**Issue**: `401 Unauthorized`
- **Cause**: Invalid or missing API key
- **Solution**: Verify `UNOSEND_API_KEY` is set correctly with `un_` prefix

**Issue**: `403 Forbidden`
- **Cause**: API key lacks permissions
- **Solution**: Regenerate API key in Unosend dashboard

**Issue**: `422 Unprocessable Entity`
- **Cause**: Invalid request body
- **Solution**: Check email format, ensure `from`, `to`, `subject` are provided

**Issue**: `429 Rate Limited`
- **Cause**: Exceeded 100 requests/minute
- **Solution**: Implement exponential backoff retry logic

**Issue**: Domain not verified
- **Cause**: Sending domain not verified in Unosend
- **Solution**: Add and verify domain in Unosend dashboard

## Support

- **Unosend Docs**: https://docs.unosend.co
- **Unosend Dashboard**: https://unosend.co/dashboard
- **API Reference**: https://docs.unosend.co/api-reference

## Rollback Instructions

If you need to revert to a previous email provider:

```bash
# Switch to Resend
export EMAIL_PROVIDER=resend
export RESEND_API_KEY=your_resend_key

# Switch to Mailpit (development)
export EMAIL_PROVIDER=mailpit
export SMTP_HOST=localhost
export SMTP_PORT=1025

# Switch to SendGrid
export EMAIL_PROVIDER=sendgrid
export EMAIL_API_KEY=your_sendgrid_key
```

No code changes required - just environment variables.

## Future Enhancements

Potential improvements for Unosend integration:
1. [ ] Implement batch email sending (`POST /v1/emails/batch`)
2. [ ] Add webhook support for email events
3. [ ] Implement email template management
4. [ ] Add click tracking and analytics
5. [ ] Support scheduled email sending
6. [ ] Attachment support
