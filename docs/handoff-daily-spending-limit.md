# Handoff: Self-Imposed Daily Spending Limit (with fee to raise)

**Audience:** Product designer + frontend (React Native) engineer
**Status:** Backend complete, built, and unit-tested on `main`. Frontend not started.
**Owner (backend):** see git blame on `internal/domain/services/spendingcommitment/`

---

## 1. What this feature is

Users can set a **self-imposed daily cap** on how much money can leave their account in a day (a commitment device / self-control tool).

- Applies to **all outflows**: card authorizations, crypto withdrawals, fiat withdrawals, and P2P sends.
- The cap **resets every day at midnight UTC**.
- **Lowering the cap is free.** (Encourages good behavior.)
- **Raising the cap costs a flat fee** (default **$1.00**). This is the whole point: it adds friction to loosening your own guardrail.
- **Turning the cap off also costs the fee** (it's effectively "raising to infinity").
- Amounts internally are tracked in **USD cents**; NGN outflows are converted to USD before counting against the cap.

The core UX tension to design around: **raising the limit is a paid, confirm-gated action; lowering it is a free, instant action.**

---

## 2. User flows to design

### A. First-time setup
1. User opens Settings → "Daily spending limit" (currently OFF).
2. User picks an amount (slider + a "Custom" input).
3. Save. **No fee** for the first-ever set. Cap is now active.

### B. Lowering an existing cap
1. User drags slider down / enters a lower number.
2. Save. **No fee, instant.** Show a light confirmation ("Your daily limit is now $X").

### C. Raising an existing cap (the important one)
1. User drags slider up / enters a higher number, taps Save.
2. Backend rejects with **HTTP 409** and returns the fee.
3. Show a **confirmation sheet**: "Raising your daily limit to $X costs a $1.00 fee. Continue?"
4. On confirm, resend the request with `confirm_fee: true`.
5. If the user's spend balance can't cover the fee, backend returns **HTTP 402** → show "You need at least $1.00 in your spend balance to raise your limit."

### D. Turning the cap off
1. User toggles the feature off.
2. Same as raising: this **charges the fee** and requires confirmation (`confirm_fee=true`).

### E. Hitting the cap during a transaction
- When a card auth / withdrawal / P2P send would push today's total over the cap, that transaction is **declined** with a `commitment_exceeded` reason.
- Design an inline error state for the send/withdraw/card screens: "This would put you over your $X daily limit. $Y remaining today. Resets at midnight UTC." (Do **not** offer a one-tap "raise limit" inside the decline flow — that would defeat the commitment. Route them to Settings instead.)

---

## 3. API contract

Base path: `/api/v1`. All endpoints require the standard auth (Bearer session token, same as every other protected route). Responses wrap the payload in `{ "data": ... }`.

### GET `/api/v1/spending-commitment`
Returns current commitment + today's usage. Always 200 (returns an inactive status if none set).

```json
{
  "data": {
    "active": true,
    "daily_limit_cents": 50000,
    "used_cents": 40000,
    "remaining_cents": 10000,
    "currency": "USD",
    "resets_at": "2026-07-11T00:00:00Z",
    "increase_fee_cents": 100,
    "increase_count": 2
  }
}
```

Use this to render the current state, the "remaining today" meter, and to read `increase_fee_cents` for copy.

### PUT `/api/v1/spending-commitment`
Set or update the cap.

Request body:
```json
{
  "daily_limit_cents": 80000,
  "currency": "USD",     // optional, defaults to USD
  "confirm_fee": false   // set true to accept the increase fee
}
```

Responses:
| Status | Meaning | Body | Frontend action |
|--------|---------|------|-----------------|
| **200** | Set/lowered/created OK (or increase confirmed) | `{ "data": CommitmentStatus }` | Update UI to new state |
| **409** | Increase attempted without `confirm_fee` | `{ "error": "FEE_CONFIRMATION_REQUIRED", "message": "...", "increase_fee_cents": 100 }` | Show fee confirm sheet, then retry with `confirm_fee: true` |
| **402** | Confirmed increase but insufficient spend balance for fee | `{ "error": "INSUFFICIENT_FUNDS", "message": "..." }` | Show "not enough balance for fee" state |
| **400** | `daily_limit_cents` <= 0 | standard error envelope | Inline validation error |

### DELETE `/api/v1/spending-commitment?confirm_fee=true`
Turn the cap off. Charges the fee.
- Without `confirm_fee=true` → **409** `FEE_CONFIRMATION_REQUIRED` (same shape as PUT).
- With `confirm_fee=true` and enough balance → **200** with `active: false`.
- Insufficient balance → **402**.

---

## 4. Response field reference (`CommitmentStatusResponse`)

| Field | Type | Notes |
|-------|------|-------|
| `active` | bool | Whether a cap is currently enforced |
| `daily_limit_cents` | int64 | The cap, in USD cents (50000 = $500.00) |
| `used_cents` | int64 | Outflow already counted toward today's cap |
| `remaining_cents` | int64 | `daily_limit_cents - used_cents`, floored at 0 |
| `currency` | string | Always `USD` for now (display currency) |
| `resets_at` | RFC3339 string | Next midnight-UTC reset; may be empty if inactive |
| `increase_fee_cents` | int64 | Flat fee to raise/disable (default 100 = $1.00) |
| `increase_count` | int | How many times the user has raised the cap (useful for a subtle "you've raised this N times" nudge) |

**All money is integer cents.** Never send floats. Convert for display client-side (`cents / 100`).

---

## 5. Suggested frontend implementation

Match existing settings patterns in the RN app. Rough shape:

**API client** (`api/spendingCommitment.ts` or wherever your API layer lives):
```ts
export type CommitmentStatus = {
  active: boolean;
  dailyLimitCents: number;
  usedCents: number;
  remainingCents: number;
  currency: string;
  resetsAt?: string;
  increaseFeeCents: number;
  increaseCount: number;
};

getSpendingCommitment(): Promise<CommitmentStatus>;                 // GET
setSpendingCommitment(cents: number, confirmFee?: boolean): Promise<
  | { ok: true; status: CommitmentStatus }
  | { ok: false; needsFeeConfirm: true; feeCents: number }         // 409
  | { ok: false; insufficientFunds: true }                          // 402
>;
clearSpendingCommitment(confirmFee: boolean): Promise<...>;         // DELETE
```
(Map snake_case → camelCase in the client. Backend sends snake_case.)

**UI components:**
- `DailySpendingLimitScreen` / sheet: header, on/off toggle, amount **slider** with sensible min/max/step, a **"Custom"** numeric input, "remaining today" progress bar (`usedCents/dailyLimitCents`), and the reset time.
- `ConfirmIncreaseFeeSheet`: reads `feeCents` from the 409 (or from `increase_fee_cents` in GET), shows the fee, confirm/cancel. On confirm, re-call `set` with `confirmFee: true`.
- Settings row entry point that shows current state ("Daily limit: $500" / "Off").

**Client logic rules:**
- Detect increase vs decrease client-side (`newCents > currentCents`) to decide whether to pre-warn about the fee before even calling PUT — but **always trust the 409** as the source of truth (server enforces).
- Decrease and first-time set: call PUT directly, expect 200.
- Increase: call PUT; on 409 show the confirm sheet; on confirm re-PUT with `confirmFee: true`.
- Handle 402 with a distinct "add funds to cover the $1 fee" message.

---

## 6. Design deliverables requested

1. Settings entry row (active + inactive states).
2. Main limit screen: slider + Custom input + "remaining today" meter + reset-time label.
3. Increase-fee confirmation sheet (dynamic fee amount from API).
4. Insufficient-funds-for-fee state.
5. Turn-off confirmation (also fee-gated).
6. Inline "over daily limit" decline state for the Send / Withdraw / Card flows, including "$Y remaining today" and reset time — **without** an in-flow shortcut to raise the limit.

**Copy notes:** Frame the fee as friction the user *asked for* ("You set this guardrail. Raising it costs $1.00."). Keep lowering-the-cap celebratory and free.

---

## 7. Config / notes for backend coordination

- Fee is configurable via env `SPENDING_COMMITMENT_INCREASE_FEE` (dollars, e.g. `1.00`). Frontend should **never hardcode** the fee — always read `increase_fee_cents` from the API.
- Currency is USD-only today; `currency` is returned for forward-compat. Design in USD.
- Reset is midnight **UTC**, not local. Consider showing a relative "resets in Xh" instead of a raw timestamp to avoid timezone confusion.
- The cap counts *settled/authorized* outflow. A declined transaction does not consume the cap.

---

## 8. Backend reference (for the curious)

- Service: `internal/domain/services/spendingcommitment/service.go`
- Entities / DTOs: `internal/domain/entities/spending_commitment_entities.go`
- HTTP handler: `internal/api/handlers/investing/spending_commitment_handler.go`
- Routes: `internal/api/routes/routes.go` (`/spending-commitment` group)
- Migration: `migrations/256_spending_commitment.up.sql`
- Enforcement hook-ins: `card`, `withdrawal`, `p2p` services (`CheckOutflow` / `RecordOutflow`).
