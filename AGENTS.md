# Repository Guidelines

## Project Structure & Module Organization

This repository is a Go backend service. Entry points live in `cmd/`, with `cmd/main.go` as the main server. Core code is under `internal/`: `internal/api` for handlers, middleware, and routes; `internal/domain` for entities and services; `internal/infrastructure` for database, config, and external adapters; and `internal/workers` for background jobs. Shared packages belong in `pkg/`. Migrations live in `migrations/`, config in `configs/`, scripts in `scripts/`, and deployment assets in `deployments/` and `infra/`. Tests live in package-local `*_test.go` files and in `test/` (`test/unit`, `test/integration`, `test/performance`).

## Build, Test, and Development Commands

- `make build` — build `bin/rail_service`.
- `make run` — start the API locally from `cmd/main.go`.
- `make dev` — boot local dependencies with Docker, run migrations, then start the app.
- `make test` — run the full Go test suite with race detection and coverage.
- `make test-coverage` — generate `coverage.out` and `coverage.html`.
- `make lint` — run `golangci-lint`.
- `make security-scan` — run `gosec` and `trivy`.
- `make migrate-up` — apply database migrations.

## Coding Style & Naming Conventions

Target Go `1.24`. Format all Go code with `gofmt`; do not manually tune spacing. Keep package names lowercase, exported identifiers in `CamelCase`, unexported identifiers in `camelCase`, and test files named `*_test.go`. Follow the existing layers: domain logic stays out of handlers, and infrastructure concerns stay out of `internal/domain`. Run `golangci-lint run ./...` before opening a PR.

## Testing Guidelines

Use Go’s `testing` package with `testify` where helpful. Prefer table-driven tests and cover success and failure paths. Run targeted tests during development, for example `go test ./internal/...` or `go test ./test/integration/... -v`. Integration tests expect PostgreSQL and Redis; CI uses Postgres 15 and Redis 7. Keep coverage on changed code meaningful, and update `test/README.md` when you add notable scenarios.

## Commit & Pull Request Guidelines

Recent history follows imperative subjects with prefixes such as `feat:`, `fix:`, `infra:`, `security:`, `perf:`, and `chore:`. Keep commits focused and avoid vague messages like `commit`. PRs should include: a concise summary, linked issue or task, test evidence (`make test` or targeted package tests), and notes for migrations, config changes, or new environment variables. For API changes, include example request/response payloads.

## Security & Configuration Tips

Never commit live secrets or edited `.env` files. Start from `.env.example` and config under `configs/`. Prefer secret managers and environment variables for credentials. If you touch auth, payments, or webhooks, run `make security-scan` before review.
