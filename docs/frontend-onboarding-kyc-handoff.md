# RAIL Frontend Handoff

## Signup, Sign-in, Onboarding, and KYC

This document describes the current backend contract for implementing the RAIL
frontend onboarding experience.

## 1. Canonical user journey

The recommended new-user journey is:

```text
Request signup OTP
  → Verify OTP
  → Receive access and refresh tokens
  → Complete the full onboarding profile
  → Backend creates the Bridge customer
  → Start hosted KYC verification
  → Complete Didit verification
  → Poll KYC status
  → Display unlocked capabilities
```

KYC must not be started before the full onboarding profile has been submitted
and a Bridge customer has been created.

## 2. Common request requirements

Base API path:

```text
/api/v1
```

Authenticated requests must include:

```http
Authorization: Bearer <accessToken>
X-Requested-With: RailApp
Content-Type: application/json
```

All state-changing requests require `X-Requested-With: RailApp`. Browser
clients may use the `X-CSRF-Token` response header instead.

The backend also returns an `X-Request-ID` header. Preserve it for support and
debugging logs.

## 3. Signup

### 3.1 Start signup

```http
POST /api/v1/auth/register
X-Requested-With: RailApp
```

Email signup:

```json
{
  "email": "user@example.com"
}
```

Phone signup:

```json
{
  "phone": "+2348012345678"
}
```

Rules:

- Send exactly one of `email` or `phone`.
- Email is normalized to lowercase by the backend.
- Phone must use E.164 format.
- No password is required at signup.

Success:

```text
202 Accepted
```

```json
{
  "message": "Registration received for user@example.com. Verification code queued for delivery.",
  "identifier": "user@example.com"
}
```

The user is not fully created until the OTP is successfully verified.

### 3.2 Verify signup OTP

```http
POST /api/v1/auth/verify
X-Requested-With: RailApp
```

Email:

```json
{
  "email": "user@example.com",
  "code": "123456"
}
```

Phone:

```json
{
  "phone": "+2348012345678",
  "code": "123456"
}
```

The code must be six digits.

New-user success returns tokens immediately:

```json
{
  "user": {
    "id": "uuid",
    "email": "user@example.com",
    "emailVerified": true,
    "phoneVerified": false,
    "onboardingStatus": "started",
    "kycStatus": ""
  },
  "accessToken": "jwt",
  "refreshToken": "jwt",
  "expiresAt": "2026-08-23T13:00:00Z",
  "sessionExpiresAt": "2026-09-22T12:00:00Z",
  "onboarding_status": "started",
  "next_step": "complete_onboarding"
}
```

Frontend behavior:

1. Store both tokens in secure platform storage.
2. Set the authenticated user in application state.
3. Navigate directly to onboarding.
4. Do not send the user back to signup after successful verification.

Verification errors:

| HTTP | Code | Frontend behavior |
|---|---|---|
| 400 | `INVALID_REQUEST` | Invalid request |
| 400 | `VALIDATION_ERROR` | Missing identifier |
| 401 | `VERIFICATION_FAILED` | Wrong or expired code |
| 404 | `USER_NOT_FOUND` | Account no longer exists |
| 500 | `USER_CREATION_FAILED` | Retry or contact support |

### 3.3 Resend OTP

```http
POST /api/v1/auth/resend-code
X-Requested-With: RailApp
```

```json
{
  "email": "user@example.com"
}
```

or:

```json
{
  "phone": "+2348012345678"
}
```

Success returns `202 Accepted`. Disable the resend control for 30–60 seconds
and honor `429 TOO_MANY_REQUESTS`.

## 4. Sign-in

### 4.1 Email OTP sign-in, recommended

Start:

```http
POST /api/v1/auth/email/start
X-Requested-With: RailApp
```

```json
{
  "email": "user@example.com"
}
```

The response is intentionally generic:

```json
{
  "message": "If that email has a RAIL account, a login code is on its way.",
  "identifier": "user@example.com"
}
```

Complete:

```http
POST /api/v1/auth/email/login
X-Requested-With: RailApp
```

```json
{
  "email": "user@example.com",
  "code": "123456"
}
```

Success returns the standard `user`, `accessToken`, `refreshToken`,
`expiresAt`, and `sessionExpiresAt` fields.

### 4.2 Password sign-in

Password login remains available for legacy or explicitly configured accounts:

```http
POST /api/v1/auth/login
X-Requested-With: RailApp
```

```json
{
  "email": "user@example.com",
  "password": "password"
}
```

Phone identifiers are also accepted. The new signup journey should not depend
on password login because signup does not set a password.

### 4.3 Passcode re-authentication

Passcode login requires a valid refresh token:

```http
POST /api/v1/auth/passcode-login
X-Requested-With: RailApp
```

```json
{
  "email": "user@example.com",
  "passcode": "1234",
  "refresh_token": "refresh-jwt"
}
```

Success:

```json
{
  "verified": true,
  "accessToken": "jwt",
  "refreshToken": "jwt",
  "expiresAt": "2026-08-23T13:00:00Z",
  "sessionExpiresAt": "2026-09-22T12:00:00Z",
  "passcodeSessionToken": "short-lived-token",
  "passcodeSessionExpiresAt": "2026-08-23T12:15:00Z"
}
```

Use `passcodeSessionToken` only for endpoints that explicitly require a
passcode session.

### 4.4 Refresh tokens

```http
POST /api/v1/auth/refresh
X-Requested-With: RailApp
```

```json
{
  "refreshToken": "refresh-jwt"
}
```

The backend rotates the token pair. On a normal `401`, refresh once, replace
both tokens, retry the original request once, and sign the user out if refresh
fails. Never retry indefinitely.

## 5. Full onboarding profile

After OTP verification, call:

```http
POST /api/v1/onboarding/complete
Authorization: Bearer <accessToken>
X-Requested-With: RailApp
```

Example:

```json
{
  "firstName": "John",
  "middleName": "Michael",
  "lastName": "Doe",
  "dateOfBirth": "1995-04-12T00:00:00Z",
  "country": "NG",
  "address": {
    "street": "12 Example Street",
    "city": "Lagos",
    "state": "Lagos",
    "postalCode": "100001",
    "country": "NG"
  },
  "phone": "+2348012345678",
  "signedAgreementId": "agreement-id"
}
```

Required fields:

- `firstName`
- `lastName`
- `dateOfBirth`
- `country`
- `address.street`
- `address.city`
- `address.postalCode`
- `address.country`

`password` is optional and should not be part of the default new-user UI.

Validation:

- User must be at least 18 years old.
- Date of birth cannot be more than 120 years ago.
- Profile countries use ISO alpha-2 codes: `NG`, `US`, `GB`.
- Phone must use E.164 format.

This endpoint creates the Bridge customer required for KYC.

Success includes:

```json
{
  "user_id": "uuid",
  "bridge_customer_id": "bridge-customer-id",
  "message": "Signup completed successfully. Complete KYC to unlock all features.",
  "next_steps": [
    "Complete KYC verification to unlock fiat deposits, cards, and investing",
    "You can deposit crypto immediately"
  ],
  "onboarding": {},
  "kyc": {}
}
```

### Important: basic-complete endpoint

```http
POST /api/v1/onboarding/basic-complete
```

This accepts only first and last name. It is a slim/legacy path and does not
create the Bridge customer required by the main KYC flow. Use
`/onboarding/complete` for the frontend KYC journey.

## 6. Profile and missing KYC fields

Fetch the current profile:

```http
GET /api/v1/users/me
Authorization: Bearer <accessToken>
```

Update profile data:

```http
PUT /api/v1/users/me
Authorization: Bearer <accessToken>
X-Requested-With: RailApp
```

Example:

```json
{
  "phone": "+2348012345678",
  "dateOfBirth": "1995-04-12T00:00:00Z",
  "addressStreet": "12 Example Street",
  "addressCity": "Lagos",
  "addressState": "Lagos",
  "addressPostalCode": "100001",
  "addressCountry": "NG"
}
```

Check missing KYC prerequisites:

```http
GET /api/v1/onboarding/kyc/missing-fields
Authorization: Bearer <accessToken>
```

Example:

```json
{
  "missingFields": [
    "date_of_birth",
    "address_street",
    "address_city",
    "phone"
  ],
  "startStep": "date_of_birth"
}
```

Use `missingFields` to resume the onboarding UI at the earliest incomplete
step.

## 7. Passcode setup

Check passcode state:

```http
GET /api/v1/security/passcode
Authorization: Bearer <accessToken>
```

Create a passcode:

```http
POST /api/v1/security/passcode
Authorization: Bearer <accessToken>
X-Requested-With: RailApp
```

```json
{
  "passcode": "1234",
  "confirmPasscode": "1234"
}
```

The passcode must be exactly four digits. Successful creation triggers wallet
provisioning asynchronously, so the UI should show progress and refresh state
later.

Possible errors:

```text
PASSCODE_EXISTS
PASSCODE_MISMATCH
INVALID_PASSCODE_FORMAT
PASSCODE_SETUP_FAILED
```

## 8. KYC status

```http
GET /api/v1/kyc/status
Authorization: Bearer <accessToken>
```

Example:

```json
{
  "user_id": "uuid",
  "status": "not_started",
  "overall_status": "not_started",
  "verified": false,
  "has_submitted": false,
  "requires_kyc": true,
  "kyc_tier": 1,
  "kyc_tier_name": "non_kyc",
  "bvn_verified": false,
  "nin_verified": false,
  "capabilities": {
    "can_deposit_crypto": true,
    "can_deposit_fiat": false,
    "can_use_card": false,
    "can_invest": false
  }
}
```

Possible overall statuses:

```text
not_started
pending
approved
rejected
```

Provider statuses may include:

```text
pending
processing
active
rejected
expired
in_review
```

Frontend mapping:

| Backend status | UI state |
|---|---|
| `not_started` | Start verification |
| `pending` | Verification submitted |
| `processing` | Verification processing |
| `approved` | Verification successful |
| `rejected` | Show rejection and retry |
| `expired` | Start a new session |

Bridge is authoritative for final approval. Do not mark the user fully
verified only because the identity provider SDK reports completion.

## 9. Recommended KYC flow: Didit

### 9.1 Start a Didit session

```http
POST /api/v1/kyc/didit/session
Authorization: Bearer <accessToken>
X-Requested-With: RailApp
```

Example for Nigeria:

```json
{
  "tax_id": "12345678901",
  "tax_id_type": "nin",
  "issuing_country": "NGA",
  "disclosures": {
    "is_control_person": false,
    "is_affiliated_exchange_or_finra": false,
    "is_politically_exposed": false,
    "immediate_family_exposed": false
  },
  "source_of_funds": "salary",
  "employment_status": "employed",
  "expected_monthly_payments_usd": "1000",
  "account_purpose": "personal"
}
```

KYC issuing countries use ISO alpha-3 codes:

```text
NGA, USA, GBR
```

The tax ID type must be valid for the issuing country. Examples:

```text
NGA → nin, bvn
USA → ssn, itin
GBR → nino, utr
```

Success:

```json
{
  "status": "pending",
  "session_id": "didit-session-id",
  "session_token": "didit-session-token",
  "url": "https://provider.example/session"
}
```

Frontend behavior:

1. Keep `session_id` in temporary screen state.
2. Launch the Didit SDK or returned URL.
3. Do not persist `session_token` in local storage.
4. Do not upload identity documents separately when using the hosted flow.
5. Wait for the provider flow to close.
6. Query backend KYC status; do not assume approval.

The backend sends tax ID data to Bridge over HTTPS and does not persist raw tax
IDs or document images locally.

### 9.2 Didit errors

| HTTP | Error | Frontend behavior |
|---|---|---|
| 400 | Unsupported tax ID | Select a valid tax ID type |
| 400 | Incomplete profile | Complete `missing_fields` |
| 400 | Already approved | Refresh KYC status |
| 400 | Complete signup first | Complete onboarding |
| 502 | Provider unavailable | Show retry |
| 503 | Provider not configured | Show temporary outage |

### 9.3 Poll status

After the hosted flow closes:

```http
GET /api/v1/kyc/status
Authorization: Bearer <accessToken>
```

Recommended polling:

- Poll every 3–5 seconds for the first minute.
- Stop after 2–3 minutes.
- If still pending, show a review screen.
- Refresh status when the user reopens the app.
- Do not poll indefinitely while the app is backgrounded.

## 10. Sumsub alternative

Sumsub is supported when selected by backend configuration. Do not implement
both hosted providers in the same frontend flow.

Start:

```http
POST /api/v1/kyc/sumsub/session
Authorization: Bearer <accessToken>
X-Requested-With: RailApp
```

```json
{
  "tax_id": "12345678901",
  "tax_id_type": "nin",
  "issuing_country": "NGA",
  "disclosures": {
    "is_control_person": false,
    "is_affiliated_exchange_or_finra": false,
    "is_politically_exposed": false,
    "immediate_family_exposed": false
  },
  "source_of_funds": "salary",
  "employment_status": "employed",
  "account_purpose": "personal"
}
```

Response:

```json
{
  "status": "pending",
  "applicant_id": "applicant-id",
  "token": "sumsub-websdk-token",
  "level_name": "basic-kyc"
}
```

Refresh an existing Sumsub token:

```http
GET /api/v1/kyc/sumsub/token
Authorization: Bearer <accessToken>
```

## 11. Legacy direct KYC upload

The backend also exposes:

```http
POST /api/v1/kyc/submit
```

This accepts tax IDs, document images, source-of-funds information, and
regulatory disclosures. It is a legacy/direct-upload path and is not
recommended for the new frontend when Didit or Sumsub hosted verification is
enabled.

## 12. Tiered capabilities

### Tier 1: `non_kyc`

The user has completed basic setup but not full identity verification.

```text
Limited crypto functionality
No cards
No investing
No advanced fiat capabilities
```

### Tier 2: `basic`

Start with:

```http
POST /api/v1/kyc/sprout/upgrade
Authorization: Bearer <accessToken>
X-Requested-With: RailApp
```

```json
{
  "phone": "+2348012345678",
  "date_of_birth": "1995-04-12",
  "bvn": "12345678901",
  "didit_session_id": "didit-session-id"
}
```

This Nigerian upgrade path validates BVN, reads the completed Didit session,
creates a Graph person, creates an NGN virtual account, submits identity data
to Bridge, and promotes the user to Tier 2.

Display the NGN account only when `ngn_account` is present in the response.

### Tier 3: `advanced`

Start with:

```http
POST /api/v1/kyc/bloom/upgrade
Authorization: Bearer <accessToken>
X-Requested-With: RailApp
```

```json
{
  "employment_status": "employed",
  "occupation": "Software Engineer",
  "source_of_funds": "salary",
  "expected_monthly_volume": "5000",
  "account_purpose": "personal banking",
  "proof_of_address_url": "https://storage.example/proof.pdf",
  "proof_of_address_type": "utility_bill"
}
```

Tier 3 unlocks USD/EUR accounts, cards, investing, tokenized investing, and
higher or unlimited limits.

## 13. Suggested frontend state machine

```typescript
type OnboardingState =
  | "signup"
  | "verify_otp"
  | "profile"
  | "passcode"
  | "kyc_start"
  | "kyc_in_progress"
  | "kyc_pending"
  | "kyc_rejected"
  | "kyc_approved"
  | "completed";
```

Routing rules:

```text
No token
  → signup or sign-in

Authenticated but verification incomplete
  → OTP verification

onboardingStatus = started
  → full profile

Profile complete and no passcode
  → create passcode

Bridge customer exists and KYC not submitted
  → KYC start

KYC status = pending or processing
  → KYC review screen

KYC status = rejected or expired
  → KYC retry screen

KYC status = approved
  → dashboard with unlocked capabilities
```

Always derive routing from `/users/me`, `/onboarding/kyc/missing-fields`, and
`/kyc/status`, not only from local navigation state.

## 14. Security and product requirements

1. Store tokens in secure platform storage.
2. Never persist OTPs, tax IDs, document images, or hosted KYC session tokens.
3. Never send tax IDs or identity documents to analytics or crash reporting.
4. Never log raw provider payloads or PII.
5. Do not treat SDK completion as KYC approval.
6. Do not start KYC before receiving a Bridge customer ID.
7. Use alpha-2 country codes for profiles and alpha-3 codes for KYC issuing country.
8. Treat wallet provisioning as asynchronous.
9. Handle `401` with one refresh attempt only.
10. Handle `429` with a visible cooldown.
11. Handle `502` and `503` as temporary provider/service failures.
12. On rejection, display a safe user-facing message and offer retry.
13. If the user closes hosted verification, resume or restart based on
    `/kyc/status`.
14. Phone-first signup has limitations in the main web onboarding path because
    full onboarding currently requires email verification. Email OTP signup is
    the safest canonical frontend flow.

## 15. Backend contract caveats to resolve before launch

- `/onboarding/basic-complete` is not sufficient for the full KYC journey
  because it does not create a Bridge customer.
- The main `/onboarding/complete` endpoint is the correct Bridge bootstrap path.
- Password fields still exist for compatibility, but the new signup flow should
  remain OTP-first and passwordless.
- The frontend should implement one hosted KYC provider, preferably Didit, based
  on the active backend configuration.
