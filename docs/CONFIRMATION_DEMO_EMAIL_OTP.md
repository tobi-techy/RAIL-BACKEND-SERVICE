# CONFIRMATION_DEMO_EMAIL_OTP (hackathon)

Skip Face ID for money confirmation cards and require a 6-digit email OTP instead.

## Env

```bash
CONFIRMATION_DEMO_EMAIL_OTP=true
CONFIRMATION_REQUIRE_DEVICE_SIGNATURE=false
```

Default is off (production Face ID path unchanged). Also needs existing `CONFIRMATION_TOKEN_SECRET`, Unosend/email, and Redis (for multi-replica OTP store).

## Flow

1. Stage card as today (`Create`).
2. `POST /confirm/:id/otp/send` or `POST /api/v1/confirmations/:id/otp/send` with token `t` (query or body).
3. User receives 6-digit email (reuses auth Unosend mailer).
4. `POST /confirm/:id/otp/approve` or `POST /api/v1/confirmations/:id/otp/approve` with `{ "t": "...", "code": "123456" }`.
5. On success, settle with assurance `email_otp`. Miriam settle via `confirm_id` is unchanged.

## Fail-closed

While the flag is on, Face ID / token-only `POST .../approve` is rejected. Wrong/expired/replayed codes fail. Flag off → Face ID path behaves as before; OTP endpoints stay registered but service rejects OTP when disabled.
