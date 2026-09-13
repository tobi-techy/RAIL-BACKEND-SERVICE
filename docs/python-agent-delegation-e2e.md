# Python-agent delegation — E2E runbook & deployment checklist

How to prove — and then deploy — the flow where platform chat (iMessage /
Telegram / WhatsApp) is handled by the **Python MIRIAM agent** as the LLM brain,
while Go stays the money authority and every mutation is confirmed via email OTP.

## Architecture recap

```
bridge ──POST /api/v1/platform/inbound (HMAC)──▶ Go backend
                                                       │
                        orchestrator.platform*… delegates to
                                                       ▼
                                                  Python agent (FastAPI, /api/v1/chat, JWT)
                                                       │  requires_confirmation card
                                                       ▼
                                                  Go stages email OTP (Redis)
                                                       │  reply = "reply with the 6-digit code"
                                                       ▼
Go ──POST /send (HMAC)──▶ bridge ──▶ user (email carries the code)
user replies with code ──inbound──▶ Go verifies OTP ──▶ Python executes via Go REST
```

Go keeps JWT signing, conversation lookup, the user record, and the money paths
(`/api/v1/p2p/send`, transfers, investments). Python only ever runs the model and
answer text, and executes confirmed cards through Go's REST with the JWT Go minted.

## Feature switch

| Config | Env | Default | Effect |
|---|---|---|---|
| `python_agent.enabled` | `PYTHON_AGENT_ENABLED` | `false` | Off = old in-process Cencori brain. On = delegate to Python. |
| `python_agent.base_url` | `PYTHON_AGENT_URL` | `http://localhost:8000` | Python agent reachable from Go. |
| `python_agent.jwt_ttl_seconds` | `PYTHON_AGENT_JWT_TTL_SECONDS` | `120` | Life of the per-turn JWT Go mints for Python. |
| `python_agent.otp_ttl_seconds` | `PYTHON_AGENT_OTP_TTL_SECONDS` | `600` | OTP code validity. |
| `python_agent.otp_max_attempts` | `PYTHON_AGENT_OTP_MAX_ATTEMPTS` | `3` | Wrong-code attempts before the confirmation is cancelled. |
| `python_agent.http_timeout_seconds` | `PYTHON_AGENT_HTTP_TIMEOUT_SECONDS` | `60` | Go→Python client timeout. |

The delegation path also requires **Redis** (OTP staging), a **non-empty
`JWT_SECRET`**, and a configured `platform.bridge_base_url` + bridge HMAC secret.
If any of those are missing the adapter stays on the in-process orchestrator
(fail closed).

## Secret alignment (do this FIRST)

All three of these must share the **same** JWT secret or money execution breaks:

1. Go mints `POST /api/v1/chat` tokens signed with `JWT_SECRET`.
2. Python validates them with its own `JWT_SECRET` (`miriam_agent/config/settings.py`).
3. Python then calls Go REST (`/api/v1/p2p/send`, etc.) **with that same token**,
   which Go validates with `JWT_SECRET` again.

Set `JWT_SECRET` to one value in:
- Go: `RAIL_BACKEND/.env` (`JWT_SECRET=…`)
- Python: `MIRIAM/.env` (`JWT_SECRET=…`)

Also confirm Python's `GO_BACKEND_URL` points at the **same Go backend** that
delegates (in production that is the public AtlasFlow URL, not `localhost`).

> Local note: MIRIAM's `.env` `JWT_SECRET` has been aligned to Go's (the container
> and Go's `.env` share one value). If you rotate secrets, re-run the alignment —
> deployment breaks the moment any two of the three diverge.

## Local E2E

### 1. Run everything used by the harness

- Go backend with the delegation path enabled. Rebuild the image (the running
  `rail_backend-app-1` predates delegation) and set:
  ```
  PYTHON_AGENT_ENABLED=true
  PYTHON_AGENT_URL=http://host.docker.internal:8000   (host.docker.internal so the app container can reach your laptop's uvicorn)
  PLATFORM_BRIDGE_BASE_URL=http://host.docker.internal:3100
  EMAIL_PROVIDER=log
  EMAIL_ENVIRONMENT=development
  JWT_SECRET=<must match MIRIAM/.env>
  ```
- Python agent: `cd MIRIAM && .venv/bin/uvicorn miriam_agent.cli:app --host 127.0.0.1 --port 8000`
  (JWT_SECRET aligned with Go's).

### 2. Start the mock bridge

```bash
BRIDGE_HMAC_SECRET=<PLATFORM_BRIDGE_HMAC_SECRET> python3 scripts/e2e/mock_bridge.py
```

### 3. Run the driver

```bash
export E2E_GO_URL=http://localhost:8080
export E2E_HMAC_SECRET=<PLATFORM_BRIDGE_HMAC_SECRET>
export E2E_DATABASE_URL=postgres://postgres:postgres@localhost:5432/rail_service_dev
# Which container's logs hold the EMAIL_PROVIDER=log OTP lines?
export E2E_GO_LOG_DOCKER=rail_backend-app-1        # …or E2E_OTP_LOG_FILE=/path/to/go.log
python3 scripts/e2e/python_delegation_e2e.py
```

The driver:
1. Seeds a linked `platform_identities` row (unless one exists).
2. Phase 1 — sends a greeting; asserts an outbound reply comes back via the bridge.
3. Phase 2 — sends a money instruction; asserts a confirmation is staged, scrapes
   the OTP from Go's logs, replies with it, and asserts a final reply.

Environment flags are documented at the top of the script. Exit code `0` = all
assertions passed.

> Phase 2 depends on the Python agent actually deciding the message is a money
> action and returning a `requires_confirmation` card. Getting "send $X to <target>"
> to stage a card needs a real recipient on the Go side. The harness only asserts
> **that the flow gated and completed**, with whatever mutation the agent staged.

## AtlasFlow (production) deployment checklist

Order matters — verify each line before moving on.

**Go backend (AtlasFlow):**
- [ ] `PYTHON_AGENT_ENABLED=true`
- [ ] `PYTHON_AGENT_URL=https://<atlasflow-host>:<port>` — must be reachable from Go
      (DNS/resolution + firewall + the AtlasFlow server actually bound to a public
      or internal network address)
- [ ] `JWT_SECRET` set and identical to Python's
- [ ] `EMAIL_PROVIDER=resend` (or `ses`) — NOT `log` (log sink is blocked in prod)
- [ ] Redis reachable (OTP staging relies on it)
- [ ] Platform stack up: `PLATFORM_ENABLED=true`, `PLATFORM_BRIDGE_BASE_URL`,
      `PLATFORM_BRIDGE_HMAC_SECRET` set
- [ ] `/api/v1/platform/inbound` + `/action` still reachable by the bridge (HMAC)
- [ ] Health check passes: the container's `--health-check` still succeeds

**Python agent (AtlasFlow):**
- [ ] Runs the uvicorn server persistently (not one-shot)
- [ ] `JWT_SECRET` identical to Go's
- [ ] `GO_BACKEND_URL` = the **public** Go backend URL (not `http://localhost:8080`)
      so money cards execute against the real Go
- [ ] LLM keys present (`CONCENTRATE_API_KEY` / `OPENAI_API_KEY`, `ANTHROPIC_API_KEY`)
- [ ] `DATABASE_URL`, `REDIS_URL`, `ENCRYPTION_KEY`, Supermemory keys as per existing
      MIRIAM config
- [ ] From Go's network, `curl https://<atlasflow-host>:<port>/health` (or
      `/docs` / root) returns 200

**Post-deploy verification (in order):**
1. Send a plain greeting via a real bridge thread → expect a reply (Python brain).
2. Send a money instruction → expect an **email** with "Your MIRIAM confirmation
   code" and a reply asking for the code.
3. Reply with the code → expect a confirmation/celebration reply and the mutation
   landing in Go.
4. Watch Go logs for: `Platform messaging delegated to Python agent (MIRIAM)`,
   `python agent chat failed` (should stay absent during the happy path), and
   `otp email delivery failed` (should stay absent).

## Fail-closed notes

- Missing Redis / JWT secret / base URL → delegation silently stays OFF; the old
  Cencori path runs instead (no OTP step-up). Verify the log line
  `Platform messaging delegated to Python agent (MIRIAM)` before trusting a deploy.
- A 6-digit number typed for any other reason while a confirmation is staged is
  treated as an OTP attempt; wrong codes do not execute anything.
- Bare YES/poll vote/tapback can never pass step-up — the adapter answers with a
  "reply with the 6-digit code" message instead.
- `EMAIL_PROVIDER=log` is a development-only sink and is refused in production.

## Proactive reacher (Miriam's 24/7 loop)

On a schedule, the Go reacher asks the Python agent whether **any linked user
has one genuinely useful thing worth telling them right now**, and delivers the
drafted message via the iMessage bridge.

```
tick ── ListLinkedByPlatform(imessage) ──▶ for each user:
        guard.CanSendCategory(nudge)  // peek quiet hours BEFORE any LLM token (no quota spent)
        Python POST /api/v1/proactive/analyze  (per-user JWT, same PYTHON_AGENT_URL)
        if should_reach_out && message: BridgeDispatcher.SendChatMessage (guard enforces daily cap)
```

Division of labour: **Python decides** (fail-open to "stay quiet"); **Go owns**
the schedule, quiet hours, per-user daily cap, and iMessage delivery.

Feature switches (Go side):

| Env | Default | Effect |
|---|---|---|
| `PROACTIVE_REACHER_ENABLED` | `false` | On enables the worker (also needs Python delegation + bridge wired). |
| `PROACTIVE_REACHER_INTERVAL_MINUTES` | `30` | Ticker interval between analysis passes. |

Python side mirrors this with `PROACTIVE_ENABLED=true` (default), plus
`PROACTIVE_MIN_INTERVAL_HOURS` (12), `PROACTIVE_MAX_TOKENS` (700),
`PROACTIVE_TEMPERATURE` (0.4) in `miriam_agent/config/settings.py` — the analyst
endpoint stays fail-open: disabled feature, missing data, dead Go backend, or an
unreachable model all resolve to `should_reach_out: false`.

Runtime prerequisites (same as delegation):
- `PYTHON_AGENT_ENABLED=true`, `PYTHON_AGENT_URL`, and **aligned `JWT_SECRET`**
  (see "Secret alignment" above) — the reacher reuses the container's
  `PythonAgentClient`, which is only built under those conditions.
- A bridge wired for iMessage delivery (`PLATFORM_BRIDGE_BASE_URL` +
  `PLATFORM_BRIDGE_HMAC_SECRET`).
- At least one `platform_identities` row with `linked_at` set, and a last thread
  on that platform — otherwise nothing is sent (bridge silently skips).

> Local note: with no `platform_identities` rows / no iMessage thread in the dev
> database the worker runs but delivers nothing — expected until a handshake
> exists.