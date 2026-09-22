# Miriam → Retirement Vault JSON Contract

The **Automated USD Retirement Plan** (the "vault") is exposed to Miriam through the same
agent API as the rest of the investment engine. This file is the exact contract a
`go_client`-style tool layer must implement. Money words only on the wire — no chain, token,
wallet or provider vocabulary anywhere.

Base path: `/api/v1/vault` (wrapped by the same delegated-agent `Bearer` token Miriam always
uses). All amounts are USD decimals parsed as strings, e.g. `"100.00"`.

## Tool surface at a glance

| Method | Path | Agent token | Description |
|---|---|---|---|
| GET | `/vault` | ✅ auto-run | Plan: principal, value, growth, unlock status, penalty now |
| GET | `/vault/strategies` | ✅ auto-run | The three tier choices a user can open |
| GET | `/vault/activity` | ✅ auto-run | Contribution history (contribution / skip / penalty) |
| POST | `/vault/withdraw/preview` | ✅ auto-run | Split / fee numbers **before anything moves** |
| POST | `/vault` | ❌ interactive | Open a plan (confirmed via standard confirmation flow) |
| PATCH | `/vault` | ❌ interactive | Change auto-save rule or retirement age |
| POST | `/vault/withdraw` | ❌ **always blocked** | Money out — app + passcode only |

Agent tokens can run every read and the preview (numbers only, changes nothing). The withdraw
endpoint additionally requires an interactive session AND a passcode-verified session, and it
is hard-gated at the middleware and handler (`RequireInteractiveSession` /
`RequirePasscodeSession`) so no tool definition can ever route around it. Do not ship a
withdraw tool; Miriam reads a preview and hands the user a deep link (`rail://authorize`)
for anything out.

Miriam proposing a withdrawal must stop at the preview numbers. There is **no** staging path
for withdrawals and no confirmation token bindable from the agent.

## Reads (auto-execute)

### GET /vault

```json
{
  "data": {
    "vault_id": "5a3d3b1e-...",
    "name": "USD Retirement Plan",
    "tier": "balanced",
    "tier_label": "Balanced Global Wealth",
    "status": "active",
    "principal_usd": "5000.00",
    "market_value_usd": "5234.12",
    "earnings_usd": "234.12",
    "unlock_date": "2055-01-01T00:00:00Z",
    "locked": true,
    "penalty_if_withdrawn_now_usd": "23.41",
    "penalty_rate": "0.10",
    "auto_contribution_pct": "5.00",
    "retirement_age": 60,
    "min_lock_years": 5,
    "as_of": "2026-09-20T08:00:00Z",
    "source": "provider",
    "stale": false
  }
}
```

- `tier` ∈ `steady` | `balanced` | `growth` → user-facing `tier_label` must come from the
  payload, never invented by Miriam.
- `locked=false` when the unlock date has passed or is unset — principal and growth are both
  free of penalty, so `penalty_if_withdrawn_now_usd` is `0.00`.
- `stale=true` means the value is a cached provider mark; still truthfully useful, but
  qualify a figure as "last we saw".

### GET /vault/strategies

```json
{ "data": { "plans": [
  { "tier": "steady",  "label": "Steady Dollar Income",    "description": "..." },
  { "tier": "balanced","label": "Balanced Global Wealth",   "description": "..." },
  { "tier": "growth",  "label": "Long-Term Global Growth",  "description": "..." }
] } }
```

If a tier is unconfigured (operator hasn't finished the checklist) it is simply absent from
this list — do not invent it.

### GET /vault/activity?limit=50

```json
{ "data": { "activity": [
  { "kind": "contribution", "at": "2026-09-01T00:00:00Z", "amount_usd": "500.00", "penalty_usd": "0.00" },
  { "kind": "skip",         "at": "2026-09-08T00:00:00Z", "amount_usd": "0.00",   "penalty_usd": "0.00" },
  { "kind": "penalty",      "at": "2026-08-15T00:00:00Z", "amount_usd": "50.00",  "penalty_usd": "5.00" }
] } }
```

`kind` ∈ `contribution` | `skip` | `insufficient` | `penalty` | `withdrawal`.

### POST /vault/withdraw/preview

Miriam's ONLY money-out capability. Returns the exact split and the fee for a hypothetical
amount, costing nothing and moving nothing:

Request:
```json
{ "amount_usd": "200.00" }
```

Response:
```json
{
  "data": {
    "vault_id": "5a3d3b1e-...",
    "enrollment_id": "8f11...",
    "gross_usd": "200.00",
    "principal_returned_usd": "180.00",
    "earnings_returned_usd": "20.00",
    "penalty_rate": "0.10",
    "penalty_usd": "2.00",
    "net_to_user_usd": "198.00",
    "locked": true,
    "unlock_date": "2055-01-01T00:00:00Z"
  }
}
```

Money language in replies: "…$198 would land, and $2 stays with Rail as the early-withdrawal
fee" — never "sells units", never "provider".

Errors (HTTP 422): `VAULT_INVALID_AMOUNT`, `VAULT_INSUFFICIENT_VALUE` ("that's more than your
plan is worth right now"), `VAULT_UNLOCK_UNKNOWN` (retirement age / DOB missing).

## Mutations (app-only)

These return the standard staged confirmation shape; Miriam must never call them, but it must
recognize the shape when the user confirms through the app and reports back.

### POST /vault (open a plan)

Request:
```json
{
  "tier": "balanced",
  "name": "USD Retirement Plan",
  "retirement_age": 60,
  "auto_contribution_pct": "5.00"
}
```

- HTTP **202** `STATUS AWAITING_CONFIRMATION`: vault is staged, nothing created yet.

```json
{
  "data": {
    "status": "AWAITING_CONFIRMATION",
    "confirmation": {
      "token": "vlt_...",
      "action": "open_retirement_vault",
      "payload_hash": "sha256...",
      "expires_at": "2026-09-20T08:15:00Z",
      "instruction": "Confirm in the app to open your USD Retirement Plan"
    }
  }
}
```

- Return it verbatim to the confirmation pipeline. The user's "yes" in the app binds the
  token to the exact payload hash; a tampered request refuses confirmation.
- HTTP **200** `status: COMPLETED` means the plan opened immediately (only when the user
  already had a pending confirmation approved — treat as done).
- HTTP **422** `VAULT_VALIDATION_FAILED` (missing date of birth needed for the unlock date),
  `VAULT_ALREADY_EXISTS` (409), `VAULT_PLAN_UNAVAILABLE` (503, tier unconfigured).

A confirmed open returns the `VaultView` object (same shape as GET /vault) under `data.vault`.
If the provider link then fails, the vault is rolled back and the user is told the plan
didn't open — no half-created plan is ever shown as active.

### PATCH /vault

```json
{ "auto_contribution_pct": "10.00", "retirement_age": 62 }
```

Both optional; at least one required. Returns the updated `VaultView` directly (this one is
not staged).

### POST /vault/withdraw

Request:
```json
{
  "amount_usd": "200.00",
  "destination_account": "solana:5eykt4UsFv8P8NJdTREpY1vzqKqZKvdp/..."
}
```

Not callable by agent tokens: expects a passcode-verified session; returns `VAULT_STEP_UP_REQUIRED`
(403) to any non-interactive caller. Single-use authorization inside the service means a
stale/replayed withdrawal can never land twice. The `destination_account` is optional and
defaults to the plan's Rail settlement account (where Rail keeps the 10% fee on early growth
before the remainder moves to the user).

## Errors (shared)

| HTTP | Code | Message |
|---|---|---|
| 503 | `VAULT_DISABLED` | "Retirement plans aren't available yet." |
| 404 | `VAULT_NOT_FOUND` | "You don't have a retirement plan yet." |
| 409 | `VAULT_ALREADY_EXISTS` | "You already have a retirement plan." |
| 503 | `VAULT_PLAN_UNAVAILABLE` | "That retirement plan isn't available yet." |
| 403 | `VAULT_STEP_UP_REQUIRED` | "Confirm in the app to take money out." |
| 403 | `VAULT_NOT_AUTHORIZED` | "We couldn't authorise that withdrawal from your plan." |
| 422 | `VAULT_UNLOCK_UNKNOWN` | "We need your date of birth to confirm when your plan unlocks." |
| 422 | `VAULT_INSUFFICIENT_VALUE` | "That's more than your plan is worth right now." |
| 422 | `VAULT_INVALID_AMOUNT` | "Enter an amount greater than zero." |
| 422 | `VAULT_UNAVAILABLE` | "We couldn't reconcile your plan, so nothing was withdrawn." |

All Truth Rules for figures apply: every number spoken by Miriam must trace to one of these
payloads or the user's own message. The vault never reports "pending lots" or internal
reconciliation fields to the user; `VAULT_UNAVAILABLE` is the only integrity failure wording.

The confirmation protocol shares the orchestrator's pending-action store — there is no
second vault-specific confirm mechanism for Miriam to learn.