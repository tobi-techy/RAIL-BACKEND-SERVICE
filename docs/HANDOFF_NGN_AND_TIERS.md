# Handoff: Naira (NGN) Rail + 3-Tier KYC System

**Audience:** Product Designer + Frontend/Mobile Developer
**Owner (backend):** Rail backend team
**Status:** Backend implemented and tested. This doc is the single source of truth for building the client experience.

---

## 1. TL;DR — What we're building

Two connected things:

1. **Naira rail** — Nigerian users get a **named NGN bank account** (their own account number at a real bank). Anyone can send Naira into it. The moment money lands, it auto-converts NGN → USDC and runs Rail's standard **70% spend / 30% stash** split. No user configuration.
2. **3-tier KYC ladder** — a progressive verification system. Each tier unlocks more (higher limits, more products). Users climb only as far as they need. The whole point is to **reduce drop-off**: don't ask for heavy KYC before the user needs it.

The client's job: show users **where they are**, **what they can do now**, and **the single next step** to unlock what they want.

---

## 2. The Tier Ladder (the mental model)

| Tier | Name (`kyc_tier_name`) | `kyc_tier` (int) | How you get here | What it unlocks |
|------|------------------------|------------------|------------------|-----------------|
| 0 | `unverified` | `0` | Account not set up | **Nothing.** All transactions blocked. |
| 1 | `non_kyc` | `1` | Sign up (wallet created, no KYC) | Crypto deposit + **NGN send/receive (limited)** |
| 2 | `basic` | `2` | **BVN + NIN** verified (via Graph) | **Named NGN virtual account** + higher NGN limits |
| 3 | `advanced` | `3` | **Bridge KYC** (full identity) | USD virtual account, **cards**, **investing** (incl. tokenized) |

Key rules the client must respect:

- **`kyc_tier` (the integer) is authoritative.** Always drive UI off the numbers/booleans the API returns, never off inferred state.
- Tiers are a **ladder** — a Tier 3 user has everything Tier 1 and 2 have.
- **Naira does NOT require Tier 3.** A Nigerian user reaches full Naira functionality at **Tier 2** (BVN + NIN). Do not send them into Bridge/USD KYC to use Naira.
- **Tier 3 is USD/cards/investing.** Only push a user to Bridge KYC when they want those.
- Tiers can go **down**. If Bridge rejects a user or an account is blocked/suspended, the API will return a lower tier. Re-read status after any KYC event; never cache "advanced" forever.

---

## 3. Capability flags — build the UI off these, not off tier numbers

Every KYC status response includes a `tier_capabilities` object. **Gate every feature/button on these booleans.** This is what keeps the UI correct even as tiers shift.

```jsonc
"tier_capabilities": {
  "tier": 2,                    // numeric tier
  "can_deposit_crypto": true,   // tier >= 1
  "can_receive_ngn": true,      // tier >= 2  → show/enable the NGN account screen
  "can_deposit_fiat_usd": false,// tier >= 3  → USD virtual account
  "can_use_card": false,        // tier >= 3  → cards
  "can_invest": false,          // tier >= 3  → brokerage investing
  "can_invest_tokenized": false // tier >= 3  → tokenized-asset investing
}
```

Also on the response (Naira-specific verification signals):

```jsonc
"bvn_verified": true,   // Graph BVN done
"nin_verified": false,  // Graph NIN done — needed (with BVN) to reach Tier 2
"supported_tax_id_type": "nin" // country-specific ID type to ask for (e.g. ssn, nino, nin)
```

**Design rule:** if `can_receive_ngn` is `false` but the user is Nigerian, the "next step" is **BVN + NIN**, not Bridge. If `can_use_card`/`can_invest` is `false`, the next step is **Bridge KYC**.

---

## 4. API Contracts

Base path: `/api/v1`. All endpoints below require auth (Bearer token) unless noted.

### 4.1 Read tier + capabilities — `GET /kyc/status`

The screen-driver. Poll/refresh this after any KYC action. Full response shape:

```jsonc
{
  "user_id": "uuid",
  "status": "pending",              // provider-level status
  "verified": false,
  "overall_status": "pending",      // pending | approved | rejected | not_started
  "kyc_tier": 2,                    // ← authoritative tier (int)
  "kyc_tier_name": "basic",         // unverified | non_kyc | basic | advanced
  "tier_capabilities": { /* see §3 */ },
  "bvn_verified": true,
  "nin_verified": false,
  "supported_tax_id_type": "nin",
  "next_steps": ["..."],            // human-readable, safe to display
  "required_for": ["..."],
  "rejection_reason": "string|null",
  "bridge": { "status": "...", "submitted_at": "...", "approved_at": "...", "rejection_reasons": ["..."] },
  "alpaca": { "status": "..." }
}
```

### 4.2 Tier 3 (USD/cards/investing) — Bridge KYC

- `GET /kyc/bridge/link` → returns the hosted Bridge KYC link to open in a webview/browser.
- `GET /kyc/bridge/status` → poll Bridge KYC progress.
- `POST /kyc/didit/session` → create a Didit identity/liveness session (identity spine). Rate-limited (3/min).
- `GET /kyc/status` reflects the resulting tier once Bridge approves (webhook-driven; there is also server-side self-heal, so status will correct itself even if a webhook is missed).

### 4.3 Naira account — provision (Tier 2 gate)

**`POST /funding/ngn/virtual-account`** — create the user's named NGN account. Rate-limited (5/min).

Request body:
```jsonc
{
  "bvn": "12345678901",          // REQUIRED — transient, used to create Graph person then discarded
  "id_number": "12345678901",    // REQUIRED — the ID value
  "id_type": "nin",              // defaults to "nin" if omitted (NIN preferred). also: voters_card, drivers_license, passport
  "id_document_url": "https://...", // optional
  "employment_status": "employed", // optional
  "occupation": "...",             // optional
  "source_of_funds": "salary",     // optional
  "primary_purpose": "savings"     // optional
}
```

Success `200`:
```jsonc
{ "virtual_account": { /* see §4.5 VirtualAccount */ } }
```

Error cases the client must handle:
- `400 { "error": "bvn_nin_required", "message": "..." }` → user is missing BVN/NIN; route them to collect both.
- `400 { "error": "Invalid request format", ... }` → validation (missing bvn/id_number).
- `500 { "error_code": "NGN_ACCOUNT_ERROR" }` → transient; safe to retry (provisioning is idempotent — a duplicate returns the existing account).

> Security: **never store or log the raw BVN / ID number** on the client. Send once over HTTPS; the backend only persists a last-4 + a verified-at flag.

### 4.4 Naira account — fetch — `GET /funding/ngn/virtual-account`

- `200 { "virtual_account": { ... } }` — show the account details (this is what users share to receive Naira).
- `404 { "error": "not_found", "message": "No NGN virtual account. Complete verification to get one." }` — user hasn't provisioned yet → show the Tier 2 upsell / provisioning flow.

### 4.5 `VirtualAccount` object (what to render on the "Receive Naira" screen)

```jsonc
{
  "id": "uuid",
  "provider": "graph",
  "account_number": "0123456789",     // ← the shareable NGN account number
  "bank_name": "…",
  "bank_code": "…",
  "beneficiary_name": "Ada NGN",      // account holder name shown at the bank
  "currency": "NGN",
  "status": "active",                 // pending | active | closed | failed
  "created_at": "…", "updated_at": "…"
}
```

Only surface `account_number`, `bank_name`, `beneficiary_name` (and a copy button) to the user. `status: "pending"` → show "account is being set up"; `active` → ready to receive.

### 4.6 Deposits into Naira (what happens after money arrives)

The user does nothing — a bank transfer into their NGN account triggers a Graph webhook → backend converts NGN→USDC → 70/30 split → funds appear in spend/stash. Client just needs to:
- Reflect new balance via existing `GET /balances`.
- Show the deposit in `GET /activity`.

There is **no client call** to trigger a deposit. Deposits are provider-webhook driven and idempotent.

---

## 5. Limits (show these; enforce server-side)

The backend enforces limits; the client should **display** them and pre-validate to avoid failed attempts. Values below are current.

### NGN limits

| Tier | Min deposit | Daily deposit | Monthly deposit | Daily withdrawal | Monthly withdrawal |
|------|-------------|---------------|-----------------|------------------|--------------------|
| 1 `non_kyc` | ₦500 | ₦50,000 | ₦200,000 | ₦50,000 | ₦200,000 |
| 2 `basic` | ₦500 | ₦2,000,000 | ₦10,000,000 | ₦1,000,000 | ₦5,000,000 |
| 3 `advanced` | ₦500 | ₦10,000,000 | ₦50,000,000 | ₦5,000,000 | ₦25,000,000 |

(Tier 1 NGN is a single transfer cap of ₦50,000/txn.)

### USD limits (for context / USD screens)

| Tier | Per-txn min | Daily deposit | Monthly deposit | Daily withdrawal | Monthly withdrawal |
|------|-------------|---------------|-----------------|------------------|--------------------|
| 1 `non_kyc` | $1 | $100 | $500 | $100 | $500 |
| 2 `basic` | $1 | $5,000 | $25,000 | $2,500 | $25,000 |
| 3 `advanced` | $1 | $50,000 | $250,000 | $10,000 | $150,000 |

> Treat these as configurable server values — don't hardcode. Ideally the client reads limits from the API/limits endpoint if available, or ships them as a remote-config table. Confirm the exact limits-fetch endpoint with backend before wiring.

---

## 6. Designer — screens & states to produce

**A. Tier / "Account level" hub**
- Visual ladder of the 4 states (unverified → non_kyc → basic → advanced) with the current one highlighted, driven by `kyc_tier`.
- Per-tier "what you can do now" list, driven by `tier_capabilities` booleans (§3).
- One clear **primary CTA = the next unlock step** (see §7 flows).

**B. Naira onboarding (Tier 2)**
- Explainer: "Get your own Naira account number."
- BVN + NIN collection form. Show `supported_tax_id_type` label. Sensitive-input treatment (masked, no screenshots hint).
- States: collecting → submitting → success (show account) / `bvn_nin_required` error / retriable error.

**C. Receive Naira**
- Big account number + copy, bank name, beneficiary name.
- `pending` vs `active` states. `404` = not-yet-provisioned empty state with CTA into flow B.

**D. Tier 3 upsell (USD/cards/investing)**
- Only shown when the user taps a Tier-3-gated feature while `can_use_card`/`can_invest`/`can_deposit_fiat_usd` is false.
- Launches Bridge KYC (webview) → progress → return.

**E. Downgrade / blocked states**
- Rejection: show `rejection_reason`, offer retry path.
- Account block/suspend (tier drops to `unverified`): clear messaging + support contact.

**F. Limits display**
- Show current tier's limits on deposit/withdraw screens; "raise your limits" nudge that maps to the next tier.

---

## 7. Developer — the two key flows

### Flow 1 — Nigerian user wants a Naira account (Tier 1 → Tier 2)
```
GET /kyc/status
  → can_receive_ngn == false?  → show Naira onboarding
Collect BVN + NIN
POST /funding/ngn/virtual-account { bvn, id_number, id_type:"nin", ... }
  → 200: show account_number / bank_name / beneficiary_name
  → 400 bvn_nin_required: re-collect missing field
  → 500: retry (idempotent)
GET /kyc/status  → confirm kyc_tier == 2, can_receive_ngn == true
GET /funding/ngn/virtual-account  → render Receive-Naira screen
```

### Flow 2 — user wants cards / USD / investing (→ Tier 3)
```
Feature tap while can_use_card / can_invest / can_deposit_fiat_usd == false
GET /kyc/bridge/link  → open hosted link (webview)
(optional) POST /kyc/didit/session for liveness
Poll GET /kyc/bridge/status  (and/or GET /kyc/status)
On approval: kyc_tier == 3, capabilities flip true → unlock features
```

### Client-side gating helper (pseudo)
```ts
const s = await api.get('/kyc/status');
const cap = s.tier_capabilities;
showNairaAccount   = cap.can_receive_ngn;      // Tier 2+
showCards          = cap.can_use_card;         // Tier 3
showInvesting      = cap.can_invest;           // Tier 3
showUsdAccount     = cap.can_deposit_fiat_usd; // Tier 3
// Next-step routing:
if (userIsNigerian && !cap.can_receive_ngn) nextStep = 'NGN_BVN_NIN';
else if (wantsCardsOrInvesting && !cap.can_use_card) nextStep = 'BRIDGE_KYC';
```

---

## 8. Rules, gotchas & non-negotiables

1. **Drive UI off `tier_capabilities` + `bvn_verified`/`nin_verified`, not off assumptions.** Tier math lives on the server.
2. **Re-fetch `/kyc/status` after every KYC action and on app foreground.** Tiers can move up (approval, self-heal) or down (rejection, block).
3. **Naira = Tier 2, not Tier 3.** Never gate Naira behind Bridge/USD KYC.
4. **Never persist/log raw BVN or ID numbers** on the client. One-shot over HTTPS.
5. **Provisioning is idempotent** — safe to retry `POST /funding/ngn/virtual-account`; a duplicate returns the existing account.
6. **Deposits are webhook-driven** — no client trigger; reflect via `/balances` and `/activity`.
7. **70/30 split is automatic and non-configurable** — do not build any split UI.
8. **Limits are server-enforced** — display them, pre-validate, but expect the server to be the final word.
9. Handle `unverified` (tier 0) explicitly: all money actions blocked; show setup path.

---

## 9. Open items to confirm with backend before build

- Exact **limits-fetch endpoint** (if the client should read limits live vs. ship a config table).
- Whether a **push/websocket event** exists for tier changes, or the client should poll `/kyc/status`.
- Copy for `next_steps` — confirm whether backend strings are display-ready or the client owns copy.
- Final list of accepted `id_type` values for `supported_tax_id_type` per country.

---

### Appendix — enums quick reference

- `kyc_tier`: `0` unverified · `1` non_kyc · `2` basic · `3` advanced
- `kyc_tier_name`: `unverified | non_kyc | basic | advanced`
- VirtualAccount `status`: `pending | active | closed | failed`
- `overall_status`: `not_started | pending | approved | rejected`
- Provision errors: `bvn_nin_required` (400), `NGN_ACCOUNT_ERROR` (500), `not_found` (404 on GET)
