# iMessage poll votes (why a tap did nothing, and how it is fixed)

Miriam asks a lot of questions with an iMessage poll: onboarding answer choices,
`send_poll` from the agent interview, and the Confirm/Cancel confirmation for a
staged money action. A user tapped a poll option and got no reply at all.

Two independent faults made a tap a no-op. Either one alone was enough to lose
the vote, and both are fixed on our side (never by patching `node_modules`).

## Fault 1 — the webhook transport never carries a vote

Production runs `SPECTRUM_TRANSPORT_MODE=webhook`. Spectrum's webhook payloads
only ever express the documented inbound arms (text, attachment, contact,
richlink, reaction, group). Poll votes are not one of them, and the docs say so
directly: typing indicators, edits, poll votes and read receipts are not
delivered in webhook form.

The SDK *can* build votes from the provider's poll event stream, but it merges
that stream into `app.messages`, which the webhook transport deliberately never
consumes (consuming both transport modes would double-deliver every message).
So `inbound.ts`'s `poll_option` arm and its entry in the router's recoverable
allowlist are dead code in production — no tap ever reaches our HTTP route.

## Fault 2 — the SDK drops a vote when the poll has no title

On the stream, `toCachedPoll` builds the poll via `asPoll({ title })` where
`pollSchema.title` is `z.string().nonempty().max(300)`. Photon's server returns
`PollCreated`/`PollInfo` **without** a title (verified against
`@spectrum-ts/core@8.2.1` and `@photon-ai/advanced-imessage@0.12.0`), so the
cache write throws, `resolvePoll` fails, and `toPollOptionMessage` yields
nothing. This is exactly what a previously-removed postinstall `dist` patch used
to coerce; that patch is gone and must not come back.

## The fix — read votes from the provider

The raw provider client is already reachable through the SDK's platform registry
(`agent.__internal.platforms: Map<string, PlatformRuntime>`, `runtime.client`),
the same registry `index.ts` taps to fix the webhook platform alias. For
iMessage that client is a list of `{ phone, client }` per dedicated line, and
each `client.polls` exposes:

```ts
polls.get(pollMessageGuid)
// -> { chatGuid, title, options: [{ optionIdentifier, text }],
//      votes: [{ optionIdentifier, participant: { address } }] }

polls.subscribeEvents()
// -> TypedEventStream of { type: "poll.changed", pollMessageGuid, isFromMe,
//      actor: { address }, delta: { type: "created"|"optionAdded"|"voted"|"unvoted",
//      title?, options?, optionIdentifier? } }
```

Neither path involves the SDK cache that drops title-less polls, so both work
where the stream alone did not.

`cmd/spectrum-bridge/src/poll-watcher.ts` owns this:

- **Register** — `MessageHandler` gained an `onPollSent` hook. In the `poll`
  content arm it captures the id of the message the provider accepted
  (`space.send(poll(...))` resolves to a Message whose `.id` IS the
  `pollMessageGuid`; the SDK's own outbound poll record agrees) and hands
  `{ pollGuid, threadId, senderId, platform, title, options }` to the watcher.
  A failed hook never fails the send.
- **Live** — one `subscribeEvents()` per line, consumed via the stream's `on()`
  (whose unsubscribe closes the underlying gRPC stream), with capped backoff
  reconnect. Deltas are handled per the provider's documented four:
  `created`/`optionAdded` carry the poll's current `title` and its **full**
  `options` list (so an option appended after the poll was sent is learned from
  the delta, not only from the send-time prime), `voted` carries the
  `optionIdentifier` and is forwarded, and `unvoted` is ignored on purpose (the
  staged action stays pending and a later `voted` still counts). Our own votes
  and votes from other participants are ignored.
- **Recover** — a tap produces no event while the stream is down, so state is
  re-read from the provider (`polls.get`) at three moments: right after we send
  the poll, when that thread gets a new inbound message (counted *before* the
  message, so onboarding state advances in the order the person actually acted),
  and when a dropped stream comes back. That last one matters because a tap
  followed by no typing has no inbound message to ride on. The events doc
  prescribes `im.events.catchUp(sequence)` to replay missed transitions; we
  deliberately read current state instead, because a tap is an answer and the
  last choice is the answer, and unlike a replay it cannot double-count a tap
  that is also seen arriving live. Per-thread work is bounded by
  `POLL_RECONCILE_TIMEOUT_MS`.
- **Attribution** — a vote with a named voter must match the addressee. Addresses
  are compared canonically (lowercased, punctuation stripped), so
  `+234 916 490 4178` matches `+2349164904178` and `Alice@Example.com` matches
  `alice@example.com`. An anonymized vote in a DM is assumed to be the person we
  asked. Option text is resolved from primed option ids, else fetched on demand.
  Identifiers already forwarded are dropped (dedupe survives a stream event plus
  a reconciliation of the same tap), and votes on one poll are delivered in tap
  order so a Confirm cannot be overtaken by a Cancel.
- **Delivery** — each vote is posted through the shared `pollVotePayload`
  (`is_poll_vote: true`, `text: <option text>`, `poll_title`), the same shape the
  router's `poll_option` arm produces, so the backend and the guest brain need no
  change. Before posting, the watcher flushes the thread's debounce buffer, so
  words the user typed around the tap are delivered first.

A poll is tracked for 24h. When no client can be resolved the watcher logs a
warning and stays inert rather than breaking the bridge.

## Turn budget (the 500 that went with it)

The same event also produced a `backend POST failed` 500. The arithmetic fits a
budget overrun, not a crash: the debounce window plus ~14s of work against a
bridge POST timeout of 15s and a guest-turn budget of 13s. Two things were wrong:

- The bridge's inbound text timeout was a hardcoded 15s, so it could not be
  raised in step with anything on the backend.
- `guestTurnTimeout` was documented as the whole-turn budget but was applied per
  *completion*. One conversational turn can make two completions (the tool pass,
  then a follow-up when the model called tools without producing text), so a turn
  could legitimately run ~26s — well past the bridge deadline. The bridge then
  gives up on a turn we are about to answer, and the user waits out a redelivery.

Now `guestBrain.respond` bounds the whole turn with the budget the completer
declares (`CompletionBudget`), so a turn fails inside a deadline our caller is
still listening to, and the failure stays retryable and answerable. Each side of
the boundary is configurable so they can be tuned as one budget.

The handler still answers 500 for every `Process` failure, because the bridge
retries 5xx. What changed is the log: `platform inbound processing failed` is now
ERROR level and carries `retryable`, `platform`, `user_id`, `thread_id`,
`attempt`/`max_attempts` and the wrapped cause, so a turn that ran out of time is
distinguishable from a hard failure — that distinction is what was missing when
this was first reported.

## Configuration

| Flag | Where | Default | Meaning |
|---|---|---|---|
| `MIRIAM_POLL_WATCHER` | bridge | on | Read poll votes from the provider. `0`/`false`/`no`/`off` disables; taps are then silently lost. |
| `RAIL_BACKEND_TEXT_TIMEOUT_MS` | bridge | `15000` | Inbound POST timeout for plain messages. Must exceed the backend's turn budget. |
| `PLATFORM_GUEST_TURN_TIMEOUT_SECONDS` | backend | `13` | Whole-turn budget for a Python-backed guest turn. Keep a few seconds under the bridge timeout; values at or below the brain's 6s default have no effect. |

`GET /health` on the bridge reports `poll_watcher: { enabled, clients,
connected, tracked, votes_forwarded, votes_dropped, reconciles }`. A vote that
lands is logged as `accepted inbound` with `type: "poll_option"`, `poll_guid` and
`debounced: false`; `votes_dropped` rising means option text could not be
resolved, which is the one case where a tap is deliberately discarded, and
`reconciles` counts the state re-reads triggered by a dropped stream.

## Tests

- `cmd/spectrum-bridge/src/poll-watcher.test.ts` — client resolution from the
  registry, a forwarded tap, a title-less poll, other voters, anonymized voters,
  case-insensitive email matching, unvoted/own votes, on-demand option
  resolution, dedupe across stream and reconcile, recovery after a stream outage
  with and without a following message, no redundant re-read on boot, unsent
  polls, retryable failures, tap ordering, and the no-client case. Run with
  `bun test`.
- `handler.test.ts` — the `onPollSent` hook registers the accepted poll id, is
  skipped when the provider never accepted the poll, and cannot fail the send.
- `TestGuestBrain_TurnBudgetCoversBothPasses`,
  `TestGuestBrain_RaisedBudgetUsesASingleAttempt` — the turn-level budget.
- `TestInboundErrorFields_*`, `TestHandleInbound_TransientFailureIsFiveHundredWithDiagnostics`
  — a retryable failure is a 500 with the attempt counters logged.
