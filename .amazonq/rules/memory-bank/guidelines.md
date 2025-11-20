# Development Guidelines

## Code Quality Standards

### Formatting and Structure
- Use Go standard formatting (gofmt) for all code
- Organize imports in three groups: standard library, external packages, internal packages
- Keep functions focused and concise (typically under 100 lines)
- Use meaningful variable names that describe purpose (e.g., `userID`, `providerRef`, `kycStatus`)
- Prefer early returns to reduce nesting depth
- Use blank lines to separate logical blocks within functions

### Naming Conventions
- **Packages**: lowercase, single word when possible (e.g., `handlers`, `entities`, `adapters`)
- **Files**: snake_case (e.g., `admin_handlers.go`, `email_service.go`, `onboarding_service.go`)
- **Types**: PascalCase (e.g., `EmailService`, `OnboardingFlow`, `KYCSubmission`)
- **Functions/Methods**: camelCase for private, PascalCase for exported (e.g., `sendEmail`, `CreateAdmin`)
- **Constants**: PascalCase or UPPER_SNAKE_CASE for package-level (e.g., `resendAPIBaseURL`, `kycRequiredFeatures`)
- **Interfaces**: Typically end with "Service", "Repository", or "Provider" (e.g., `WalletService`, `UserRepository`, `KYCProvider`)

### Documentation
- Add package-level comments for all packages
- Document all exported types, functions, and methods with godoc-style comments
- Include parameter descriptions and return value explanations for complex functions
- Use inline comments sparingly, only when logic is non-obvious
- Document business logic decisions and edge cases

## Architectural Patterns

### Dependency Injection
- Use constructor functions that accept dependencies as parameters
- Return concrete types from constructors, accept interfaces as parameters
- Store dependencies as private struct fields
- Example pattern:
```go
type Service struct {
    repo Repository
    logger *zap.Logger
}

func NewService(repo Repository, logger *zap.Logger) *Service {
    return &Service{
        repo: repo,
        logger: logger,
    }
}
```

### Repository Pattern
- Define repository interfaces in `internal/domain/repositories/`
- Implement repositories in `internal/infrastructure/repositories/`
- Use context.Context as first parameter for all repository methods
- Return domain entities, not database models
- Handle database errors and convert to domain errors

### Service Layer Pattern
- Business logic resides in service layer (`internal/domain/services/`)
- Services orchestrate between repositories and external adapters
- Services validate input and enforce business rules
- Services handle transaction boundaries
- Services log important business events

### Handler Pattern
- HTTP handlers in `internal/api/handlers/`
- Handlers are thin, delegating to services
- Handlers validate HTTP-specific concerns (headers, query params)
- Handlers transform between HTTP and domain models
- Use helper functions to create handler instances with dependencies

## Error Handling

### Error Creation and Wrapping
- Use `fmt.Errorf` with `%w` verb to wrap errors with context
- Add contextual information when wrapping: `fmt.Errorf("failed to create user: %w", err)`
- Check for specific errors using `errors.Is()` for sentinel errors
- Use `errors.As()` for error type assertions

### Error Logging
- Log errors at the point where they're handled, not where they're created
- Use structured logging with zap: `logger.Error("message", zap.Error(err), zap.String("key", value))`
- Include relevant context (user IDs, request IDs, etc.) in error logs
- Use appropriate log levels: Error for failures, Warn for recoverable issues, Info for normal operations

### HTTP Error Responses
- Return consistent error response format: `{"error": "ERROR_CODE", "message": "Human readable message"}`
- Use appropriate HTTP status codes (400 for validation, 404 for not found, 500 for server errors)
- Don't expose internal error details to clients
- Log full error details server-side before returning sanitized response

## Testing Patterns

### Test Organization
- Unit tests in `test/unit/` directory
- Integration tests in `test/integration/` directory
- Use build tags for integration tests: `//go:build integration`
- Name test files with `_test.go` suffix
- Group related tests using subtests with `t.Run()`

### Test Structure
- Follow Arrange-Act-Assert pattern
- Use testify/require for assertions that should stop test execution
- Use testify/assert for assertions that should continue test execution
- Create mock implementations for interfaces
- Use table-driven tests for testing multiple scenarios

### Mock Patterns
- Create mock structs that implement interfaces
- Track method calls with boolean flags (e.g., `CreateCalled`)
- Store test data in maps for retrieval
- Return appropriate test data based on input parameters
- Example:
```go
type MockRepository struct {
    CreateCalled bool
    items map[string]*Entity
}

func (m *MockRepository) Create(ctx context.Context, entity *Entity) error {
    m.CreateCalled = true
    if m.items == nil {
        m.items = make(map[string]*Entity)
    }
    m.items[entity.ID.String()] = entity
    return nil
}
```

## Database Patterns

### Query Construction
- Use parameterized queries with `$1, $2, ...` placeholders
- Build dynamic queries using `strings.Builder` for complex filtering
- Keep track of parameter index when building dynamic queries
- Use `RETURNING` clause to get inserted/updated data in single query
- Apply pagination with `LIMIT` and `OFFSET`

### Transaction Handling
- Use context with timeout for all database operations
- Defer context cancellation: `defer cancel()`
- Handle `sql.ErrNoRows` separately from other errors
- Use `sql.NullTime`, `sql.NullString` for nullable columns
- Scan results immediately after query execution

### Connection Management
- Configure connection pool settings (max open, max idle, lifetime)
- Use prepared statements for repeated queries
- Close rows with `defer rows.Close()`
- Check for errors after iterating rows: `if err := rows.Err(); err != nil`

## Configuration Management

### Configuration Structure
- Define configuration structs with mapstructure tags
- Group related settings into nested structs
- Use environment variables for sensitive values
- Provide sensible defaults using `viper.SetDefault()`
- Validate required configuration on startup

### Environment Variable Overrides
- Support both config file and environment variable configuration
- Use consistent naming: `SECTION_SUBSECTION_KEY` format
- Override config file values with environment variables
- Document all environment variables in README

## Logging Standards

### Structured Logging with Zap
- Use structured logging exclusively (no string formatting in log messages)
- Include context fields: `zap.String("userId", id)`, `zap.Error(err)`
- Use appropriate log levels:
  - Debug: Detailed diagnostic information
  - Info: General informational messages
  - Warn: Warning messages for recoverable issues
  - Error: Error messages for failures
- Log at decision points and state transitions
- Include timing information for performance-critical operations

### Log Message Format
- Start with action verb: "Starting", "Processing", "Failed to", "Completed"
- Be specific and actionable: "Failed to send email" not "Error occurred"
- Include relevant identifiers in structured fields
- Don't log sensitive information (passwords, tokens, PII)

## API Design Patterns

### Request/Response Models
- Define request/response structs in `internal/domain/entities/`
- Use JSON struct tags for serialization
- Validate request data before processing
- Use pointer fields for optional values
- Return consistent response structures

### Handler Implementation
- Create handler factory functions that return `gin.HandlerFunc`
- Extract and validate path parameters first
- Parse and validate request body
- Set context timeout for operations
- Return early on validation failures
- Use helper methods for common operations

### Middleware Usage
- Apply authentication middleware to protected routes
- Use rate limiting middleware for public endpoints
- Add request logging middleware for observability
- Implement CORS middleware for cross-origin requests
- Chain middleware in logical order

## Security Best Practices

### Authentication and Authorization
- Use JWT tokens for authentication
- Store tokens securely (httpOnly cookies or secure storage)
- Validate tokens on every protected request
- Check user roles/permissions before operations
- Hash passwords using bcrypt with appropriate cost
- Encrypt sensitive data at rest using AES-256-GCM

### Input Validation
- Validate all user input at API boundary
- Sanitize input to prevent injection attacks
- Use allowlists rather than denylists
- Validate data types, formats, and ranges
- Trim and normalize string inputs

### Sensitive Data Handling
- Never log passwords, tokens, or API keys
- Encrypt sensitive data before database storage
- Use environment variables for secrets
- Rotate credentials regularly
- Implement audit logging for sensitive operations

## External Service Integration

### HTTP Client Patterns
- Create dedicated client structs for external services
- Set appropriate timeouts (typically 30 seconds)
- Use context for cancellation and timeouts
- Implement retry logic with exponential backoff
- Log request/response details for debugging

### Circuit Breaker Pattern
- Use circuit breaker for external service calls
- Configure failure thresholds and timeout periods
- Implement fallback behavior when circuit is open
- Monitor circuit breaker state changes
- Reset circuit after successful operations

### Webhook Handling
- Verify webhook signatures before processing
- Use idempotency keys to prevent duplicate processing
- Process webhooks asynchronously when possible
- Return 200 OK quickly, process in background
- Implement retry logic for failed webhook processing

## Code Organization

### Package Structure
- Group by feature/domain, not by layer
- Keep related code together
- Minimize dependencies between packages
- Use internal/ for private packages
- Export only what's necessary

### File Organization
- One primary type per file
- Group related functions with their types
- Keep files focused and cohesive (typically under 500 lines)
- Use separate files for tests
- Name files after primary type or functionality

## Performance Considerations

### Database Optimization
- Use indexes for frequently queried columns
- Batch operations when possible
- Avoid N+1 query problems
- Use connection pooling
- Monitor slow queries

### Caching Strategy
- Cache frequently accessed, rarely changing data
- Use Redis for distributed caching
- Set appropriate TTLs for cached data
- Implement cache invalidation strategy
- Monitor cache hit rates

### Concurrency Patterns
- Use goroutines for independent operations
- Use channels for communication between goroutines
- Implement graceful shutdown for background workers
- Use sync.WaitGroup for coordinating goroutines
- Avoid shared mutable state

## Common Code Idioms

### Context Usage
- Always pass context as first parameter
- Create context with timeout for operations: `context.WithTimeout(ctx, 5*time.Second)`
- Defer context cancellation: `defer cancel()`
- Check context cancellation in long-running operations
- Propagate context through call chain

### UUID Handling
- Use `github.com/google/uuid` for UUID generation
- Generate UUIDs with `uuid.New()`
- Parse UUIDs with `uuid.Parse()` and handle errors
- Store UUIDs as uuid.UUID type, not strings
- Convert to string only for serialization

### Time Handling
- Use `time.Now().UTC()` for timestamps
- Store times in UTC in database
- Use `time.Time` type, not strings
- Handle nullable times with `sql.NullTime`
- Format times consistently for API responses

### Decimal Arithmetic
- Use `github.com/shopspring/decimal` for financial calculations
- Never use float64 for money amounts
- Create decimals with `decimal.NewFromFloat()` or `decimal.NewFromString()`
- Use decimal methods for arithmetic operations
- Convert to string for JSON serialization
