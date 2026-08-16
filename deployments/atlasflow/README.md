# AtlasFlow Deployment Guide

Rail deploys to [AtlasFlow](https://atlasflow.com) — a hardware-isolated VM platform
that builds from GitHub and auto-deploys on every push. This guide covers deploying
all Rail services: the Go backend, TypeScript spectrum bridge, and Python enrichment
and OCR sidecars.

## Architecture Overview

```
┌─────────────────────────────────────────────────────────────┐
│                     AtlasFlow Deployments                     │
│                                                               │
│  ┌──────────────┐   ┌──────────────┐                          │
│  │  rail-api    │   │ spectrum-    │                          │
│  │  (Go)        │   │ bridge       │                          │
│  │  :8080       │   │ (Bun/TS)     │                          │
│  │  public      │   │  :3000       │                          │
│  └──────┬───────┘   └──────┬───────┘                          │
│         │                  │                                   │
│         │ calls            │ AMQP                              │
│         ▼                  ▼                                   │
│  ┌──────────────┐   ┌──────────────┐                          │
│  │ enrichment   │   │ RabbitMQ     │ ← external (CloudAMQP)   │
│  │ (Python)     │   └──────────────┘                          │
│  │  :8090       │                                              │
│  └──────────────┘                                              │
│  ┌──────────────┐                                              │
│  │ ocr          │                                              │
│  │ (Python)     │                                              │
│  │  :8091       │                                              │
│  └──────────────┘                                              │
└─────────────────────────────────────────────────────────────┘

External managed services (NOT on AtlasFlow):
  • PostgreSQL  — AWS RDS / Supabase / Neon
  • Redis       — Upstash / AWS ElastiCache
  • RabbitMQ    — CloudAMQP / AWS MQ
```

## Prerequisites

### 1. Install the AtlasFlow CLI

```bash
curl -fsSL https://atlasflow.com/install.sh | sh
```

### 2. Get your API key

1. Log in to the [AtlasFlow dashboard](https://atlasflow.com)
2. Go to **Settings → API Keys**
3. Click **Create API Key** (starts with `af_live_...`)
4. Store it securely — it won't be shown again

### 3. Log in

```bash
atlasflow login --key af_live_YOUR_KEY_HERE
atlasflow whoami   # verify: should show your workspace
```

### 4. Connect GitHub

```bash
atlasflow github connect
# This prints a URL — open it and install the AtlasFlow GitHub App
# on your organization/repo (e.g. your-org/RAIL_BACKEND)

atlasflow github repos   # verify the repo is visible
```

## Live workspace (do not invent new names)

| | |
|---|---|
| AtlasFlow workspace | `tobi-omotade-2cd167ac` |
| GitHub repo (GitHub App is installed here) | `tobi-techy/RAIL-BACKEND-SERVICE` |
| Go API project (already RUNNING) | `rail-backend-service` — **not** `rail-api` |
| Public API | `https://api.userail.money` |
| AtlasFlow API URL | `https://rail-backend-service-tobi-omotade-2cd167ac.atlasflow.dev` |

Sidecar URL pattern: `https://{project}-{workspace}.atlasflow.dev`.

## Services to Deploy

Each service is a separate AtlasFlow project, all pointing to the same
GitHub repo but with different root directories and Dockerfiles:

| # | Project slug | Root | Dockerfile | Port | Health | Runtime Tier | Status |
|---|---|---|---|---|---|---|---|
| 1 | `rail-backend-service` | `/` | `Dockerfile` | 8080 | `/` and `/health` | medium | **Live** |
| 2 | `spectrum-bridge` | `cmd/spectrum-bridge/` | `cmd/spectrum-bridge/Dockerfile` | 3000 | `/` and `/health` | small | Create + set Spectrum/AMQP/HMAC **before** first deploy |
| 3 | `rail-enrichment` | `/services/enrichment` | `Dockerfile` | 3000 (AtlasFlow) / 8090 local | `/` and `/health` | small | Create; no secrets |
| 4 | `rail-ocr` | `/services/ocr` | `Dockerfile` | 3000 (AtlasFlow) / 8091 local | `/` and `/health` | large | Create; no secrets |

There is **no separate Miriam service**. Miriam is the Go API (`rail-backend-service`) plus the spectrum bridge (iMessage) plus optional enrichment/OCR sidecars.

## Step-by-Step Deployment

### Step 1: Create AtlasFlow Projects

```bash
# Go API already exists as rail-backend-service — do not create rail-api.

REPO=tobi-techy/RAIL-BACKEND-SERVICE

# Spectrum bridge (TypeScript/Bun)
atlasflow projects create --name spectrum-bridge \
  --repo "$REPO" \
  --root /cmd/spectrum-bridge \
  --dockerfile Dockerfile

# Transaction enrichment (Python)
atlasflow projects create --name rail-enrichment \
  --repo "$REPO" \
  --root /services/enrichment \
  --dockerfile Dockerfile

# OCR service (Python/PaddleOCR)
atlasflow projects create --name rail-ocr \
  --repo "$REPO" \
  --root /services/ocr \
  --dockerfile Dockerfile
```

### Step 2: Configure Runtime Tiers

The OCR service needs the `large` tier (4 vCPU / 8 GB) for PaddleOCR:

```bash
# Current workspace plan only allows build-tier "standard".
atlasflow projects environments update rail-ocr production --runtime-tier large
atlasflow projects environments update rail-backend-service production --runtime-tier medium
atlasflow projects environments update spectrum-bridge production --runtime-tier small
atlasflow projects environments update rail-enrichment production --runtime-tier small
```

### Step 3: Set Environment Variables

Set env vars for each project in the AtlasFlow dashboard (or via CLI/API).
See `env-vars-reference.md` for the complete list per service.

**Critical: set these BEFORE triggering the first deployment.**

#### rail-backend-service (most env vars)

Set in AtlasFlow dashboard → rail-backend-service → production → Environment Variables.
Miriam lives here — `CENCORI_API_KEY` + `PLATFORM_*` are required for the agent.

```
ENVIRONMENT=production
PORT=8080
GIN_MODE=release
DATABASE_URL=postgresql://user:pass@your-rds-host:5432/rail_service
REDIS_HOST=your-redis-host.upstash.io
REDIS_PORT=6379
REDIS_PASSWORD=your-redis-password
REDIS_TLS=true
JWT_SECRET=<32+ char random string>
ENCRYPTION_KEY=<32 byte hex key>
CENCORI_API_KEY=your-cencori-api-key
CIRCLE_API_KEY=your-circle-api-key
CIRCLE_ENTITY_SECRET=your-circle-entity-secret
CIRCLE_ENVIRONMENT=production
BRIDGE_API_KEY=your-bridge-api-key
BRIDGE_ENVIRONMENT=production
PLATFORM_ENABLED=true
PLATFORM_AMQP_URL=amqps://user:pass@your-rabbitmq-host/vhost
PLATFORM_BRIDGE_HMAC_SECRET=your-hmac-secret
PLATFORM_BRIDGE_BASE_URL=https://spectrum-bridge-tobi-omotade-2cd167ac.atlasflow.dev
ENRICHMENT_SERVICE_URL=https://rail-enrichment-tobi-omotade-2cd167ac.atlasflow.dev
DOCUMENT_OCR_SERVICE_URL=https://rail-ocr-tobi-omotade-2cd167ac.atlasflow.dev
DOCUMENT_ENABLE_PYTHON_OCR=true
...
```

See `env-vars-reference.md` for the full list.

#### spectrum-bridge

```
SPECTRUM_PROJECT_ID=your-spectrum-project-id
SPECTRUM_PROJECT_SECRET=your-spectrum-project-secret
AMQP_URL=amqps://user:pass@your-rabbitmq-host/vhost
AMQP_EXCHANGE=miriam
RAIL_HMAC_SECRET=your-hmac-secret  # must match rail-api's PLATFORM_BRIDGE_HMAC_SECRET
BRIDGE_PORT=3000
NODE_ENV=production
# Optional: Telegram
TELEGRAM_BOT_TOKEN=
# Optional: WhatsApp
WHATSAPP_ACCESS_TOKEN=
WHATSAPP_PHONE_NUMBER_ID=
WHATSAPP_APP_SECRET=
```

#### rail-enrichment

No special env vars needed — it's a stateless ML inference service.

#### rail-ocr

No special env vars needed — it's a stateless OCR service.

### Step 4: Deploy

Push to the `main` branch to trigger automatic deployments, or deploy manually:

```bash
# Sidecars (no secrets). Spectrum only after dashboard secrets are set.
atlasflow deployments create --project rail-enrichment
atlasflow deployments create --project rail-ocr
atlasflow deployments create --project spectrum-bridge
# Go API already auto-deploys on push to main
atlasflow deployments create --project rail-backend-service   # only if you need a manual roll
```

The `create` command waits by default, streams build logs, and prints the URL:
```
https://rail-enrichment-tobi-omotade-2cd167ac.atlasflow.dev
```

### Step 5: Run Database Migrations

After the first successful deployment of `rail-api`, run migrations:

```bash
# Option A: AtlasFlow exec (if supported) — run the migrate subcommand
atlasflow deployments exec <deployment-id> -- /main migrate

# Option B: Run locally with the production DATABASE_URL
DATABASE_URL=postgresql://user:pass@your-rds-host:5432/rail_service \
  go run cmd/main.go migrate
```

### Step 6: Verify

```bash
# Check health
curl https://api.userail.money/health
curl https://spectrum-bridge-tobi-omotade-2cd167ac.atlasflow.dev/health
curl https://rail-enrichment-tobi-omotade-2cd167ac.atlasflow.dev/health
curl https://rail-ocr-tobi-omotade-2cd167ac.atlasflow.dev/health

# Check deployment status
atlasflow deployments list --project rail-backend-service
```

## External Infrastructure Setup

### PostgreSQL (AWS RDS / Supabase / Neon)

1. Create a managed PostgreSQL 15+ instance
2. Create database `rail_service`
3. Run `scripts/init-db.sql` for extensions
4. Set `DATABASE_URL` in rail-api env vars

### Redis (Upstash / ElastiCache)

1. Create a managed Redis 7+ instance
2. Set `REDIS_HOST`, `REDIS_PORT`, `REDIS_PASSWORD`, `REDIS_TLS=true` in rail-api env vars

### RabbitMQ (CloudAMQP / AWS MQ)

1. Create a managed RabbitMQ instance
2. Set `PLATFORM_AMQP_URL` in both rail-api and spectrum-bridge env vars
3. Set `AMQP_URL` in spectrum-bridge (same connection string)

## Custom Domains

Add custom domains in the AtlasFlow dashboard (TLS is automatic):

| Service | Domain | AtlasFlow Project |
|---|---|---|
| API | `api.userail.money` | `rail-backend-service` (already verified) |
| Bridge webhook | `bridge.userail.money` | `spectrum-bridge` |

Point CNAME records to the AtlasFlow-provided target.

## Logs and Monitoring

```bash
# Runtime logs
atlasflow deployments logs <deployment-id> --tail 500

# Build logs
atlasflow deployments logs <deployment-id> --type build --tail 500

# JSON output for scripting
atlasflow deployments list --project rail-api --json
```

## Automation

Use `deploy.sh` to create all projects and set runtime tiers in one command:

```bash
cd deployments/atlasflow
REPO=tobi-techy/RAIL-BACKEND-SERVICE ./deploy.sh
```

## Notes

- **Auto-deploy**: Every push to `main` triggers a new deployment for each
  project. Only the project whose code changed will rebuild (AtlasFlow detects
  path-level changes).
- **Health checks**: AtlasFlow probes `http://<vm>:<port>/` every 15s (port
  from `PORT`, default 3000). All four services now answer `/` and `/health`.
- **Replicas**: `rail-backend-service` should run `--min-replicas 2` once
  worker leader election is live (`WORKERS_LEADER_ELECTION`, on by default in
  production). Only the Redis lock holder runs crons. Leave `spectrum-bridge`
  at 1 (local `spaces.json`). Enrichment and OCR stay at 1 until queue depth
  says otherwise.
- **Staging**: `atlasflow projects environments create rail-backend-service --slug staging --branch staging`
- **OCR warm-up**: PaddleOCR warms its model cache at Docker build time. First
  request after deploy may take a few seconds; subsequent requests are fast.
- **Secrets**: Never commit real secrets to the repo. Set all secrets as
  environment variables in the AtlasFlow dashboard. The `.env.example` file
  documents every variable.
