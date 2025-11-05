# Coding Standards

**Version:** v0.2  
**Last Updated:** October 24, 2025

## Navigation
- **Previous:** [Error Handling](./08-error-handling.md)
- **Next:** [Test Strategy](./10-test-strategy.md)
- **[Index](./README.md)**

---

## 12. Coding Standards

These standards are **MANDATORY** for all developers, including AI agents, working on the STACK backend. They are kept minimal to focus on project-specific consistency and critical rules, assuming general Go best practices are followed. These will guide code generation and review.

### 12.1 Core Standards
* **Languages & Runtimes:** **Go 1.21.x**. Follow standard Go formatting (`gofmt` / `goimports`).
* **Style & Linting:** Use `golangci-lint` with a pre-defined configuration (to be added to the repo). Enforce standard Go conventions (effective Go).
* **Test Organization:** Test files should be named `*_test.go` and reside in the same package/directory as the code being tested. Use Go's standard `testing` package.

### 12.2 Naming Conventions
* **Packages:** `lowercase`, short, concise names (e.g., `funding`, `adapters`, `persistence`).
* **Structs/Interfaces/Types:** `PascalCase`.
* **Variables/Functions:** `camelCase`. Exported functions/variables start with an uppercase letter.
* **Constants:** `PascalCase` or `UPPER_SNAKE_CASE` (be consistent within a module).
* **Acronyms:** Treat acronyms like `ID`, `URL`, `API` as single words in names (e.g., `userID`, `apiClient`, not `UserId`, `ApiClient`).

### 12.3 Critical Rules (For AI Agent Guidance)
* **Error Handling:**
    * **NEVER** ignore errors. Check every error returned by function calls.
    * Wrap errors with context using `fmt.Errorf("...: %w", err)` when propagating.
    * Define and use custom error types (e.g., `var ErrNotFound = errors.New("not found")`) for specific business logic failures.
* **Context Propagation:** Pass `context.Context` as the first argument to functions involved in requests or background tasks. Use it for deadlines, cancellation signals, and passing request-scoped values (like correlation IDs).
* **Database Access:** **MUST** use the Repository pattern defined in `internal/persistence/`. **NEVER** use `lib/pq` directly within core business logic (`internal/core/`).
* **External API Calls:** **MUST** go through the defined Adapters in `internal/adapters/`. **NEVER** make direct HTTP calls to external services from core business logic.
* **Configuration:** Access configuration values **ONLY** via the `internal/config/` package. **NEVER** read environment variables directly elsewhere.
* **Logging:** Use the configured **Zap** logger instance. **NEVER** use `fmt.Println` or `log.Println` for application logging. Include correlation ID from context.
* **Concurrency:** Use Go's standard concurrency primitives (goroutines, channels) carefully. Avoid race conditions (use `go run -race ./...` during development). Favor structured concurrency patterns where possible.
* **Secrets:** **NEVER** hardcode secrets (API keys, passwords). Retrieve them via the configuration system which loads from AWS Secrets Manager or environment variables. Do not log secrets.
* **Input Validation:** Validate **ALL** input at the API boundary (GraphQL resolvers / Gin handlers). Use a standard validation library if adopted, otherwise perform explicit checks.

---

**Next:** [Test Strategy](./10-test-strategy.md)
