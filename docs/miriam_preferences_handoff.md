# Handoff — Miriam Preferences (Settings + iMessage Integration)

**Audience:** Product Designer + Backend Developer
**Status:** Backend delivery mechanism shipped; user-facing controls not yet built
**Owner:** (assign)
**Last updated:** 2026-07-10

---

## 1. One-paragraph summary

Miriam now delivers **all** of her proactive output through iMessage only — daily briefings, risk alerts, nudges, mandate receipts, and prediction follow-ups. Delivery is governed by a backend `ProactiveGuard` (quiet hours, a per-user daily message cap, and per-user timezone). **Today those values are hardcoded** (Lagos default, cap 6, quiet 22:00–07:00, briefing at local 09:00). This feature turns those into **user-editable preferences** — a "Miriam" settings surface in-app plus natural-language controls over iMessage — and wires them back into the guard and the daily-briefing worker so user choices actually take effect.

The goal is the tester's north star: Miriam should feel like *discreet infrastructure, not a notification machine*. These settings are how a user tunes that discretion without her ever feeling broken or noisy.

---

## 2. What already exists (the backend truth to build against)

| Capability | Where | Notes |
|---|---|---|
| Single iMessage delivery path | `internal/infrastructure/platform/bridge_dispatcher.go` | `BridgeDispatcher.deliver()` resolves the user's iMessage thread and publishes. No-ops if the user has no linked thread. `composeMessage()` folds title+body into one bubble. |
| Quiet hours + daily cap + timezone | `internal/infrastructure/platform/proactive_guard.go` | `ProactiveGuard.Allow(ctx, userID, critical)`. Redis-backed daily counter, fails open, `critical=true` bypasses both. `UserTimezoneResolver` maps country → IANA tz (`timezoneForCountry`). |
| Guard construction (hardcoded) | `internal/infrastructure/di/container.go` (~L1874) | `NewProactiveGuard(redis, tzResolver, "Africa/Lagos", 6, 22, 7, log)` then `bridgeDispatcher.SetGuard(guard)`. **This is the seam the settings feature plugs into.** |
| Daily briefing worker | `internal/workers/daily_pulse/worker.go` | Runs for **all active users**, fires once per user in their **local 09:00** window (`dueForDailyPulse`). Delivers via the dispatcher (iMessage). |
| Prediction loop-closing | `internal/domain/services/miriam/intelligence_orchestrator.go` (~L180) + `outcome_tracker.go` (`LoopClosingMessage`) | One "I flagged X, here's how it turned out" line per sweep. |
| Account linking (iMessage) | `POST /api/v1/platform/link` → one-time token; bind happens when the user **texts the token back** (sender-verified). | No insecure confirm endpoint. This is the "connect channel" flow the settings screen surfaces. |
| Autonomy / control level | `autopilotControlLevelAdapter` (backed by `MemoryService`); mandates in the decision engine | Control levels gate how much Miriam does silently (e.g. `"full"`). This is the autonomy ladder the settings expose. |
| Existing notification prefs | `repositories.NewNotificationPreferenceRepository` (`notification_preferences`) | Consider extending vs. a new `miriam_preferences` table — see Open Decision D1. |

**Key constraint already enforced:** money-safety-critical messages (mandate receipts, fund-move confirmations) pass `critical=true` and bypass the cap/quiet hours. The settings UI must respect and disclose this — a user cannot silence "we moved your money."

---

## 3. The feature: "Miriam Preferences"

A dedicated settings surface (not buried in generic app notifications) with a matching set of over-iMessage commands. Two coordinated deliverables:

- **In-app settings screen** — the full control panel.
- **iMessage-native controls** — quick natural-language edits ("quiet until Monday", "less chatty", "stop the daily brief") so a user never has to leave the thread to tune her.

Both read/write the same preference record.

---

## 4. Settings inventory (source of truth for both design & dev)

| # | Setting | Control type | Default | Backend field | Critical-bypass? |
|---|---|---|---|---|---|
| 1 | **iMessage connection** | Connect / disconnect (link-token flow) | Not connected | `imessage_linked` (derived from thread) | — |
| 2 | **Daily briefing** | Toggle | On | `briefing_enabled` | No — fully user-controlled |
| 3 | **Briefing time** | Time picker (local) | 09:00 | `briefing_hour` (0–23) | — |
| 4 | **Timezone** | Auto-detected + override | From country → else Africa/Lagos | `timezone` (IANA, nullable) | — |
| 5 | **Quiet hours** | Toggle + start/end | On, 22:00–07:00 | `quiet_enabled`, `quiet_start`, `quiet_end` | Critical alerts still send |
| 6 | **How chatty** (cadence) | 3-way segmented: Minimal / Balanced / Proactive | Balanced | `daily_cap` (2 / 6 / 12) | — |
| 7 | **What she pings about** | Multi-toggle: Briefings · Risk alerts · Wins & nudges · Follow-ups | All on | `allow_briefings`, `allow_risk`, `allow_nudges`, `allow_followups` | Risk = money-safety stays on; disclose |
| 8 | **Autonomy** | 3-way: Observe / Suggest / Act | Suggest | `autonomy_level` | Maps to control level + mandate gating |
| 9 | **Humor / roasting** | Toggle | Off (roasting opt-in) | `humor_roasting` | — |

Notes:
- **#6 "How chatty"** is deliberately a plain-language 3-way, not a number stepper. It maps to `daily_cap` under the hood. Users think in vibes, not message counts.
- **#8 Autonomy** is the highest-stakes control. "Act" implies silent money moves within mandates. Design must make the trust ladder legible (see §5 copy).
- Fund moves **always** require in-app Face ID (`rail://authorize`) regardless of autonomy — messaging can't do passcode step-up. Autonomy governs *reservations/nudges/sweeps within mandate*, not raw withdrawals.

---

## 5. Designer deliverables

### 5.1 Screens & states
1. **Miriam Preferences (main screen)** — grouped sections mirroring §4: *Connection · Daily briefing · Quiet hours · Cadence · What she pings about · Autonomy · Personality.*
2. **Connection sub-flow** — three states:
   - *Not connected*: explainer + "Connect iMessage" → shows the one-time code and the instruction "text this to Miriam." (Backend: `POST /api/v1/platform/link`.)
   - *Pending*: "Waiting for your text…" (binds when user texts the token back).
   - *Connected*: shows the linked handle + "Disconnect."
3. **Empty/disconnected settings state** — settings remain editable, with a persistent banner: "Connect iMessage to start hearing from Miriam." (Preferences persist so they're ready on connect.)
4. **Autonomy explainer** — a short 3-tier visual (Observe = watches & reports; Suggest = asks before acting; Act = handles it within your rules, always tells you). Include the "money moves still need Face ID" reassurance.
5. **Confirmation & disclosure micro-states** — when a user disables Risk alerts, show the disclosure: "You'll still hear if your money is genuinely at risk. Everything else goes quiet."

### 5.2 Copy & tone (critical — this is a persona surface)
Miriam's voice per the shipped persona (`system_prompt_v2.go`): warm, competent, dry, reads-the-room, **no emojis, no puns**, roasting only if opted in. Settings copy should sound like *her*, not like an OS settings pane.

- Section headers can be plain; helper text should carry the voice.
- Example — Quiet hours helper: *"I'll hold anything non-urgent until morning. If your money's actually on fire, I'll still say so."*
- Example — Cadence "Minimal": *"I'll only speak up when it matters. You won't hear from me most days."*
- Deliver a **copy deck** for all 9 settings (label + helper + any disclosure), plus the iMessage command confirmations in §6.3.

### 5.3 Components / design system
- Segmented 3-way control (used by Cadence + Autonomy).
- Time picker constrained to local tz with the resolved zone shown ("Lagos time").
- Multi-toggle list with per-row disclosure affordance.
- Disclosure/reassurance inline banner pattern (reused across critical-bypass explanations).

### 5.4 What design does *not* decide alone (flag to PM)
- Whether cap-suppressed messages get a "held N updates" summary or are dropped silently (Open Decision D2).
- Exact cadence→cap numbers (proposed 2/6/12; confirm with data).

---

## 6. Developer deliverables

### 6.1 Data model
Recommend a dedicated table (see D1 for the fold-in alternative):

```sql
-- migrations/NNN_miriam_preferences.up.sql   (NNN = next sequential number)
CREATE TABLE miriam_preferences (
    user_id           UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    briefing_enabled  BOOLEAN NOT NULL DEFAULT TRUE,
    briefing_hour     SMALLINT NOT NULL DEFAULT 9  CHECK (briefing_hour BETWEEN 0 AND 23),
    timezone          TEXT,                          -- IANA; NULL => derive from country
    quiet_enabled     BOOLEAN NOT NULL DEFAULT TRUE,
    quiet_start       SMALLINT NOT NULL DEFAULT 22 CHECK (quiet_start BETWEEN 0 AND 23),
    quiet_end         SMALLINT NOT NULL DEFAULT 7  CHECK (quiet_end   BETWEEN 0 AND 23),
    daily_cap         SMALLINT NOT NULL DEFAULT 6  CHECK (daily_cap BETWEEN 0 AND 50),
    allow_briefings   BOOLEAN NOT NULL DEFAULT TRUE,
    allow_risk        BOOLEAN NOT NULL DEFAULT TRUE,
    allow_nudges      BOOLEAN NOT NULL DEFAULT TRUE,
    allow_followups   BOOLEAN NOT NULL DEFAULT TRUE,
    autonomy_level    TEXT NOT NULL DEFAULT 'suggest' CHECK (autonomy_level IN ('observe','suggest','act')),
    humor_roasting    BOOLEAN NOT NULL DEFAULT FALSE,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);
```
Ship the paired `.down.sql`. Reads must return **defaults when no row exists** (don't force a row on signup).

### 6.2 API contract
```
GET /api/v1/miriam/preferences        -> 200 MiriamPreferences (defaults if unset)
PUT /api/v1/miriam/preferences        -> 200 MiriamPreferences (full or partial upsert)
GET /api/v1/miriam/connection         -> { linked: bool, handle?: string, pending: bool }
POST /api/v1/platform/link            -> { token, expires_at }   (EXISTS — reuse)
```
Example `GET` response:
```json
{
  "briefing_enabled": true,
  "briefing_hour": 9,
  "timezone": "Africa/Lagos",
  "timezone_source": "country",
  "quiet_enabled": true,
  "quiet_start": 22,
  "quiet_end": 7,
  "cadence": "balanced",
  "daily_cap": 6,
  "allow": { "briefings": true, "risk": true, "nudges": true, "followups": true },
  "autonomy_level": "suggest",
  "humor_roasting": false
}
```
- Expose cadence as the enum in the API (`minimal|balanced|proactive`) and map to `daily_cap` server-side so the number stays an implementation detail.
- Handler stays thin (per `CLAUDE.md`); logic in a `miriampreferences` service.

### 6.3 Guard integration (the core wiring)
Replace the static constructor args with a per-user lookup. Two changes in `proactive_guard.go`:

1. Add a `PreferencesResolver` (mirrors the existing `UserTimezoneResolver` pattern):
   ```go
   type PreferencesResolver interface {
       Prefs(ctx context.Context, userID uuid.UUID) MiriamPrefs // returns defaults on miss
   }
   ```
2. Evolve `Allow` to be category-aware so opt-outs and critical-bypass are centralized:
   ```go
   // category ∈ {briefing, risk, nudge, followup, receipt}
   func (g *ProactiveGuard) Allow(ctx, userID, category string) bool
   ```
   - `receipt`/money-safety `risk` → bypass cap + quiet hours (unchanged behavior).
   - Per-category `allow_*` gate checked first.
   - `quiet_start/end`, `daily_cap`, and timezone read from prefs (fallback to today's defaults).
   - **Cache prefs in Redis (short TTL, e.g. 60s)** so the send path doesn't hit Postgres per message.

Then update call sites to pass a category:
- `bridge_dispatcher.go` `SendChatMessage/SendGenericNotification/SendToUser` → thread a category through (add a variant or context key). Mandate receipts already conceptually `critical` → map to `receipt`.
- Container wiring (~L1874): construct the guard with the new `PreferencesResolver` (backed by the new repo) instead of literals.

### 6.4 Daily-briefing worker changes (`daily_pulse/worker.go`)
- `dueForDailyPulse` currently hardcodes hour `9`. Read `briefing_hour` per user.
- Skip users with `briefing_enabled=false` **or** `allow_briefings=false`.
- Prefer prefs `timezone` over country-derived when set (keep country fallback).
- Keep the `sentDate` guard keyed by `user+localDate` so a mid-day tz/time change can't double-send.

### 6.5 Autonomy wiring
- Map `autonomy_level` → the existing control level used by `autopilotControlLevelAdapter` and the decision engine's mandate gating:
  - `observe` → report-only (no silent actions; nudges/suggestions still allowed if `allow_nudges`).
  - `suggest` → propose + require confirm (poll/`rail://authorize`).
  - `act` → execute within mandate, always emit a receipt.
- **Do not** let `act` bypass Face ID for withdrawals/transfers — that path stays as-is.

### 6.6 iMessage-native controls (integration)
Let the orchestrator recognize a small set of preference intents inbound and write to the same service, then confirm in Miriam's voice:
| User texts | Effect | Confirm copy (draft — design to finalize) |
|---|---|---|
| "mute today" / "not today" | Suppress non-critical for the rest of local day | "Done. I'll stay out of your way until tomorrow." |
| "quiet until Monday" | Temporary quiet window | "Quiet till Monday. I'll still flag anything urgent." |
| "less chatty" / "more" | Step cadence down/up one tier | "Dialing it back. You'll hear from me less." |
| "stop the daily brief" | `briefing_enabled=false` | "No more morning briefs. Ask anytime and I'm here." |
| "settings" | Deep-link to in-app screen | "Here's where you can tune me: [link]" |

Temporary states ("today", "until Monday") can live as Redis keys checked in `Allow` — no schema needed.

---

## 7. Integration flow (happy path)

```
User opens app → Miriam Preferences → toggles Quiet 23:00–08:00, cadence "Minimal"
   → PUT /miriam/preferences → miriam_preferences upserted
Worker sweep / proactive send
   → BridgeDispatcher.deliver(category)
      → ProactiveGuard.Allow(userID, category)
          → prefs (Redis-cached) → allow_* gate → quiet-hours(tz) → daily_cap(INCR)
          → critical (receipt/safety-risk) bypasses cap + quiet
      → resolve iMessage thread → publish (or no-op if unlinked)
Daily 08:00 local (their new briefing_hour if changed)
   → daily_pulse honors briefing_enabled + hour + tz
```

---

## 8. Edge cases & rules
- **Unlinked user:** preferences editable and persisted; nothing delivers (dispatcher no-ops). Show the connect banner.
- **No country/timezone:** default `Africa/Lagos`; surface `timezone_source: "default"` so UI can prompt "Set your timezone."
- **Critical bypass disclosure:** disabling Risk/Quiet cannot silence money-safety events — must be stated in UI, not silently ignored.
- **Cap reached:** non-critical messages dropped for the day (see D2 for whether to summarize).
- **Quiet window wrapping midnight** (22→7) already handled in `inQuietHours` — reuse, don't reimplement.
- **Redis down:** guard fails open (delivers). Acceptable; note in QA.
- **Autonomy = act but no mandate:** nothing to execute silently → behaves like suggest. No error state.

---

## 9. Telemetry
Emit (extend existing Prometheus counters in the orchestrator):
- `miriam_proactive_suppressed_total{reason="quiet|cap|opt_out"}`
- `miriam_preference_change_total{setting}`
- `miriam_briefing_sent_total` / `_skipped_total{reason}`
- iMessage command intents recognized vs. unmatched.
Product analytics: cadence distribution, % who disable briefings, autonomy tier adoption, quiet-hours customization rate.

---

## 10. Acceptance criteria / QA checklist
- [ ] `GET` returns correct defaults with no row; `PUT` upserts and round-trips.
- [ ] Setting quiet hours suppresses a non-critical nudge inside the window; a mandate receipt still arrives.
- [ ] Cadence "Minimal" caps non-critical sends at the mapped number/day (verify Redis counter + TTL).
- [ ] Briefing time change moves the send window; no double-send across a same-day change.
- [ ] Disabling briefings stops the daily pulse for that user only.
- [ ] Autonomy "observe" produces no silent mandate actions; "act" executes within mandate and always emits a receipt; withdrawals still require Face ID at every tier.
- [ ] Unlinked user: settings save; connect flow issues token; texting token back binds; connected state renders.
- [ ] iMessage "mute today" / "less chatty" mutate prefs and confirm in voice.
- [ ] `go build ./...`, targeted package tests, `make lint` clean; new migration has up+down.

---

## 11. Suggested phasing
- **P1 (core):** data model + API + guard/worker wiring for briefing on/off, briefing time, quiet hours, cadence. This alone delivers the "discreet infrastructure" promise.
- **P2:** connection sub-flow polish + message-type opt-outs + timezone override UI.
- **P3:** autonomy ladder UI + wiring; humor/roasting toggle.
- **P4:** iMessage-native NL controls + temporary quiet states.

---

## 12. Open decisions (need PM/design/eng sign-off)
- **D1 — Table strategy:** dedicated `miriam_preferences` (recommended, cohesive) vs. extend `notification_preferences`. Affects repo + migration.
- **D2 — Suppressed messages:** silently drop vs. a once-daily "held N updates" line vs. roll into next briefing.
- **D3 — Cadence→cap numbers:** confirm 2 / 6 / 12 against real engagement data.
- **D4 — Autonomy default:** ship as `suggest` (recommended) vs. `observe` for new users until trust is established.
- **D5 — Where "Miriam Preferences" lives** in the app IA (top-level vs. under Notifications vs. under the AI chat surface).

---

### Quick reference — files the developer will touch
- `internal/infrastructure/platform/proactive_guard.go` — add `PreferencesResolver`, category-aware `Allow`.
- `internal/infrastructure/platform/bridge_dispatcher.go` — thread category through send methods.
- `internal/infrastructure/di/container.go` (~L1874, ~L2119) — construct guard with resolver; inject new repo/service.
- `internal/workers/daily_pulse/worker.go` — honor `briefing_enabled` / `briefing_hour` / prefs `timezone`.
- `internal/domain/services/miriam/intelligence_orchestrator.go` — autonomy gating; NL preference intents.
- `internal/domain/services/miriampreferences/` (new) — service + repo interface.
- `internal/infrastructure/repositories/` — `miriam_preferences` repo impl.
- `internal/api/handlers/investing/` + `internal/api/routes/` — new endpoints (thin handlers).
- `migrations/NNN_miriam_preferences.{up,down}.sql` — new table.
