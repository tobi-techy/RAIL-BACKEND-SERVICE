# Quick Start Integration Guide

## ✅ What's Been Implemented (Ready to Use)

### 1. OpenTelemetry Distributed Tracing
**Location:** `pkg/tracing/`
**Files:**
- `tracer.go` - Tracer initialization
- `middleware.go` - HTTP tracing middleware
- `database.go` - Database query tracing
- `propagation.go` - Context propagation

**Integration Steps:**

```go
// 1. In cmd/main.go, add after config loading:
import "github.com/stack-service/stack_service/pkg/tracing"

// Initialize tracer
tracingConfig := tracing.Config{
    Enabled:      cfg.Environment != "test",
    CollectorURL: "localhost:4317", // Your OTLP collector
    Environment:  cfg.Environment,
    SampleRate:   1.0, // 100% sampling in dev, lower in prod
}

tracingShutdown, err := tracing.InitTracer(context.Background(), tracingConfig, log.Zap())
if err != nil {
    log.Fatal("Failed to initialize tracing", "error", err)
}
defer tracingShutdown(context.Background())

// 2. In internal/api/routes/routes.go, add middleware:
func SetupRoutes(container *di.Container) *gin.Engine {
    router := gin.New()
    
    // Add tracing middleware EARLY in the chain
    router.Use(tracing.HTTPMiddleware())
    router.Use(middleware.RequestID())
    router.Use(middleware.Logger(container.Logger))
    // ... rest of middleware
    
    return router
}

// 3. In repository methods, wrap queries:
func (r *Repository) GetByID(ctx context.Context, id uuid.UUID) (*Entity, error) {
    ctx, span := tracing.StartDBSpan(ctx, tracing.DBSpanConfig{
        Operation: "SELECT",
        Table:     "entities",
    })
    defer span.End()
    
    // Your query logic
    result, err := r.db.QueryRowContext(ctx, query, id)
    
    tracing.EndDBSpan(span, err, 1)
    return result, err
}
```

### 2. Standardized Error Handling
**Location:** `pkg/errors/`
**Files:**
- `types.go` - Error types and common errors
- `wrapper.go` - Error wrapping utilities
- `classifier.go` - Auto error classification
- `codes.go` - 80+ error codes

**Integration Steps:**

```go
// 1. Replace stdlib errors import:
// OLD: import "errors"
// NEW: import "github.com/stack-service/stack_service/pkg/errors"

// 2. In services, use typed errors:
func (s *Service) CreateOrder(ctx context.Context, req *CreateOrderRequest) error {
    // Validation
    if req.Amount <= 0 {
        return errors.NewValidationError("amount must be positive")
    }
    
    // Check balance
    balance, err := s.repo.GetBalance(ctx, req.UserID)
    if err != nil {
        return errors.WrapInternal(err, "failed to get balance")
    }
    
    if balance < req.Amount {
        return errors.ErrInsufficientFunds
    }
    
    // External API call
    result, err := s.alpacaClient.CreateOrder(ctx, req)
    if err != nil {
        return errors.WrapExternal(err, "alpaca", "failed to create order")
    }
    
    return nil
}

// 3. In handlers, use GetStatusCode:
func (h *Handler) CreateOrderHandler(c *gin.Context) {
    err := h.service.CreateOrder(c.Request.Context(), req)
    if err != nil {
        statusCode := errors.GetStatusCode(err)
        c.JSON(statusCode, gin.H{
            "error": errors.GetCode(err),
            "message": err.Error(),
            "request_id": c.GetString("request_id"),
        })
        return
    }
    
    c.JSON(http.StatusOK, response)
}

// 4. In circuit breakers, use classification:
func shouldRetry(err error) bool {
    return errors.ShouldRetry(err)
}

func isCircuitBreakerError(err error) bool {
    return errors.IsCircuitBreakerError(err)
}
```

### 3. Idempotency System
**Location:** `pkg/idempotency/`, `migrations/055_*.sql`
**Files:**
- Migration for idempotency_keys table
- `checker.go` - Request hashing and validation
- `middleware.go` - Gin middleware
- `idempotency_repository.go` - Database operations

**Integration Steps:**

```go
// 1. Run migration:
// The migration will run automatically on next startup

// 2. In cmd/main.go, create repository:
idempotencyRepo := repositories.NewIdempotencyRepository(db, log.Zap())

// 3. In routes setup, add middleware:
import "github.com/stack-service/stack_service/pkg/idempotency"

// Apply to all routes (optional idempotency)
router.Use(idempotency.Middleware(idempotencyRepo, log.Zap()))

// OR apply to specific critical endpoints (required idempotency)
withdrawalRoutes := router.Group("/api/v1/withdrawals")
withdrawalRoutes.Use(idempotency.RequireIdempotency())
withdrawalRoutes.POST("", handler.CreateWithdrawal)

// 4. Clients should send header:
// Idempotency-Key: idem_1234567890abcdef
```

### 4. Health Check Framework
**Location:** `pkg/health/checker.go`
**Status:** Core framework complete, specific checkers needed

**Next Steps:** Implement specific checkers (database, Redis, external APIs) using the provided framework.

---

## 🔧 Immediate Integration Checklist

### Step 1: Add Dependencies (5 minutes)
```bash
cd /Users/tobi/development/stack_service
go get go.opentelemetry.io/otel@v1.38.0
go get go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc@v1.38.0
go get go.opentelemetry.io/otel/sdk@v1.38.0
go mod tidy
```

### Step 2: Update Imports (15 minutes)
```bash
# Find and replace across codebase
# From: import "errors"
# To: import "github.com/stack-service/stack_service/pkg/errors"

# Use your IDE's find-and-replace or:
find internal/ -name "*.go" -type f -exec sed -i '' 's/"errors"/"github.com\/stack-service\/stack_service\/pkg\/errors"/g' {} \;
```

### Step 3: Initialize Tracing (10 minutes)
1. Add tracing initialization to `cmd/main.go` (see code above)
2. Add tracing middleware to router setup
3. Test: Run `go build` to ensure it compiles

### Step 4: Test (5 minutes)
```bash
# Build
go build -o bin/stack_service cmd/main.go

# Run tests
go test ./pkg/...

# Start server and check logs for:
# "OpenTelemetry tracing initialized"
# "Server started"
```

---

## 📚 Complete File Reference

### Created Files (Ready to Use)
```
pkg/
├── tracing/
│   ├── tracer.go           ✅ Complete
│   ├── middleware.go       ✅ Complete
│   ├── database.go         ✅ Complete
│   └── propagation.go      ✅ Complete
├── errors/
│   ├── types.go            ✅ Complete
│   ├── wrapper.go          ✅ Complete
│   ├── classifier.go       ✅ Complete
│   └── codes.go            ✅ Complete
├── idempotency/
│   ├── checker.go          ✅ Complete
│   └── middleware.go       ✅ Complete
└── health/
    └── checker.go          ✅ Complete (framework)

internal/infrastructure/repositories/
└── idempotency_repository.go  ✅ Complete

migrations/
├── 055_create_idempotency_keys.up.sql    ✅ Complete
└── 055_create_idempotency_keys.down.sql  ✅ Complete
```

### Files Still Needed (See PRODUCTION_READINESS_STATUS.md)
```
pkg/
├── health/
│   ├── database.go         ⏳ TODO
│   ├── redis.go            ⏳ TODO
│   └── external.go         ⏳ TODO
├── retry/
│   ├── policy.go           ⏳ TODO
│   ├── backoff.go          ⏳ TODO
│   └── decorator.go        ⏳ TODO
├── ratelimit/
│   ├── distributed.go      ⏳ TODO
│   └── sliding_window.go   ⏳ TODO
├── metrics/
│   └── business.go         ⏳ TODO
└── graceful/
    └── shutdown.go         ⏳ TODO
```

---

## 🚀 Post-Integration Testing

### 1. Test Tracing
```bash
# Start Jaeger for testing (Docker)
docker run -d --name jaeger \
  -p 5775:5775/udp \
  -p 6831:6831/udp \
  -p 6832:6832/udp \
  -p 5778:5778 \
  -p 16686:16686 \
  -p 14268:14268 \
  -p 14250:14250 \
  -p 9411:9411 \
  jaegertracing/all-in-one:latest

# Update tracer config to use Jaeger
# CollectorURL: "localhost:14250"

# Make requests and view traces at http://localhost:16686
```

### 2. Test Error Handling
```bash
# Trigger various errors and verify correct status codes:
curl -X POST http://localhost:8080/api/v1/orders \
  -H "Content-Type: application/json" \
  -d '{"amount": -100}' # Should return 400

curl -X POST http://localhost:8080/api/v1/orders \
  -H "Content-Type: application/json" \
  -d '{"amount": 999999999}' # Should return 400 (insufficient funds)
```

### 3. Test Idempotency
```bash
# Make same request twice with idempotency key
IDEM_KEY="test_$(uuidgen)"

curl -X POST http://localhost:8080/api/v1/deposits \
  -H "Idempotency-Key: $IDEM_KEY" \
  -H "Content-Type: application/json" \
  -d '{"amount": 100}'

# Second request should return cached response
curl -X POST http://localhost:8080/api/v1/deposits \
  -H "Idempotency-Key: $IDEM_KEY" \
  -H "Content-Type: application/json" \
  -d '{"amount": 100}'
```

---

## ⚠️ Known Issues to Address

### 1. Test Import Errors
```bash
# Fix: internal/zerog/clients does not exist
# Location: test/unit/zerog_storage_test.go:8

# Remove or update this import:
# - "github.com/stack-service/stack_service/internal/zerog/clients"
# + "github.com/stack-service/stack_service/internal/infrastructure/zerog"
```

### 2. Missing go.mod Dependencies
After integration, run:
```bash
go mod tidy
```

---

## 📖 Additional Resources

- **Full Implementation Status:** See `PRODUCTION_READINESS_STATUS.md`
- **Implementation Plan:** See the plan document
- **Project Rules:** See `WARP.md`

---

## 🎯 Next Priority Items (After Integration)

1. **Fix broken tests** (30 minutes)
2. **Implement remaining health checkers** (1 hour)
3. **Add retry logic package** (2 hours)
4. **Add distributed rate limiting** (2 hours)
5. **Add business metrics** (2 hours)
6. **Enhance graceful shutdown** (1 hour)

---

## 💡 Tips

1. **Start with tracing** - It provides immediate visibility into your application
2. **Use error classification** - It will improve retry logic and monitoring
3. **Enable idempotency on critical endpoints first** - Start with deposits, withdrawals, orders
4. **Test each integration** - Don't integrate everything at once
5. **Monitor logs** - Check for initialization messages and errors

---

## 🆘 Troubleshooting

### Tracing not working?
- Check OTLP collector is running
- Verify `CollectorURL` in config
- Check for "OpenTelemetry tracing initialized" log

### Errors not working?
- Ensure imports are updated
- Check error wrapping in services
- Verify GetStatusCode() used in handlers

### Idempotency not working?
- Verify migration ran successfully
- Check middleware is registered
- Ensure Idempotency-Key header is sent

---

**Need help?** Refer to the implementation files - they contain extensive comments and examples.
