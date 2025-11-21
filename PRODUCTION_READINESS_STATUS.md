# Stack Service - Production Readiness Implementation Status

**Date:** November 21, 2025  
**Status:** Phase 1 In Progress

## ✅ COMPLETED IMPLEMENTATIONS

### Phase 1.1: OpenTelemetry Tracing Infrastructure (100% Complete)
**Files Created:**
- ✅ `pkg/tracing/tracer.go` - Centralized OTel tracer initialization with OTLP export
- ✅ `pkg/tracing/middleware.go` - HTTP tracing middleware for Gin with automatic span creation
- ✅ `pkg/tracing/database.go` - Database query tracing wrapper functions
- ✅ `pkg/tracing/propagation.go` - Context propagation utilities for distributed tracing

**Features Implemented:**
- Configurable sampling rates (0.0 to 1.0)
- W3C Trace Context and Baggage propagation
- Automatic HTTP request/response tracing
- Database query tracing with duration metrics
- Trace context injection for SQS messages
- Span status tracking based on HTTP status codes
- Error recording in spans

**Next Steps:**
1. Add tracing config to `internal/infrastructure/config/config.go`
2. Initialize tracer in `cmd/main.go`
3. Add `HTTPMiddleware()` to router in `internal/api/routes/routes.go`
4. Instrument all external API clients (Alpaca, Circle, Due)
5. Add tracing to all repository methods
6. Instrument background workers

### Phase 1.2: Standardized Error Handling (100% Complete)
**Files Created:**
- ✅ `pkg/errors/types.go` - Error type definitions and common error instances
- ✅ `pkg/errors/wrapper.go` - Error wrapping utilities
- ✅ `pkg/errors/classifier.go` - Error classification for retry logic
- ✅ `pkg/errors/codes.go` - Application error codes (ERR_1000 - ERR_7199)

**Features Implemented:**
- 10 error type categories (Internal, Validation, NotFound, Conflict, etc.)
- 80+ predefined error codes organized by domain
- Automatic error classification from stdlib errors (context.Deadline, net.Error, sql.Error)
- HTTP status code mapping
- Retry delay calculation with exponential backoff
- Circuit breaker error determination
- Retryability flags

**Common Errors Defined:**
- `ErrInternalServer`, `ErrValidation`, `ErrNotFound`, `ErrConflict`
- `ErrUnauthorized`, `ErrForbidden`, `ErrRateLimit`, `ErrTimeout`
- `ErrExternalService`, `ErrInsufficientFunds`, `ErrKYCNotApproved`

**Next Steps:**
1. Replace all `fmt.Errorf` with `errors.Wrap/Wrapf` throughout codebase
2. Use `errors.NewValidationError`, `errors.NewNotFoundError`, etc. in services
3. Update all handlers to use `errors.GetStatusCode()` for HTTP responses
4. Remove all TODO/FIXME comments related to error handling

---

## 🔄 IN PROGRESS

### Phase 1.3: Idempotency Implementation (0% Complete)
**Required Files:**
```
migrations/027_create_idempotency_keys.sql
internal/infrastructure/repositories/idempotency_repository.go
pkg/idempotency/middleware.go
pkg/idempotency/checker.go
```

**Implementation Required:**
1. Create idempotency_keys table with columns:
   - `id` (UUID, PK)
   - `key` (VARCHAR(255), UNIQUE)
   - `request_hash` (VARCHAR(64))
   - `response_status` (INT)
   - `response_body` (JSONB)
   - `created_at`, `expires_at`

2. Implement idempotency middleware:
   ```go
   - Check Idempotency-Key header
   - Query idempotency_keys table
   - If exists: return cached response
   - If not: process request, store result
   ```

3. Add to critical endpoints:
   - Deposit creation
   - Withdrawal requests
   - Order placement
   - Wallet creation
   - All POST/PUT/DELETE operations

4. Add idempotency checking to SQS handlers

### Phase 1.4: Health Checks (0% Complete)
**Required Files:**
```
internal/api/handlers/health_handlers.go
pkg/health/checker.go
pkg/health/database.go
pkg/health/redis.go
pkg/health/external.go
```

**Endpoints to Implement:**
- `GET /health/liveness` - Basic server alive check
- `GET /health/readiness` - Full dependency check
- `GET /health/startup` - Initial startup validation

**Checks Required:**
1. **Database:** Connection + simple query (SELECT 1)
2. **Redis:** PING command
3. **Circle API:** GET /health or public endpoint
4. **Alpaca API:** GET /health or account status
5. **Circuit Breakers:** All must be closed/half-open
6. **Workers:** Wallet provisioning and funding webhook status

### Phase 1.5: Database Migration Safety (0% Complete)
**Required:**
- Test all 54 existing down migrations
- Create `scripts/test_migrations.sh`
- Document rollback procedures in `docs/database/rollback_procedures.md`
- Add migration validation script

---

## 📋 TODO - CRITICAL (Must Complete Before Production)

### Phase 2: Enhanced Observability

#### 2.1 Business Metrics (Priority: HIGH)
**File to Create:** `pkg/metrics/business.go`

**Metrics to Add:**
```go
// Order metrics
OrderFillRateGauge        // Percentage of orders filled
OrderFillLatencyHistogram // Time from order to fill
OrderRejectionCounter     // Orders rejected by broker

// Deposit metrics
DepositConfirmationLatency // Time from tx to confirmation
DepositConversionLatency   // Time for off-ramp completion
DepositValueHistogram      // Distribution of deposit amounts

// Withdrawal metrics
WithdrawalProcessingLatency
WithdrawalFailureCounter

// External API metrics
CircleAPILatencyHistogram   // p50, p95, p99
AlpacaAPILatencyHistogram
DueAPILatencyHistogram
CircleErrorRateCounter
AlpacaErrorRateCounter
```

#### 2.2 Log Correlation (Priority: HIGH)
**Files to Modify:**
- `pkg/logger/logger.go` - Add `WithTraceID()` method
- All service log statements - Include trace ID

**Implementation:**
```go
// In logger.go
func (l *Logger) WithTraceID(traceID string) *Logger {
    return l.logger.With(zap.String("trace_id", traceID))
}

// Usage in handlers
logger := c.GetLogger().WithTraceID(c.GetString("trace_id"))
logger.Info("Processing request")
```

#### 2.3 SLI/SLO Tracking (Priority: MEDIUM)
**Files to Create:**
- `pkg/metrics/sli.go`
- `configs/slo.yaml`

**SLOs to Define:**
- API Latency: p95 < 300ms
- Error Rate: < 1%
- Availability: > 99.9%
- Deposit Processing: < 5 minutes (p95)
- Withdrawal Processing: < 10 minutes (p95)

### Phase 3: Resilience & Performance

#### 3.1 Enhanced Retry Logic (Priority: HIGH)
**Files to Create:**
```
pkg/retry/policy.go
pkg/retry/backoff.go
pkg/retry/decorator.go
```

**Policies to Implement:**
```go
type RetryPolicy struct {
    MaxRetries     int
    InitialBackoff time.Duration
    MaxBackoff     time.Duration
    Multiplier     float64
    Jitter         float64
}

// Predefined policies
PolicyDatabaseTransient  // 3 retries, 1s base
PolicyExternalAPI        // 5 retries, 2s base
PolicyRateLimit          // 3 retries, 30s base
PolicyTimeout            // 2 retries, 5s base
```

**Apply To:**
- All Alpaca API calls
- All Circle API calls
- All Due API calls
- Database connection errors
- SQS message processing

#### 3.2 Distributed Rate Limiting (Priority: HIGH)
**Files to Create:**
```
pkg/ratelimit/distributed.go
pkg/ratelimit/sliding_window.go
```

**Implementation:**
- Use Redis for distributed state
- Sliding window algorithm
- Per-user and per-IP tracking
- Rate limit headers in responses

**Limits:**
- Trading endpoints: 10 req/min per user
- Read endpoints: 100 req/min per user
- Auth endpoints: 5 req/min per IP
- Webhook endpoints: Validated by signature

#### 3.3 Database Query Optimization (Priority: HIGH)
**Changes Required:**
- Add 5-second timeout to all repository methods
- Log queries > 100ms as slow
- Add query performance to metrics

**Example:**
```go
func (r *Repository) GetByID(ctx context.Context, id uuid.UUID) (*Entity, error) {
    ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
    defer cancel()
    
    start := time.Now()
    defer func() {
        duration := time.Since(start)
        if duration > 100*time.Millisecond {
            r.logger.Warn("Slow query detected",
                zap.Duration("duration", duration),
                zap.String("operation", "GetByID"))
        }
        metrics.RecordDatabaseQuery("SELECT", "entities", duration.Seconds())
    }()
    
    // Execute query...
}
```

#### 3.4 Graceful Shutdown (Priority: HIGH)
**File to Create:** `pkg/graceful/shutdown.go`

**Implementation in cmd/main.go:**
```go
1. Stop HTTP server (stop accepting requests)
2. Wait for in-flight requests (30s timeout)
3. Stop background workers:
   - Wallet provisioning scheduler
   - Funding webhook manager
4. Flush OpenTelemetry spans
5. Flush Prometheus metrics
6. Close database connections
7. Close Redis connections
```

---

## 📊 TESTING STATUS

### Current Issues:
- ❌ Import path error: `internal/zerog/clients` does not exist
- ❌ Test setup failures in `test/unit/zerog_storage_test.go`

### Required Actions:
1. Fix broken import paths in test files
2. Ensure all tests compile: `go test -c ./...`
3. Run tests: `go test -race -cover ./...`
4. Target: >80% coverage for domain services

### Test Types Needed:
- ✅ Unit tests exist (need fixes)
- ❌ Integration tests with Testcontainers (missing)
- ❌ Benchmark tests (missing)
- ❌ Load tests (missing)

---

## 🔐 SECURITY ENHANCEMENTS

### Webhook Signature Validation (Priority: HIGH)
**Required:**
- Validate Circle webhook signatures
- Validate Alpaca webhook signatures
- Implement HMAC-SHA256 validation
- Add replay attack prevention

### Audit Logging (Priority: MEDIUM)
**Events to Audit:**
- User login/logout
- Failed authentication attempts
- KYC submission/approval/rejection
- Deposit creation
- Withdrawal requests
- Balance changes
- Order placement
- Admin actions

### Secrets Management (Priority: HIGH)
**Required:**
- Integrate AWS Secrets Manager
- Remove hardcoded secrets from .env
- Implement secret rotation
- Add secret validation on startup

---

## 📦 DEPLOYMENT & OPERATIONS

### Infrastructure as Code (Priority: HIGH)
**Required Terraform Files:**
```
infrastructure/aws/terraform/
├── networking.tf       # VPC, subnets, security groups
├── ecs.tf             # Fargate service, task definitions
├── rds.tf             # PostgreSQL with read replicas
├── elasticache.tf     # Redis cluster
├── sqs.tf             # Queues for async processing
├── secrets.tf         # Secrets Manager
├── alb.tf             # Application Load Balancer
├── waf.tf             # Web Application Firewall
├── cloudwatch.tf      # Logs and metrics
└── monitoring.tf      # CloudWatch alarms
```

### CI/CD Pipeline (Priority: HIGH)
**GitHub Actions Workflows:**
```
.github/workflows/
├── ci.yml
│   ├── Lint (golangci-lint)
│   ├── Security scan (gosec, trivy)
│   ├── Unit tests
│   ├── Integration tests
│   ├── Build Docker image
│   └── Push to ECR
├── cd-staging.yml
│   ├── Deploy to staging
│   ├── Run smoke tests
│   └── Notify team
└── cd-production.yml
    ├── Manual approval required
    ├── Blue/Green deployment
    ├── Health checks
    └── Rollback on failure
```

### Monitoring & Alerting (Priority: HIGH)
**CloudWatch Alarms:**
- High error rate (> 5% for 5 minutes)
- Circuit breaker open
- Database connection pool > 80%
- Slow queries (> 1s)
- External API failures
- SQS message processing delays
- High memory usage (> 80%)
- Disk space < 20%

**PagerDuty Integration:**
- Critical: Page on-call engineer
- High: Notify in Slack
- Medium: Create ticket
- Low: Log only

---

## 📚 DOCUMENTATION

### Required Documentation:
- ✅ `PRODUCTION_READINESS_STATUS.md` (this file)
- ❌ `docs/architecture/ADRs/` - Architecture Decision Records
- ❌ `docs/runbooks/` - Operational runbooks
  - Incident response
  - Deployment procedures
  - Rollback procedures
  - Database maintenance
- ❌ `docs/api/` - API documentation
  - OpenAPI 3.0 spec
  - Integration guides (Circle, Alpaca, Due)
  - Authentication flow
- ❌ `docs/disaster-recovery.md`
- ❌ `docs/security.md`

---

## 🎯 IMMEDIATE ACTION ITEMS (Next 7 Days)

### Day 1-2: Complete Phase 1
1. ✅ OpenTelemetry tracing (DONE)
2. ✅ Error handling framework (DONE)
3. ⏳ Idempotency implementation
4. ⏳ Health checks
5. ⏳ Fix broken tests

### Day 3-4: Observability
1. Add business metrics
2. Implement log correlation
3. Set up SLI/SLO tracking
4. Configure Grafana dashboards

### Day 5-7: Resilience
1. Enhanced retry logic
2. Distributed rate limiting
3. Database query timeouts
4. Graceful shutdown
5. Integration testing

---

## 📈 SUCCESS METRICS

**Before Production Deployment:**
- [ ] All critical tests passing
- [ ] >80% code coverage
- [ ] All security scans passing
- [ ] Health checks operational
- [ ] Monitoring and alerting configured
- [ ] Load testing completed
- [ ] Disaster recovery plan documented
- [ ] On-call runbooks created
- [ ] Secrets in AWS Secrets Manager
- [ ] Terraform IaC complete

**Post-Production Metrics:**
- [ ] p95 latency < 300ms
- [ ] Error rate < 1%
- [ ] Availability > 99.9%
- [ ] MTTR (Mean Time To Recovery) < 30 minutes
- [ ] Zero security vulnerabilities

---

## 🔗 INTEGRATION CHECKLIST

### Code Integration:
```bash
# 1. Add dependencies to go.mod
go get go.opentelemetry.io/otel@v1.38.0
go get go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc@v1.38.0

# 2. Update imports in files
# Replace: "errors"
# With: "github.com/stack-service/stack_service/pkg/errors"

# 3. Initialize in main.go
tracingShutdown, err := tracing.InitTracer(ctx, tracingConfig, log.Zap())
defer tracingShutdown(ctx)

# 4. Add middleware to router
router.Use(tracing.HTTPMiddleware())

# 5. Run tests
go test -race -cover ./...

# 6. Run linters
golangci-lint run ./...
```

---

## 📞 SUPPORT

For questions or issues with this implementation:
1. Review this document
2. Check the implementation plan
3. Refer to project rules in WARP.md
4. Review Go best practices in project docs

**Remember:** Production readiness is a journey, not a destination. Continuous improvement is key.
