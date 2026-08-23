# Environment Variables Reference — AtlasFlow Production

Set these in the AtlasFlow dashboard under each project → **production → Environment Variables**.
There is no CLI for secrets; values are encrypted at rest (max 100 runtime vars per environment).

**Live workspace (2026-08-15):** `tobi-omotade-2cd167ac`  
**GitHub repo:** `tobi-techy/RAIL-BACKEND-SERVICE`  
**Go API project (already live):** `rail-backend-service`  
**Custom domain:** `https://api.userail.money`

Variables marked **SECRET** must be real values — never commit them.

---

## Key audit of vars already on `rail-backend-service`

Reviewed against `internal/infrastructure/config/config.go` and current callers (2026-08-15).
This is the list you already stored, classified.

### Safe to delete (code no longer reads them)

| Variable | Why |
|---|---|
| `LULO_API_KEY` | Lulo yield is gone. Only leftover ECS task-def JSON references it. Use `REFLECT_*` instead. |
| `LULO_BRIDGE_SOURCE_WALLET_ID` | Same. |
| `LULO_OWNER_WALLET` | Same. |
| `LULO_PRIVATE_KEY` | Same — also a live private key sitting unused. Remove first. |
| `GRAFANA_API_TOKEN` | Only consumed by the old ECS Grafana Alloy sidecar (`deployments/grafana/`). Not read by `rail-api`. |
| `NEWS_API_KEY` | Only in retired ECS task defs. No Go reader. |
| `OPENAI_API_KEY` | Miriam routes through **Cencori**, not OpenAI directly. Simulation tests use `AI_OPENAI_API_KEY`, which is a different name and local-only. |
| `EMAIL_API_KEY` | Not bound. Email key is `RESEND_API_KEY` or `UNOSEND_API_KEY`. |
| `INTERNAL_API_KEY` | Not bound. The app reads `SECURITY_INTERNAL_API_KEY` only. You already have that — drop the unused alias. |
| `DATABASE_PASSWORD` | Unused when `DATABASE_URL` already contains the password. |
| `VOICE_DAILY_LIMIT_USD` | Present in `.env.example` but **not wired**. `ai_wiring.go` hardcodes `$500`. Harmless clutter. |

### Deprecated — keep only if you still need the old path

| Variable | Status |
|---|---|
| `ASSEMBLYAI_API_KEY` | Voice path is ElevenLabs. AssemblyAI client is backward-compat only. Safe to drop once you confirm no old iOS build still expects the AssemblyAI WS protocol against a live key. |
| `SNS_PUSH_ANDROID_PLATFORM_ARN` | Legacy AWS SNS push. Preferred path is OneSignal (`PUSH_PROVIDER=onesignal` + `ONESIGNAL_*`). Keep until OneSignal is set and SNS is unused. |
| `SNS_PUSH_IOS_PLATFORM_ARN` | Same. |
| `SNS_PUSH_REGION` | Same. |

### Keep — still used

Everything else on your list is still bound and used (or is a real feature flag for a provider you still have):

`APPLE_*` (Sign in with Apple), `AUTH_BLACKLIST_FAIL_OPEN`, `BLEND_*` (if `BLEND_ENABLED=true`), `BRIDGE_*`, `CHAINRAILS_*`, `CIRCLE_*` (including `CIRCLE_PUBLIC_KEY_PEM` for signed wallet ops / recovery), `DATABASE_URL`, `DATABASE_SSL_MODE`, `DIDIT_*`, `ELEVENLABS_*`, `EMAIL_PROVIDER` / `EMAIL_FROM_*` / `EMAIL_REPLY_TO`, `ENCRYPTION_KEY`, `ENVIRONMENT`, `GIN_MODE`, `GRAPH_*` (only required when `GRAPH_ENABLED=true`), `JWT_SECRET`, `LANGFUSE_*` (optional LLM traces), `MIXPANEL_TOKEN`, `PAJ_*`, `PORT`, `POSTHOG_*`, `RAMPHUB_*`, `REDIS_*`, `RESEND_API_KEY`, `SECURITY_INTERNAL_API_KEY`, `SUPERMEMORY_API_KEY`, `TELEGRAM_ALERTS_*`.

---

## Missing on `rail-backend-service` (needed for Miriam + full prod)

These are **not** in the list you pasted. Add them before treating the agent as live.

### Blockers for iMessage / Miriam chat

| Variable | Notes |
|---|---|
| `CENCORI_API_KEY` | **SECRET.** Miriam's LLM gateway. Without this she cannot think. |
| `CENCORI_MODEL` | `gpt-4o` (or your chosen quality model) |
| `CENCORI_FAST_MODEL` | `gpt-4o-mini` |
| `CENCORI_VISION_MODEL` | `gpt-4o` |
| `PLATFORM_ENABLED` | `true` |
| `PLATFORM_BRIDGE_HMAC_SECRET` | **SECRET.** Must match spectrum-bridge `RAIL_HMAC_SECRET`. |
| `PLATFORM_BRIDGE_BASE_URL` | `https://spectrum-bridge-tobi-omotade-2cd167ac.atlasflow.dev` (after that project is up) |
| `PLATFORM_BRIDGE_MESSAGING_ADDRESS` | The bridge's iMessage handle (e.g. `+15555550100`). Users text the link token here. Required for account linking deep links. |
| `PLATFORM_APP_DEEP_LINK_BASE_URL` | `rail://` (defaults to `rail://` if unset) |
| `PLATFORM_ONBOARDING_ENABLED` | `true` |

### Sidecar URLs (set after those projects are running)

| Variable | Value |
|---|---|
| `ENRICHMENT_SERVICE_URL` | `https://rail-enrichment-tobi-omotade-2cd167ac.atlasflow.dev` |
| `ENRICHMENT_ENABLED` | `true` |
| `DOCUMENT_OCR_SERVICE_URL` | `https://rail-ocr-tobi-omotade-2cd167ac.atlasflow.dev` |
| `DOCUMENT_ENABLE_PYTHON_OCR` | `true` |

### Strongly recommended (features already in code)

| Variable | Why |
|---|---|
| `ONESIGNAL_APP_ID` / `ONESIGNAL_API_KEY` | Preferred push (`feat` #212). Set `PUSH_PROVIDER=onesignal`. |
| `REFLECT_API_KEY` / `REFLECT_BASE_URL` / `REFLECT_SOLANA_RPC` | Yield (replaces Lulo). |
| `TAVILY_API_KEY` | Miriam `web_search`. |
| `CLOUDFLARE_ACCOUNT_ID` / `CLOUDFLARE_ACCESS_KEY` / `CLOUDFLARE_SECRET_KEY` / `CLOUDFLARE_R2_BUCKET` | Document / receipt storage. |
| `AIRBILLS_SECRET_KEY` / `AIRBILLS_WEBHOOK_SECRET` | Bill pay. |
| `SENTRY_DSN` / `SENTRY_ENVIRONMENT` | Error tracking. |
| `WEBAUTHN_RP_ID` / `WEBAUTHN_RP_ORIGINS` | Passkeys / Face ID webauthn. |
| `CORS_ALLOWED_ORIGINS` | `https://app.userail.money,https://userail.money` |
| `AI_ELEVENLABS_API_KEY` / `AI_ELEVENLABS_VOICE_ID` | Voice notes alias. You already have `ELEVENLABS_*`; add these if you want the gated REST STT/TTS path. |

---

## rail-backend-service (Go API) — keep / set

### Core

| Variable | Value | Notes |
|---|---|---|
| `ENVIRONMENT` | `production` | |
| `PORT` | `8080` | AtlasFlow health probes this port at `/` |
| `GIN_MODE` | `release` | |
| `LOG_LEVEL` | `info` | |
| `LOG_FORMAT` | `json` | |
| `WORKERS_LEADER_ELECTION` | `true` | Production default. Only one API replica runs crons. |

### Database / Redis

| Variable | Notes |
|---|---|
| `DATABASE_URL` | **SECRET.** `postgresql://…?sslmode=require` |
| `DATABASE_SSL_MODE` | `require` |
| `REDIS_HOST` | Upstash host (already set) |
| `REDIS_PORT` | `6379` |
| `REDIS_PASSWORD` | **SECRET** |
| `REDIS_TLS` | `true` |

### Auth / crypto

| Variable | Notes |
|---|---|
| `JWT_SECRET` | **SECRET** |
| `ENCRYPTION_KEY` | **SECRET** |
| `SECURITY_INTERNAL_API_KEY` | **SECRET** — ops `/internal/*` |
| `AUTH_BLACKLIST_FAIL_OPEN` | `false` in prod |
| `APPLE_TEAM_ID` / `APPLE_KEY_ID` / `APPLE_PRIVATE_KEY` | Sign in with Apple |

### AI / Miriam

| Variable | Notes |
|---|---|
| `CENCORI_API_KEY` | **SECRET — required** |
| `ELEVENLABS_API_KEY` / `ELEVENLABS_AGENT_ID` / `ELEVENLABS_VOICE_ID` / `ELEVENLABS_WEBHOOK_SECRET` | Voice |
| `SUPERMEMORY_API_KEY` | Long-term memory |
| `LANGFUSE_HOST` / `LANGFUSE_PUBLIC_KEY` / `LANGFUSE_SECRET_KEY` | Optional traces |
| `TAVILY_API_KEY` | Optional web search |

### Money providers (keep)

Circle, Bridge, Blend, ChainRails, PAJ, RampHub, Didit — keep the vars you already have.

---

## spectrum-bridge (TypeScript / Bun) — new project

Create as AtlasFlow project `spectrum-bridge`, root `cmd/spectrum-bridge/`, port **3000**.

`src/config.ts` **refuses to boot** unless the required rows are set.

| Variable | Value | Notes |
|---|---|---|
| `SPECTRUM_PROJECT_ID` | Spectrum dashboard | **SECRET, required** |
| `SPECTRUM_PROJECT_SECRET` | Spectrum dashboard | **SECRET, required** |
| `RAIL_HMAC_SECRET` | same as `PLATFORM_BRIDGE_HMAC_SECRET` | **SECRET, required** — used to sign outbound requests to backend |
| `RAIL_BACKEND_URL` | `https://api.userail.money` | backend base URL for inbound/action HTTP POSTs |
| `BRIDGE_PORT` | `3000` | AtlasFlow default health port |
| `NODE_ENV` | `production` | |
| `LOG_LEVEL` | `info` | |
| `SPECTRUM_WEBHOOK_PATH` | `/spectrum/webhook` | |
| `SPECTRUM_WEBHOOK_SECRET` | optional | **SECRET** |
| `TELEGRAM_BOT_TOKEN` | optional, enables Telegram | **SECRET** |
| `WHATSAPP_ACCESS_TOKEN` | optional | **SECRET** |
| `WHATSAPP_PHONE_NUMBER_ID` | optional | |
| `WHATSAPP_APP_SECRET` | optional | **SECRET** |

Do **not** trigger the first deploy until the required secrets are saved. The process exits on invalid config, so health checks will fail.

---

## rail-enrichment (Python)

No secrets. Stateless ML inference.

| Variable | Value |
|---|---|
| `PORT` | `8090` (or omit — Dockerfile defaults to 8090; AtlasFlow may inject `3000`) |

Health: `GET /` and `GET /health`.

## rail-ocr (Python / PaddleOCR)

No secrets. Stateless OCR.

| Variable | Value |
|---|---|
| `PORT` | `8091` |

Health: `GET /` and `GET /health`.  
Runtime tier **large**. Current workspace plan only allows build-tier `standard`.

---

## Shared secrets (must match)

| rail-backend-service | spectrum-bridge | Notes |
|---|---|---|
| `PLATFORM_BRIDGE_HMAC_SECRET` | `RAIL_HMAC_SECRET` | **SECRET.** Used to HMAC-sign HTTP traffic in both directions. |
| `PLATFORM_BRIDGE_BASE_URL` | `RAIL_BACKEND_URL` | The backend's outbound URL must point at the bridge (`https://spectrum-bridge-…atlasflow.dev`); the bridge's `RAIL_BACKEND_URL` must point back at the Go API (`https://api.userail.money`). |

The bridge and backend communicate over HTTP (AMQP/RabbitMQ is no longer used).

---

## Key generation

```bash
openssl rand -hex 32   # JWT_SECRET, ENCRYPTION_KEY, HMAC
```
