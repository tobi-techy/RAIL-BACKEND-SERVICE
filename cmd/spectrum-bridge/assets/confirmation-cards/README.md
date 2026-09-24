# Confirmation card preview assets

Branded JPEG backgrounds for the live iMessage confirmation card — one per
**state family** (5 files, not one per action):

| File | States | Treatment |
|---|---|---|
| `confirm-pending.jpg` | `pending`, `authenticating` | Brand dark, "Approve with Face ID" energy |
| `confirm-working.jpg` | `approved` | Brand dark, in-progress shimmer |
| `confirm-done.jpg` | `completed` | Success treatment ("Done") |
| `confirm-muted.jpg` | `rejected`, `expired` | Muted / dead treatment |
| `confirm-error.jpg` | `failed` | Error treatment |

## Rules (non-negotiable)

- Bubble chrome = this JPEG + Apple `MSMessageTemplateLayout` text slots
  (`caption`, `subcaption`, `summary`). Button, Face ID, fonts, animation, and
  disabled states live in OUR Messages extension — never in this art.
- **Do NOT draw a Face ID button in the JPEG and pretend it is interactive.**
- Keep the center-left clear for `caption`/`subcaption` text; keep contrast
  high enough that white system text stays readable.
- Suggested export: JPEG, ~1200×900, sRGB, dark background `#0A0A0F` with a
  per-family accent edge. No text baked in (localization lives in slots).

## Behavior when missing

The bridge renders captions-only cards until these files land — the flow works
end to end without them. Drop the five JPEGs here and restarts pick them up
(no code change).
