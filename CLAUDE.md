# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

Rail is a production fintech backend (Go) — a "rules-based capital engine". Every deposit (NGN/GBP/USD/EUR fiat or USDC crypto) is automatically split **70% to a spend wallet / 30% to a yield-bearing stash** the moment it clears. There is no user configuration of the split. Real users, real money, real custody — treat correctness around money, ledgers, and external provider calls as safety-critical.

See `README.md` for product/domain context and `AGENTS.md` for the contributor-facing summary (commands, style, security). This file assumes both and focuses on what speeds up code work.

## Commands

```bash
make build          # build bin/rail_service (CGO_ENABLED=0)
make run            # go run cmd/main.go — start API locally
make dev            # docker-compose up deps, migrate-up, then run
make test           # full suite: go test -v -race -coverprofile=coverage.out ./...
make test-coverage  # coverage.out + coverage.html
make lint           # golangci-lint run ./...
make security-scan  # gosec + trivy (run if you touch auth/payments/webhooks)
make migrate-up     # go run cmd/main.go migrate
```

Run a single test / package during development:

```bash
go test ./internal/domain/services/allocation/...        # one package
go test -run TestName ./internal/...                     # one test by name
go test -v ./test/integration/... -race                  # integration (needs Postgres + Redis)
```

Integration tests expect PostgreSQL 15 and Redis 7 (use `make dev` / `docker-compose up -d`).

## Architecture

Clean/hexagonal layering under `internal/`. The dependency direction is strict — keep it:

- `internal/api/` — Gin HTTP layer: `handlers/`, `middleware/`, `routes/`, plus `graphql/`. Handlers must stay thin; no business logic.
- `internal/domain/` — the core. `entities/`, `repositories/` (interfaces only), `services/` (business logic, ~70 service packages e.g. `allocation`, `autoinvest`, `funding`, `spending`, `yield`, `copytrading`, `kyc`). Domain code must not import infrastructure.
- `internal/infrastructure/` — `adapters/` (external provider clients), `repositories/` (DB implementations of domain interfaces), `database/`, `cache/`, `config/`, `di/`, `ai/`, `supermemory/`.
- `internal/workers/` — ~45 background job processors (allocation, autoinvest, yield_distribution, reconciliation, copy_trading, *_recovery, etc.). Anything that can't block the request path lives here.
- `internal/app/application.go` — the composition root: `Initialize()` → `initializeWorkers()` / `initializeServer()` → `Start()` / `WaitForShutdown()` / `Shutdown()`.
- `pkg/` — reusable, app-agnostic packages (auth, crypto, ratelimit, retry, circuitbreaker, idempotency, jobqueue, metrics, tracing, webhook, …).

### Wiring (DI)

Dependencies are assembled by hand in `internal/infrastructure/di/container.go` (large file, ~5k lines) and `builders.go`/`*_adapters.go` alongside it. There is no DI framework — adding a service means wiring it through the container and exposing it where handlers/routes need it. Routes are registered via `routes.SetupRoutes(app.container)` (see the many `Register*Routes`/`Setup*Routes` functions in `internal/api/routes/`).

### Entry point & run modes

`cmd/main.go` is the only binary. Subcommands:
- `migrate` — runs DB migrations, then a one-shot Bridge→Circle wallet migration (no-op once `circle_wallet_id` is set).
- `--health-check` — used by Docker healthcheck.
- (no args) — boots the full application.

### Migrations

`migrations/` holds 400+ numbered `NNN_name.up.sql` / `.down.sql` pairs. Migrations are run from the **`migrations/` directory on disk at runtime** (file-based via golang-migrate, not embedded) — the working directory matters when running `migrate`. The runner has dirty-state recovery. New migration = next sequential number with both up and down files.

### Configuration

`config.Load()` (`internal/infrastructure/config/config.go`) uses godotenv + viper: loads `.env` (if present), reads `configs/config.yaml`, then `AutomaticEnv()` overrides with `.`→`_` key replacement (e.g. `server.port` ← `SERVER_PORT`). Env vars win over the YAML file. Start from `.env.example` / `configs/config.yaml`; never commit a real `.env`.

## External providers (current state)

The README lists Bridge for wallets/cards, but the codebase is mid-migration to a broader set. Check `internal/infrastructure/adapters/` for what actually exists before assuming a provider: `bridge`, `circle` (wallets — migration target), `alpaca` (brokerage), `reflect` (yield), `paj`/`chainrails`/`cctp` (rails/bridging), `didit`/`sumsub` (KYC), `umbra` (privacy, has a separate Node sidecar in `umbra-sidecar/`), `r2` (storage). When integrating a provider, follow the adapter pattern: client in `adapters/<provider>/`, exposed to domain via an interface defined in `internal/domain/repositories` or a service interface.

## The umbra-sidecar

`umbra-sidecar/` is a separate TypeScript/Node service (`@umbra-privacy/sdk` + `@solana/kit`, Express) — not part of the Go build. Scripts: `bun install`, `npm run dev` (tsx), `npm run build` (tsc) → `npm run start`. Only touch it for Umbra privacy-wallet work.

## Conventions

- Go 1.25 (toolchain), `gofmt` is authoritative — don't hand-tune spacing.
- `golangci-lint` enforces (among others) `errcheck` with `check-blank` + `check-type-assertions`, `gosec`, `bodyclose`, `sqlclosecheck`, `noctx`, and `gocyclo` min-complexity 15. Run `make lint` before a PR.
- Tests are package-local `*_test.go` plus `test/{unit,integration,performance}`; prefer table-driven, cover failure paths, use `testify` where it helps.
- Commits: imperative subjects with `feat:`/`fix:`/`infra:`/`security:`/`perf:`/`chore:` prefixes (match recent history).
- Money/ledger code is double-entry; reconciliation and recovery workers exist to catch drift. Don't introduce paths that mutate balances outside the ledger/service layer.
