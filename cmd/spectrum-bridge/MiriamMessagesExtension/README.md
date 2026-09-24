# Miriam Messages extension — stub spec

Native iMessage extension UI for the reusable live confirmation card.
Photon cannot render a native Face ID button; this extension is the product
half that can. `MessagesViewController.swift.stub` is the drop-in skeleton
(rename to `.swift` inside a real Xcode Messages Extension target).

## Target checklist

- Xcode → New Target → **Messages Extension**, bundle id e.g.
  `com.railmoney.rail.messages` (= `IMESSAGE_EXTENSION_BUNDLE_ID`), team
  (= `APPLE_TEAM_ID`).
- `NSFaceIDUsageDescription` in the extension `Info.plist`.
- Compact presentation only (`willBecomeActive`); no expanded transcript UI.
- Dynamic Type + VoiceOver labels on amount, destination, approve button.

## API contract (Go backend, server = source of truth)

| Call | Auth | Notes |
|---|---|---|
| `GET /confirm/{actionId}?t={token}` | signed token | payload or dead state (`dead: true`, `live: false`) |
| `POST /confirm/{actionId}/approve` `{t, biometric}` | signed token, single-use | executes on `pass`; replay = terminal no-op |
| `POST /confirm/{actionId}/reject` `{t, biometric}` | signed token, single-use | cancel path |

Token shape: `?t={expiryUnix}.{hexHMAC(secret, actionId.expiry)}`.
States: `pending → authenticating → approved → completed | rejected | failed | expired`
(terminal states never leave; illegal edges rejected server-side).

## Backend rules the extension relies on

1. Creating a confirmation never moves money. Face ID success + server accept does.
2. Token is single-use; replay after approve/reject/expire returns the terminal state.
3. Execution is idempotent by `actionId`.
4. Extension never renders "filled" unless the API response says `completed`.
5. Backend calls Spectrum `edit()` after every transition so the transcript card
   matches; if edit fails the server persists state and retries the edit —
   never a duplicate bubble.

## Until the extension ships

Leave `IMESSAGE_EXTENSION_BUNDLE_ID` / `APPLE_TEAM_ID` unset: the bridge sends
`app(confirmUrl, { live: true })` (Spectrum-hosted, tappable link to the same
`GET /confirm/:id` payload as a web fallback) so the flow tests end to end
this week. Setting both flips the bridge to
`customizedMiniApp({ live: true, … })` — the real Miriam clone. Do not fake Face
ID in a webview and call it done.
