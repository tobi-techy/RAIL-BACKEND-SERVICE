# AGENTS.md — Mimi

> Read `SOUL.md` first. That is how you think. This file is how you work in these repos.
> If this file and a direct instruction from Tobi disagree, Tobi wins. If this file and your own idea disagree, this file wins.

---

## 1. The product in one paragraph

Mimi is an AI helper on WhatsApp for Nigerian shop owners. It reads the stock book from a photo, tracks stock and sell-through, checks payments and fake alerts, and keeps the debt book. Distributors and brands use a web console to see verified shop data and give stock on credit. Distributors and brands pay. Shops are free.

Current goal: **pilot to first paying distributor.** Work that does not serve that goal waits.

---

## 2. Repos and what you own

| Repo | What it is | Stack |
|---|---|---|
| `tobi-techy/MIRIAM` | The agent brain: WhatsApp messages, page reading (stock book, invoices, delivery notes), tools, evals | Python |
| `tobi-techy/RAIL-BACKEND-SERVICE` | Core API: shops, distributors, ledger, payments, outbox, webhooks | Go, Postgres |
| `tobi-techy/mimi-webite` | Marketing site + `/console` for distributors and brands | Next.js 16, React 19, TypeScript |

Old consumer-finance code (savings, investing, USDC wallets for consumers) is **legacy**. Do not extend it. Do not import from it into new shop code. Delete it when a task gives you the chance and tests stay green.

---

## 3. Commands

### mimi-webite
```bash
npm ci
npm run dev            # local
npm run lint
npm run typecheck
npm run format:check
npm run build          # must pass before any PR
npm run build:export   # static export used for Cloudflare deploy
```

### MIRIAM and RAIL-BACKEND-SERVICE
Use the commands in each repo's `Makefile`, `pyproject.toml` or `go.mod` tooling. Before a PR, all of these must pass locally:
- formatter and linter
- unit tests
- the eval suite for any task you touched (MIRIAM)
- migrations up and down on a clean database (backend)

If a command is missing or broken, fix that first and say so in the PR.

---

## 4. Definition of done

A change is done when:
1. CI is green. Red CI on `main` is a stop-the-line event.
2. Tests cover the new path, including the unhappy path.
3. Any model or prompt change shows eval results **before vs after** on the golden set.
4. Anything touching naira has a test with real-looking numbers and an idempotency check.
5. The PR description says: what changed, why, how you tested it, what could break, how to roll back.
6. Docs or this file are updated if behaviour changed.

---

## 5. Hard rules (never break)

**Money**
- Never move money without verified intent, an idempotency key and an audit row in the same transaction.
- Money paths fail **closed**. Nice-to-haves fail open via the outbox.
- A screenshot is never proof of payment. Verify with the payment partner (webhook signature + requery).
- Payment references are unique on the partner's own ID (e.g. `sessionId`), not on a payload hash.
- LLMs read and talk. **Code** does sums, reconciliation and decisions.

**Data and consent**
- One Mimi per shop, one book. Distributors see only what the shop consented to: their own products, payment history, credit signals. Never a competitor's sales.
- Every shop-data query is scoped by tenant. Postgres row-level security on.
- Mark every number as `verified` or `self_reported`. Credit uses verified only.
- No personal data in logs, traces or eval fixtures without masking.

**Secrets**
- Never commit secrets, keys, tokens or `.env` files. gitleaks runs in CI; do not bypass it.
- If a secret ever lands in git, stop, tell Tobi, rotate it, then clean history.

**Scope**
- No consumer financial advice. Mimi helps businesses run stock and get paid.
- WhatsApp goes through the official Cloud API (via Photon/Spectrum). No unofficial WhatsApp libraries, ever.
- Agent computers never hold banking keys and never move money.

**Permissions**
- Never merge to `main`, deploy to production, or run a destructive migration without Tobi's explicit go-ahead in writing.
- Never send a message, email or post as Tobi or as Mimi to real users without approval.
- Never change DNS or custom domains.

---

## 6. How to work

1. **Pick work from Linear.** Initiative: *Mimi: from pilot to first paying distributor*. Reference the issue ID (`RAI-xxx`) in branch names and PRs: `rai-123-short-name`.
2. **Write the plan first** for anything over half a day: problem, numbers, options, choice, test plan. Put it in the issue.
3. **Small PRs.** Under ~400 lines changed where possible. One idea per PR.
4. **Conventional commits:** `feat:`, `fix:`, `chore:`, `refactor:`, `test:`, `docs:`.
5. **Ask only when blocked.** If a decision is reversible, decide, note it, move on. If it touches money, consent, legal or cost over ~$50/month, ask Tobi first.
6. **Decisions go in an ADR** (`docs/adr/NNNN-title.md`): context, options, decision, consequences, what would change our mind.

---

## 7. AI work rules (MIRIAM)

- Every task (stock book read, invoice read, fake alert check, Pidgin intent, voice note) has a **golden set** of 30 to 50 real, masked examples in `evals/`.
- Every bug from a real shop becomes a new eval case before the fix merges.
- Pick models per task by eval: cheapest model that passes wins. Log cost and latency per run.
- Numeric reads: read twice, reconcile in code, ask the owner only about rows that disagree.
- Every agent run is traced (Langfuse): input, tools, output, cost, latency. If you cannot replay it, it is not done.
- Prompts live in files, versioned, never inline strings scattered in code.
- Replies to shop owners: short, warm, plain English or Pidgin to match the owner, never jargon.

---

## 8. Backend rules (RAIL-BACKEND-SERVICE)

- New shop domain lives in its own small, clean module. Reuse the good parts (ledger, outbox, idempotency, HMAC bridge, webhook adapter). Do not reuse consumer-finance models.
- Postgres is the source of truth. Every external side effect goes through the outbox.
- Every endpoint: auth, tenant scope, input validation, rate limit, structured error.
- Every migration is reversible and tested on a copy of real-shaped data.
- Invariant checks in code, e.g. `sales = opening_stock + deliveries − closing_stock`. Alert when they break.

---

## 9. Web rules (mimi-webite)

- Design system: dark by default with light toggle, near-black `#101010`, warm greys, Geist + Geist Mono labels, 3px corners, hairline borders, one hot orange for status only, quick mechanical motion, draggable stickers for play.
- Copy is plain and short. No buzzwords. No em dashes.
- `/console` must work on a cheap Android phone on slow 3G. Check bundle size on every PR.
- Accessibility: keyboard reachable, real labels, contrast passes.

---

## 10. Reporting to Tobi

End every work session with:
```
Shipped:   what merged or is ready (links)
Broke:     what failed and what you learned
Number:    the one metric that moved
Blocked:   what you need from Tobi (or "nothing")
Next:      top 3 for the next session
```
Keep it short. Answer first, then why. One recommendation, not five options.

---

## 11. When something breaks in production

1. Kill switch or rollback first.
2. Tell Tobi in plain words: what happened, who is affected, what you did.
3. Root cause, not nearest cause.
4. Add a test or eval case so it cannot repeat silently.
5. Five-line postmortem in the issue.
