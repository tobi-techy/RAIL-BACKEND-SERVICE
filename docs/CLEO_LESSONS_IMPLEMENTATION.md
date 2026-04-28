# Cleo-Inspired Features — Implementation Plan

## ✅ Shipped

### #2 Personality Upgrade
**Status**: Deployed
- Punchier, quotable tone in system prompt
- Cultural humor (Lagos traffic, jollof, diaspora references)
- "Screenshot-worthy" responses directive

### #6 Conversational Paywall
**Status**: Deployed
- Miriam gives free diagnosis, gates actions behind Rail Pro
- Natural upsell in chat flow, never blocks conversation
- Triggers on: set_budget, transfer_funds, build plan, automate savings

---

## 🔨 To Build

### #1 Aha Moment Before Paywall

**Goal**: Every new user gets a "wow" moment in their first session before seeing any upgrade prompt.

**Free tier gets**:
- Financial health score (get_financial_health)
- Spending breakdown with donut chart
- One sharp insight ("You spent more on withdrawals than deposits this month")
- Balance overview
- 10 messages/day with Miriam

**Gated behind Rail Pro**:
- Transfers via Miriam (spend↔stash)
- Budget automation (set_budget)
- Financial plan builder (get_financial_plan)
- Receipt scanning + categorization
- Voice mode
- Unlimited messages
- Cash flow forecast

**Backend changes**:
- Add `subscription_tier` field to user context passed to orchestrator
- In `executeActionTool`, check tier before creating pending actions
- Return `upgrade_required: true` in tool result so Miriam can upsell naturally
- No code changes to the tools themselves — just gate execution

**Frontend changes**:
- Handle `upgrade_required` in stream events
- Show inline upgrade CTA styled as Miriam's suggestion, not a modal wall

**Effort**: ~2 days backend, ~1 day frontend

---

### #3 Daily Money Pulse (Push Notifications)

**Goal**: One personalized push notification per day from Miriam that creates a daily open habit.

**Notification types** (rotate daily):
1. **Daily spend**: "You spent $12 today — $3 under your daily budget 💪"
2. **Stash growth**: "Your stash earned $0.02 overnight. Small but steady 📈"
3. **Streak**: "Day 14 of your saving streak! Don't break it 🔥"
4. **Budget check**: "You've used 60% of your budget with 10 days left. Looking good."
5. **Milestone approaching**: "You're $47 away from $500 in stash. One more deposit does it."
6. **Inactivity nudge**: "Hey, your money's still working even if you're not checking. But I miss you 👋"

**Backend changes**:
- New worker: `daily_pulse_worker` — runs at 9am user-local-time
- Queries: today's spending, stash balance delta, budget progress, streak
- Generates one-liner using a lightweight LLM call (or template-based to save cost)
- Sends via existing SNS push infrastructure

**Data needed**:
- User timezone (add to profile if not present)
- Push notification opt-in (already exists via expo-notifications)

**Frontend changes**:
- Deep link from notification → Miriam chat with the insight pre-loaded
- Opt-in toggle in settings: "Daily Money Pulse from Miriam"

**Effort**: ~3 days backend (worker + templates), ~1 day frontend

---

### #4 Instant Stash Transfer (Conversion Weapon)

**Goal**: Free tier has a delay on stash→spend transfers. Pro gets instant. Creates urgency-driven upgrades.

**Current behavior**: All transfers are instant for everyone.

**Proposed behavior**:
- **Free tier**: Stash→spend transfers have a 4-hour processing delay (funds are locked, released after delay)
- **Pro tier**: Instant transfer (current behavior)
- **Spend→stash**: Always instant for everyone (we want people saving)

**Backend changes**:
- In `executeTransfer`, check subscription tier
- If free + stash→spend: create a `pending_transfer` with `scheduled_at = now + 4h`
- New worker: `delayed_transfer_worker` — processes pending transfers when scheduled_at is reached
- New table: `delayed_transfers` (id, user_id, from, to, amount, status, scheduled_at, executed_at)
- Miriam says: "Transfer queued! It'll hit your Spend in about 4 hours. Want it now? Rail Pro makes it instant ⚡"

**Frontend changes**:
- Show "Processing..." state for delayed transfers
- Show countdown timer on pending transfer
- Upgrade CTA on the pending transfer screen

**Effort**: ~3 days backend, ~2 days frontend

**⚠️ Product decision needed**: Is 4 hours the right delay? Too long = frustrating, too short = no urgency. Cleo uses "up to 3 business days" for free cash advances vs instant for paid.

---

### #5 Personal Spending Categories

**Goal**: Replace generic categories with personality-driven labels that feel personal.

**Category mapping**:
| Generic | Miriam's Label |
|---------|---------------|
| Food & Dining | Jollof Fund 🍚 |
| Transportation | Movement Money 🚗 |
| Entertainment | Fun Fund 🎉 |
| Shopping | Treat Yourself 🛍️ |
| Bills & Utilities | Adulting Costs 💡 |
| Transfers | Money Moves 💸 |
| Withdrawals | Cash Out 🏧 |
| Subscriptions | Auto-Deductions 🔄 |
| Health | Self-Care 💊 |
| Education | Level Up 📚 |
| Other | Miscellaneous |

**Backend changes**:
- New function: `humanizeCategory(category string) string` in the AI orchestrator
- Apply when building tool results for spending summary, money flow, transactions
- Store user-customized category names in Redis (future: let users rename their own)

**Frontend changes**:
- Display the humanized names in insight cards and spending breakdowns
- Category icons/emojis in the donut chart legend

**Effort**: ~0.5 day backend, ~0.5 day frontend

---

## Priority Order

1. **#5 Personal Categories** (0.5 day) — quick win, immediate personality boost
2. **#1 Aha Moment / Tier Gating** (3 days) — enables monetization
3. **#3 Daily Money Pulse** (4 days) — retention driver
4. **#4 Instant Transfer Gating** (5 days) — conversion weapon, needs product decision on delay duration

Total: ~12.5 days of engineering work to ship all four.
