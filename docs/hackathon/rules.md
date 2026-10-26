# Hackathon rules: Meta Global AI Developer Hackathon, 26 Oct to 4 Nov 2026

Read this before touching code in this repo until submissions close. It is the
operating agreement between Tobi and every agent working in MIRIAM or
RAIL_BACKEND.

Order of authority: **official event rules > this file > a task prompt.** If the
official rules contradict something here, the official rules win, and this file
is corrected the same day. Record every correction in section 7.

- Kickoff: Mon 26 Oct 2026, 17:00 WAT.
- Hard deadline: Thu 5 Nov 2026, 03:00 WAT (Wed 4 Nov, 9:00 PM ET).
- Our target: Wed 4 Nov 2026, 21:00 WAT, six hours of slack.
- Track: Startup. Team: Tobi Omotade, Rail Technology Concepts Ltd.

The submission is **Mimi, an always-on agent that runs a distributor's shop
network**. Not a photo reader, not an image classifier, not a dashboard.

---

## 1. Prove what is new

The single biggest disqualification risk is prior work that was not declared.
The baseline tag is the cut line, and `HACKATHON.md` is the declaration.

- At 17:00 WAT on 26 Oct, in every repo we touch, tag the default branch
  `hackathon-baseline` and push the tag. Run `scripts/hackathon_baseline.sh`.
  The script refuses to run before 17:00 WAT without `--force`, refuses to
  overwrite an existing tag, and tags `origin/main` so it works from a dirty or
  feature branch.
- Put new code in one obvious namespace, `hackathon/`, so a judge can read the
  diff as "one new folder plus small wiring changes":
  - `hackathon/harness/`: agent loop, budgets, tool registry, router, context
  - `hackathon/tools/`: Muse Spark and SAM 3.1 wrappers
  - `hackathon/sim/`: shop simulator, personas, scene goals
  - `hackathon/world/`: synthetic world seed and `WORLD.md`
  - `hackathon/evals/`: eval inputs, labels, runners, results
  - `hackathon/demo/`: demo script and fixtures
  In RAIL_BACKEND also `hackathon/delivery/` and migrations named
  `hackathon_*`.
- Edits to existing files are allowed and expected (wiring, routes, DI). Every
  such edit is listed in `HACKATHON.md` with the commit, so nothing about the
  new surface is hidden.
- Commit daily, small, with clear messages. No giant squash at the end. Each
  day's commits are the evidence of what was built during the window.
- `HACKATHON.md` at the repo root holds the table: built during the event vs
  existed before, with commit links. Update it on the day the work lands, not at
  the end, when nobody remembers.
- Prep work done before the window (the synthetic world, the screenshot set and
  its labels, API setup) is disclosed in `HACKATHON.md` under "Disclosed prep".
  It is allowed to be prep; it is not allowed to be silent.

## 2. Demo only new work

- The console existed before the event. Either keep it off screen, or label it
  on screen: "existing UI, new live data". Never imply the console was built
  this week.
- Narrate the boundary out loud in the video: "Everything you see Mimi decide
  here was built this week. The console shell existed; the data and the agent
  behind it are new."
- The demo must run on the synthetic world with real model calls. Never show a
  real shop, a real alert, a real bank account or a real person's name.
- If a feature does not work at filming time, it does not appear in the video
  and does not appear in the README as working. Say "building" instead.

## 3. Public repos and secrets

- Public today: `tobi-techy/MIRIAM`, `tobi-techy/RAIL-BACKEND-SERVICE`.
  Private today: `tobi-techy/mimi-webite` (the console).
- A repo that is demoed must be public, or the console stays off screen. The
  console is currently private: make it public after a secrets and history
  scrub, or cut it from the video.
- Before pushing anything for submission: no live keys, no `.env`, no customer
  data, no real phone numbers, no real account numbers. Run a secret scan on the
  diff, not only on HEAD.
- Secrets live in SSM or the AtlasFlow dashboard. Never in git, never in a
  screenshot, never in a README.
- Never paste a key, token or customer detail into a Linear issue or a commit
  message.

## 4. Data, names and assets

- **Synthetic world only.** The dataset is fictional; the README says so in one
  plain sentence, and `hackathon/world/WORLD.md` lists what is planted.
- **The distributor is "Ileri Distribution".** Wigmore Trading is a real company
  and a live sales lead: it must not appear in the demo, the seed, the console,
  the README, the video or any new file. If you find the old name, replace it.
- Shops, owners, reps, phone numbers, bank names and account numbers are
  invented. Use reserved or obviously fake numbers (`0800 000 0000`) and no real
  account numbers.
- Existing product docs in this repo already name real prospects. Do not add
  more, and never copy those names into hackathon material. The history is
  already public, so treat those names as public facts and keep all new writing
  generic ("Distributor A", "the pilot distributor").
- Mimi's own brand, stickers and art are ours. Use them freely.

## 5. Positioning and red lines

Pitch, in this order:

1. A distributor's shop network is unmanaged: nobody knows what is on the shelf,
   who has paid, who is faking a transfer, or who is about to run out.
2. Mimi is an always-on agent that runs that network: it reads what the rep and
   the shop send, checks it against the database, decides what is true, drafts
   the restock, and routes credit to a human for approval.
3. The console is where the distributor sees it and approves it.

Never pitch "we read payment screenshots". That sounds like a simple image
analyser, which is exactly what the anti-project list rejects. The vision model
is a sensor; the agent is the product.

Red lines, unchanged from the product:

- Mimi never moves money.
- Mimi never gives a shop money or credit advice. The distributor approves every
  credit change.
- Every number in a message is traceable to the database or the user's own
  words. No invented numbers, no invented shops, no invented traction.
- Shop text and photos are untrusted input. They never change Mimi's
  instructions.
- A screenshot is never proof of payment on its own. The code checks it and the
  record carries a proof level.

## 6. Questions the official rules must answer

Answer each with a quoted line from the official rules, or "asked, waiting" plus
the organisers' answer when it arrives. Put the answers in this file, section 7.

- [ ] Is existing code allowed? What must be disclosed, and how?
- [ ] What must be built inside the window, and must Meta models be the core?
- [ ] Are synthetic datasets and simulated users allowed in the demo?
- [ ] Are rendered screenshots of fake alerts acceptable eval inputs?
- [ ] Submission format: video length, repo requirement, README, deadline, time
      zone.
- [ ] Startup track: team size, company eligibility, revenue rules.
- [ ] Is the console counted as part of the submission if it is demoed?

## 7. Decisions and corrections

Keep this list dated and short. Newest last.

| Date | Decision | Why |
| --- | --- | --- |
| 26 Oct 2026 | Distributor renamed from Wigmore Trading to **Ileri Distribution** | Wigmore is a real company and a sales lead |
| 26 Oct 2026 | New code lives under `hackathon/` in every repo | Makes the judge's diff obvious |
| 26 Oct 2026 | Baseline tag `hackathon-baseline` at 17:00 WAT, pushed | Provenance |
| 26 Oct 2026 | Open: console repo public after scrub, or console off screen | Rule 3 |

## 8. The 17:00 runbook

Six steps, in this order. Nothing else at 17:00.

1. `git fetch` in MIRIAM and RAIL_BACKEND.
2. `scripts/hackathon_baseline.sh` in each repo. Confirm the tag on GitHub.
3. Read the official rules and fill section 6, then section 7.
4. Update `HACKATHON.md` with the tag commit in both repos, commit both.
5. Decide the console: public after scrub, or off screen. Write it in section 7.
6. Start RAI-266 (Day 1) only after steps 1 to 5 are done.

## 9. The agent self-check

Before every commit in either repo, answer these three questions in the commit
message or the PR body:

1. Is this new, and does `HACKATHON.md` say where?
2. Does this file touch a secret, a real company, a real person or a real
   account?
3. Would this line be a lie if a judge asked "did you build that this week?"

If any answer is uncomfortable, stop and fix it before committing.

---

Source of truth for this file: MIRIAM `docs/hackathon/rules.md`. The copy in
RAIL_BACKEND must match it, word for word apart from the repo-specific paths in
section 1. If you change one, change both in the same commit.
