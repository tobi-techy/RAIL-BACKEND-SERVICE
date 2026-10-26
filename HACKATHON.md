# HACKATHON.md

Submission: **Meta Global AI Developer Hackathon**, Startup track.
Project: Mimi, an always-on agent that runs a distributor's shop network.
Team: Tobi Omotade, Rail Technology Concepts Ltd.
Window: Mon 26 Oct 2026 17:00 WAT to Wed 4 Nov 2026 21:00 WAT.

Baseline tag: `hackathon-baseline` on `origin/main` at 17:00 WAT on 26 Oct 2026.
Commit: `(fill at tag time)`.
Everything below "During the window" is new after that tag. Everything in
"Before the window" is prior work, disclosed on purpose.

## Built during the window vs existed before

| Area | Before the window | During the window | Where it lives |
| --- | --- | --- | --- |
| Go core and Postgres schema | Existed | Unchanged except the delivery schema and wiring | `internal/`, `migrations/` |
| WhatsApp front door | Existed (Spectrum bridge + `/api/v1/platform/inbound`) | Fixes only if Day 1 needs them | `cmd/spectrum-bridge/`, `internal/api/` |
| Delivery records | None | New `deliveries` and `delivery_lines` tables, totals computed in code | `hackathon/delivery/`, `migrations/` |
| Synthetic world loader | None | New seed runner that loads the world into the demo schema | `hackathon/world/` |
| Console API for the demo | Partly existed | New read endpoints for sell-through, low stock and credit reasons | `hackathon/`, wiring diff |
| Money and payment records | Existed, out of scope | Only the demo payment paths are used | `internal/` |
| Agent rules | `AGENTS.md` existed | Hackathon section added, `docs/hackathon/rules.md` added | root, `docs/hackathon/` |

## Disclosed prep (authored before the window)

- `docs/hackathon/rules.md`, the hackathon block in `AGENTS.md`, this file:
  written on 26 Oct before 17:00 WAT. They contain no product logic.
- The synthetic world seed is prep by design, disclosed here and in the README.
- The Go core, the ledger and the bridge are prior work and are never presented
  as new. The submission demo only shows the delivery, shop and credit path.

## What this repo contributes to the submission

The state side: deliveries, stock, payments, credit reasons and the API the
console reads. The agent and the models live in MIRIAM.

## How to check the claim

```bash
git tag -l hackathon-baseline
git log --oneline hackathon-baseline..HEAD
git diff --stat hackathon-baseline..HEAD
```

The diff from the tag is the submission. If a file in that diff is not in the
table above, the table is wrong.

## What is not demoed

- The consumer wallet, crypto off-ramp, travel and iMessage features.
- Anything touching real money, real accounts or real customers.
- Anything that does not run end to end at filming time.
