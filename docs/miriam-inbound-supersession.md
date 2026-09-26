# Miriam inbound supersession

Status: **implemented**, dark-shipped behind two flags that both default to off
(`PLATFORM_TURN_SUPERSESSION` in the Go backend, `MIRIAM_TURN_SUPERSESSION` in
the bridge). This document is the design *and* a record of what was actually
built, including the parts deliberately left out.

## Problem

Photon's `best-practices/architecture` splits a turn into stages so any stage can
be cancelled when a follow-up message lands. Rail's bridge coalesces bursts
inside the quiet window, but once a batch has been posted the backend generates
and sends a reply with no way to know the user has moved on. Concretely:

1. User: "how much did I spend on food last month"
2. Backend starts generating (a model pass, several seconds).
3. User: "actually never mind, what's my balance"
4. The food reply lands *after* the balance question, and the balance answer
   lands after that. The user sees two replies, one of which answers a question
   they retracted.

The docs' fix is to cancel the in-flight stage. We cannot simply abort the
inbound HTTP call: by the time the bridge notices the follow-up, the backend may
already have staged a pending action, executed a ledger move, or sent a
confirmation card. Aborting the connection does not un-ring those effects, and a
partial abort masquerading as a network error would trip the bridge's retry
budget and confuse the HMAC/attempt accounting.

## Goal

When a newer inbound message lands while an older turn is still generating or
sending, the older turn's **advisory reply** must not be delivered. Nothing
already executed is reversed.

Non-goals:

- Cancelling a user-confirmed action. A `confirm_action` poll tap or an in-app
  Face ID approval is deliberate; an unrelated later message must never cancel it.
- Reversing a completed money move (see "Money actions" below).
- Cancelling proactive/briefing messages, which are not replies to a turn.

## Design: a turn token, two gates

The bridge mints a **turn id** per inbound batch and carries it end to end. The
backend echoes it on every message it sends for that turn. Two independent gates
then drop a stale turn:

```
bridge                    backend                         bridge
  │  POST /inbound           │                              │
  │  {turn_id: T1, ...} ───► │ MarkTurn(thread, T1)         │
  │                          │ generate (LLM, tools)        │
  │  POST /inbound           │                              │
  │  {turn_id: T2, ...} ───► │ MarkTurn(thread, T2)         │
  │                          │ ... T1 replies ────────────► │ /send {turn_id: T1}
  │                          │     gate 1: IsCurrent(T1)?   │   gate 2: T1 == latest?
  │                          │     no → drop, no send       │   no → drop
  │                          │ ... T2 replies ────────────► │ /send {turn_id: T2} → deliver
```

- **Gate 1 (backend).** `Processor.send` and `Processor.sendToSender` stamp the
  reply with the turn from the request context and drop it when
  `TurnTracker.IsCurrent` says a newer turn owns the conversation. These two
  helpers are the choke point every `Process`-path reply flows through, so the
  gate cannot be forgotten at a new call site.
- **Gate 2 (bridge, delivery safety net).** `/send` carries the turn; the bridge
  drops any reply whose turn is not the thread's latest. This closes the race
  where the backend's check passed a millisecond before a newer message arrived.

Both gates only ever *suppress a reply*. Neither mutates state.

### Turn id

`turn_id = crypto.randomUUID()`, minted in the bridge's `postToBackend` — but
**only for payloads that carry user content** (non-blank text, voice note, image,
document, contact card, or poll vote). Lifecycle signals (read receipts,
unsends, group events, unsupported/oversized notices) carry no words and are
left untagged, so they can never supersede a reply that is still in flight. That
guard is not cosmetic: read receipts arrive constantly while we are generating,
and letting one become "the latest turn" would silently swallow the answer to
the message the user actually sent.

A UUID rather than a counter so ordering survives bridge restarts with no
persistence: the bridge only ever compares "is this the id I most recently
posted for this thread", never "is this numerically newer".

`TurnSupersession.tagInbound` is idempotent — a payload that already carries a
turn id keeps it, so a retried flush reuses its original turn instead of minting
a second one.

### Config

| Side | Key | Env | Default |
|---|---|---|---|
| Bridge | `MIRIAM_TURN_SUPERSESSION` | `MIRIAM_TURN_SUPERSESSION` (`1`/`true`/`yes`/`on`) | off |
| Backend | `platform.turn_supersession` | `PLATFORM_TURN_SUPERSESSION` | off |

The flags are safe in any combination:

- Bridge on, backend off: ids are minted but never stamped back, so gate 2 has
  nothing to compare and every reply delivers.
- Backend on, bridge off: no ids ever arrive, so no turn is tracked and gate 1
  never suppresses.
- Off (both): no turn id is minted, sent, or stored — a true no-op, not a
  passthrough.

## Contract changes

### Bridge → backend (`POST /api/v1/platform/inbound`)

`turn_id?: string` on `InboundPayload` (`inbound.ts`). `postToBackend` mints it
via `turns.tagInbound(body)` for content-bearing payloads and the bridge records
it as the thread's latest at the same moment it posts:

```ts
// turn-supersession.ts
tagInbound(body) {
  if (!this.enabled || !body.thread_id || body.turn_id) return body.turn_id;
  if (!carriesUserContent(body)) return undefined;   // lifecycle signal
  const turnId = this.mint(body.thread_id);          // records as latest
  body.turn_id = turnId;
  return turnId;
}
```

Proactive sends and non-turn payloads simply omit it.

### Backend → bridge (`POST /send`)

`turn_id?: string` (`json:"turn_id,omitempty"`) on the Go `OutboundMessage`
(`response_builder.go`) and on the bridge's `OutboundMessage` (`handler.ts`).
`Processor.send`/`sendToSender` stamp the inbound turn from the context onto
every message they send for that turn (typing indicators aside — see below).
`BridgeDispatcher` builds `OutboundMessage` values and the wired `sendFunc`
marshals the whole struct with `ResponseBuilder.JSON`, so the field flows to the
bridge without per-call-site plumbing. Replies to `ProcessAction`
(confirm/cancel) and all proactive sends run with no turn on the context and
therefore carry **no** turn id — they are never superseded.

### Turn state

`TurnTracker` (`turn_tracker.go`) stores the current turn per conversation in
Redis under `miriam:turn:<platform>:<thread_id>` with a 5-minute TTL. The TTL is
long enough to outlast the slowest turn (a statement scan holds the request up
to ~150s) and short enough that a long-idle thread's marker cannot pin a much
later reply as current. Because the state is in Redis, every API replica
observes the same current turn.

## Where the gates live

**Gate 1 — `Processor.send` / `Processor.sendToSender`.** Deliberately exempt:

- **Typing indicators** (`content_type: "typing"`) are transient and ungated.
  A dropped stale reply leaves the typing keeper running until the newer
  reply's own send stops it, which is the correct behaviour.
- **`ProcessAction` replies** run with a context that has no turn, so the gate
  is a no-op for them by construction. Confirmed actions always report back.
- **Proactive sends** never enter `Process`.

**Gate 2 — `sendToSpace`.** Single choke point in front of both the `/send`
endpoint and the outbound-queue flush, so a stale reply is dropped whether it
arrives live or is replayed from the queue (including after a bridge restart,
in which case the in-memory turn map is empty and everything fails open).

## What is deliberately NOT implemented

Original design point 2 ("do not begin *new irreversible side effects* for a
superseded turn") was **not** implemented. The gate is applied at delivery, so a
superseded turn still pays for its model pass (and its statement scan) before the
reply is dropped. This is a conscious trade-off:

- The prompt-suppression points that exist today are either unactionable — the
  statement scan *is* what produces the reply, so there is no earlier moment at
  which "the user has moved on" is knowable — or already declared harmless
  (a staged confirmation expires on its own TTL and must not be auto-cancelled,
  because the *new* turn may reference it).
- Spreading an `IsCurrent` check across the staging/scanning call sites would put
  money-adjacent branching in a place with no safety upside, for a saving of one
  model pass in a rare race.

Also not implemented: persisting the bridge's `latestTurn` map. It is bounded
(`MAX_TRACKED_TURNS`, evicted by the existing 60s sweeper) and fail-open, so a
restart degrades to today's behaviour instead of dropping real replies.

## Money actions

- **Staged mutations** (confirmations): the staging is harmless and expires on
  its own TTL. A superseded turn that staged an action leaves a card the user can
  simply ignore or reject. Do not auto-cancel it — the *new* turn may reference
  it.
- **Immediate/irreversible moves**: a turn that has already called the ledger
  cannot be undone, and the design does not try. The existing confirmation flow
  (stage + explicit poll tap / Face ID) is the real protection. Supersession
  must never be used to bypass or weaken it; the gate is not consulted on the
  execution path of a confirmed action, only on advisory delivery.

## Edge cases

- **Multiple outbound messages per turn** (typing, reaction, N bubbles, poll):
  all share the turn id, so once the turn is superseded the remaining sends are
  dropped. Bubbles already accepted by the provider stay delivered — a
  multi-bubble reply can therefore end mid-stream. That is the accepted cost of
  a per-send check; nothing is duplicated, and the answer to the newer message
  follows immediately.
- **Reactions and poll votes** are content-bearing (`is_poll_vote`/text), so a
  genuine tap does supersede a stale generation. Read receipts do not.
- **Idempotency**: orthogonal to `client_guid`/bubble resume. A superseded turn
  is not retried; a delivered one is not duplicated.
- **Ordering of concurrent turns**: the bridge posts turns serially per thread
  (debouncer flushes are awaited), so "latest" is well-defined.
- **Fail-open everywhere**: unknown thread, unknown turn, missing key, Redis
  error, disabled flag, or untagged reply all mean deliver.

## Tests

Go (`internal/infrastructure/platform`):

- `TestProcess_SupersededTurnDoesNotDeliver` — a newer turn minted while the
  first is generating suppresses the first's reply.
- `TestProcess_CurrentTurnDeliversAndStampsTurnID` — the current turn's reply is
  delivered and carries its turn id for gate 2.
- `TestProcessAction_NeverSuperseded` — a confirm reply is delivered even with a
  newer conversational turn.
- `TestTurnTracker_NilRedisFailsOpen` — a tracker with no Redis treats every turn
  as current and marking is a safe no-op.

Bridge (`cmd/spectrum-bridge/src/turn-supersession.test.ts`):

- content payloads get a turn id; lifecycle payloads are never tagged
- a newer turn makes the previous one stale; turns are scoped per thread
- retries reuse the turn id already on the payload
- unknown thread / unknown turn / untagged / disabled all fail open

## Rollout

1. Deploy both sides with the flags off. Nothing changes on the wire.
2. Enable `PLATFORM_TURN_SUPERSESSION` + `MIRIAM_TURN_SUPERSESSION` together in
   staging; confirm `/health` reports `turn_supersession.enabled = true` and
   watch for `suppressed reply for superseded inbound turn` in the backend log
   and `dropping reply for superseded inbound turn` in the bridge log.
3. Watch for the failure mode that matters: a legitimate reply never arriving.
   The logs above are the signal; both gates log at `info` when they drop.
4. Enable in production. Roll back by unsetting either flag (unsetting the
   backend flag alone is sufficient — gate 2 then has nothing stamped to compare).
