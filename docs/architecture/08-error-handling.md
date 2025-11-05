# Error Handling Strategy

**Version:** v0.2  
**Last Updated:** October 24, 2025

## Navigation
- **Previous:** [Infrastructure](./07-infrastructure.md)
- **Next:** [Coding Standards](./09-coding-standards.md)
- **[Index](./README.md)**

---

## 11. Error Handling Strategy

A consistent error handling strategy is crucial for maintainability and observability, especially in a system involving multiple external partners. We will leverage Go's standard error handling mechanisms combined with structured logging and resilience patterns.

### 11.1 General Approach
* **Error Model:** Go's standard `error` interface. Errors should be wrapped with context as they propagate up the call stack (e.g., using `fmt.Errorf("operation failed: %w", err)` or a library like `pkg/errors`).
* **Exception Hierarchy:** Define custom error types (structs implementing the `error` interface) for specific domain errors (e.g., `ErrInsufficientFunds`, `ErrKYCFailed`, `ErrBrokerOrderRejected`) to allow for programmatic handling. Standard library errors (e.g., `io.EOF`, `sql.ErrNoRows`) should be handled appropriately.
* **Error Propagation:** Functions should return errors rather than panicking. The top-level handlers (e.g., Gin middleware, GraphQL resolvers) will be responsible for catching errors, logging them, and returning appropriate responses to the client.

### 11.2 Logging Standards
* **Library:** **Zap** (uber-go/zap).
* **Format:** Structured JSON format for easy parsing by log aggregators (e.g., CloudWatch Logs Insights, Datadog).
* **Levels:** Use standard levels (Debug, Info, Warn, Error, Fatal, Panic). Default level in production should be Info, configurable via environment variables.
* **Required Context:**
    * **Correlation ID:** Automatically injected into the `context.Context` (e.g., via middleware) and included in every log message. This ID should ideally originate from the incoming request or be generated for background jobs. Trace IDs from **OpenTelemetry** can serve this purpose.
    * **Service Context:** Module name (e.g., "FundingService", "InvestingService").
    * **User Context:** User ID (if available and relevant, taking care not to log PII unnecessarily).
    * **Error Details:** Include the full wrapped error string and potentially a stack trace for unexpected errors (Error level and above).

### 11.3 Error Handling Patterns
* **External API Errors (Circle, Alpaca, etc.):**
    * **Retry Policy:** Implement configurable exponential backoff retries for transient network errors or specific idempotent API calls.
    * **Circuit Breaker:** Use **gobreaker** for critical external dependencies (especially Alpaca trading, Circle on/off-ramps) to prevent cascading failures. Monitor breaker state (open, half-open, closed).
    * **Timeout Configuration:** Configure appropriate timeouts for all external HTTP requests.
    * **Error Translation:** Translate partner-specific error codes/messages into standardized internal error types for consistent handling. Log the original partner error for debugging.
* **Business Logic Errors:**
    * **Custom Errors:** Use the defined custom error types (e.g., `ErrInsufficientFunds`) to signal specific business rule violations.
    * **User-Facing Errors:** API Gateway/GraphQL layer translates internal errors into user-friendly messages. Avoid exposing internal details. Return standard GraphQL error responses.
    * **Error Codes:** Consider defining a simple internal error code system for easier frontend handling and monitoring, mapped from internal error types.
* **Data Consistency:**
    * **Transaction Strategy:** Use database transactions (`lib/pq`) for operations requiring atomicity (e.g., updating order status and positions).
    * **Compensation Logic (Sagas):** For multi-step asynchronous flows (Funding/Withdrawal involving SQS), implement compensating actions to revert steps in case of failure (e.g., if Alpaca funding fails after Circle off-ramp, flag for manual review or attempt refund via Circle).
    * **Idempotency:** Ensure message handlers (SQS) and critical API endpoints are idempotent (can be safely called multiple times with the same input) to handle retries and message duplication.

---

**Next:** [Coding Standards](./09-coding-standards.md)
