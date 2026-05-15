# Project Learnings

> Managed by `/learn`. Append-only — latest entry wins on conflicts.

## Patterns

## Pitfalls

## Preferences

## Architecture

### aws-ssm-runtime-config
- **Insight:** Production/staging config and secrets are stored in AWS SSM Parameter Store under `/rail/{env}/...` and injected into ECS as environment variables; the Go app reads those env vars via Viper rather than calling SSM directly.
- **Confidence:** 9/10
- **Source:** learn
- **Files:** infra/terraform/main.tf, infra/store-secrets.sh.example, infra/SETUP.md, internal/infrastructure/config/config.go
- **Date:** 2026-05-13

### reflect-user-wallet-yield
- **Insight:** Reflect yield must be minted and burned by each user's Circle Solana wallet; Reflect API keys are optional for public transaction-generation/rate endpoints and Rail treasury private-key sweeps should stay disabled by default.
- **Confidence:** 9/10
- **Source:** reflect-money-fix
- **Files:** internal/infrastructure/adapters/reflect/circle_deposit_router.go, internal/infrastructure/di/container.go, internal/infrastructure/config/config.go, infra/terraform/main.tf
- **Date:** 2026-05-13

## Tools
