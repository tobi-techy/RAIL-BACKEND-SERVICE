# Miriam iOS app + Messages extension — device runbook

Real Face ID approvals for confirmation cards. The extension (`MiriamMessages`)
is the only code that can present a biometric prompt from an iMessage bubble;
everything else (staging, tokens, execution, transcript edits) stays server-side.

## 0. One-time: identity (5 min)

1. Apple Developer Program membership (paid) on the account that will sign.
   Free accounts cannot install on device or use TestFlight.
2. In Xcode: Settings > Accounts > add your Apple ID > select the Team >
   note the **10-character Team ID** (or developer.apple.com > Membership).
3. Edit `ios/Miriam/Config.xcconfig`: set `FLIP_TEAM_ID` to that Team ID.
   Defaults live there too: host `com.railmoney.rail`, extension
   `com.railmoney.rail.messages`. If those bundle IDs are taken in your account,
   change both lines AND mirror them into server env (step 4).
4. Connect the iPhone via cable, trust the computer, enable Developer Mode
   on the phone (Settings > Privacy & Security > Developer Mode).

## 1. Build & run on your iPhone

1. Open `ios/Miriam/Miriam.xcodeproj` in Xcode.
2. Select the **Miriam** scheme, destination = your iPhone.
3. Run (⌘R). First launch: iPhone Settings > General > VPN & Device
   Management > trust the developer certificate.
4. The Miriam host app is informational. The extension activates from Messages:
   open any iMessage conversation, tap `+` > Miriam.
5. Simulator note: the extension compiles and unit-tests run in Simulator,
   but Simulator has no Secure Enclave — approvals there use a software key
   (bannered in-UI) and must only ever point at dev backends.

## 2. End-to-end Face ID test (15 min)

Backend prerequisites (AtlasFlow env / local `.env`):

| Var | Value |
|---|---|
| `CONFIRMATION_TOKEN_SECRET` | random ≥32 chars (fail-closed without it) |
| `CONFIRMATION_BASE_URL` | `https://api.userail.money/confirm` |
| `RAIL_SERVICE_KEY` | random ≥32 chars, SAME value in Miriam env |
| `IMESSAGE_EXTENSION_BUNDLE_ID` | `com.railmoney.rail.messages` (bridge env) |
| `APPLE_TEAM_ID` | your Team ID (bridge env) |
| `CONFIRMATION_REQUIRE_DEVICE_SIGNATURE` | leave unset (off) until step 3 |

Flow:
1. In chat, trigger a money action (e.g. "send ₦500 to @tester").
2. ONE live card lands in the transcript. `bun` bridge log shows
   `sent live confirmation card`; `/health` shows `confirmation_cards: 1`.
3. Tap the card → extension opens → payload loads → tap **Approve with Face ID**.
4. Real system Face ID prompt appears (this is `SecKeyCreateSignature` on the
   Enclave-bound key path — first approval enrolls the key, audited as
   `biometric: "pass", assurance: enrolled`).
5. Money moves; the SAME bubble edits to Done. No second message.
6. Approve again from the same card (or replay the URL): terminal no-op.
7. Cancel path: fresh card → Cancel → bubble edits to Cancelled, nothing moves.
8. Expiry path: wait 5 min (or 60 s with `CONFIRMATION_TTL_SECONDS=60`) →
   bubble edits to Expired.

## 3. Enforce signatures (after step 2 passes on your device)

1. Confirm your iPhone's key is enrolled (server log: `confirmation transition`
   with `assurance: secure_enclave` on second approval).
2. Set `CONFIRMATION_REQUIRE_DEVICE_SIGNATURE=true` (strict mode).
3. Re-run step 2 with a fresh card: approval without a valid Enclave signature
   is now rejected (422 `device signature required`), token unconsumed, retry
   from the real device succeeds. Token-only approves are dead.

## 4. TestFlight (wider testing)

1. Xcode > Product > Archive (Release, Any iOS Device destination).
2. Distribute > App Store Connect > Upload. Extension + host upload as one item.
3. App Store Connect > TestFlight > add internal testers. Install on test phones.
4. Point the build at staging backend first (`MiriamConfirmBaseURL` in
   `Miriam/Info.plist`, or per-scheme config later).

## 5. Troubleshooting

- "No action link" in extension: the message has no `url` — cards sent before
  the bridge bundle-ID config carry plain links. Re-mint the card.
- "Unknown device key" once, then success: biometry changed (new face) — the
  extension re-enrolls automatically, audited as a new enrollment. Expected.
- 422 `device enrollment required (strict mode)`: strict on, device never
  enrolled. Turn strict off, approve once from the device, turn strict on.
- Xcode signing errors: Team ID mismatch between `Config.xcconfig` and the
  signing team; bundle ID already registered by another team.
- `xcodebuild` here: compile-only via
  `xcodebuild -project ios/Miriam/Miriam.xcodeproj -target MiriamMessages \
   -sdk iphonesimulator26.5 -configuration Debug CODE_SIGNING_ALLOWED=NO build`.
  Running tests needs a downloaded Simulator runtime
  (Xcode > Settings > Components) or a device.

## 6. What the server guarantees (so reviewers don't re-litigate)

- Token is single-use; replay after terminal = no-op (`ApproveWithDevice`).
- Failed signature NEVER burns the token — the real device can retry.
- Enrolled users must sign; unknown/unsigned approves are rejected, not
  downgraded (fail closed). Unenrolled users enroll once (TOFU, audited) or
  stay token-only unless strict mode is on.
- Only P-256 ECDSA SPKI enrolls (`ParseDevicePublicKey`) — a software key
  from anywhere but the Enclave cannot pass as biometric.
- Signatures cover `actionId.expiry` (`SignedMessage`) — no cross-card or
  post-expiry replay.
- `assurance` (token_only/enrolled/secure_enclave) rides the Miriam settle
  call and lands on the receipt audit.
