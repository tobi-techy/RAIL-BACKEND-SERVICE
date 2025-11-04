# 0G Implementation Refactoring Plan

## Overview
This document outlines the refactoring of the 0G (Zero Gravity) implementation to follow clean architecture principles and project standards.

## Problems Identified

### 1. Duplicate Implementations
- **Location**: `internal/zerog/` and `internal/infrastructure/zerog/`
- **Issue**: Two separate implementations with overlapping functionality
- **Impact**: Code duplication, maintenance overhead, confusion

### 2. Architecture Violations
- Missing circuit breaker pattern (required by project rules)
- Improper error handling (not all errors wrapped with context)
- Hardcoded configurations instead of using config system
- Missing proper dependency injection

### 3. Code Quality Issues
- Repository adapter workaround indicates interface mismatch
- Commented-out code in integration layer
- Inconsistent error types
- Missing input validation in several places

## Refactoring Strategy

### Phase 1: Consolidation (COMPLETED)
✅ Create unified adapter in `internal/adapters/zerog/`
✅ Add proper configuration with validation
✅ Implement circuit breaker pattern
✅ Add comprehensive observability (metrics, tracing, logging)

### Phase 2: Cleanup (IN PROGRESS)
- Remove duplicate implementations
- Delete obsolete files
- Update imports throughout codebase
- Clean up handlers and routes

### Phase 3: Testing
- Add unit tests with mocks
- Add integration tests with testcontainers
- Ensure >80% code coverage

## New Architecture

```
internal/
├── adapters/
│   └── zerog/                    # NEW: Clean unified adapter
│       ├── config.go             # Configuration with validation
│       ├── storage_adapter.go    # Storage implementation with circuit breaker
│       ├── inference_adapter.go  # Inference implementation with circuit breaker
│       └── namespace_manager.go  # Namespace management utilities
├── api/
│   ├── handlers/
│   │   ├── zerog_handlers.go     # UPDATED: Use new adapters
│   │   └── aicfo_handlers.go     # UPDATED: Use new adapters
│   └── routes/
│       └── zerog_routes.go       # UPDATED: Simplified routing
└── domain/
    ├── entities/
    │   └── zerog_entities.go     # Domain entities (unchanged)
    └── services/
        └── aicfo_service.go      # UPDATED: Use new adapters
```

## Files to DELETE

### Duplicate Implementations
- `internal/zerog/integration.go` - Replace with simplified version
- `internal/infrastructure/zerog/inference_gateway.go` - Moved to adapters
- `internal/infrastructure/zerog/namespace_manager.go` - Moved to adapters
- `internal/infrastructure/zerog/storage_client.go` - Moved to adapters
- `internal/infrastructure/config/zerog.go` - Consolidated into adapter config

### Obsolete Files
- `internal/zerog/clients/storage.go` - Duplicate functionality
- `internal/zerog/compute/client.go` - Needs refactoring
- `internal/zerog/inference/gateway.go` - Duplicate
- `internal/zerog/storage/namespace.go` - Duplicate
- `internal/zerog/prompts/templates.go` - Move to separate package if needed

## Migration Steps

### For Developers

1. **Update Imports**
   ```go
   // OLD
   import "github.com/stack-service/stack_service/internal/infrastructure/zerog"
   
   // NEW
   import zerogadapter "github.com/stack-service/stack_service/internal/adapters/zerog"
   ```

2. **Initialize Adapters**
   ```go
   // OLD
   storageClient, err := zerog.NewStorageClient(cfg, logger)
   
   // NEW
   config := &zerogadapter.Config{
       Storage: zerogadapter.StorageConfig{
           RPCEndpoint: cfg.Storage.RPCEndpoint,
           // ... other fields
       },
       // ... circuit breaker config
   }
   storageAdapter, err := zerogadapter.NewStorageAdapter(config, logger)
   ```

3. **Circuit Breaker Benefits**
   - Automatic failure detection
   - Fast-fail when service is down
   - Automatic recovery attempts
   - Observability via state changes

## Benefits

### Code Quality
- Single source of truth for 0G integration
- Proper separation of concerns
- Follows adapter pattern consistently
- Comprehensive error handling

### Observability
- OpenTelemetry metrics for all operations
- Distributed tracing support
- Structured logging with correlation IDs
- Circuit breaker state monitoring

### Reliability
- Circuit breaker prevents cascade failures
- Automatic retry with exponential backoff
- Proper timeout handling
- Health check monitoring

### Maintainability
- Clear module boundaries
- Testable interfaces
- Comprehensive configuration validation
- Self-documenting code

## Testing Strategy

### Unit Tests
```go
// Test with mocked dependencies
func TestStorageAdapter_Store(t *testing.T) {
    mockConfig := &Config{...}
    mockLogger := zaptest.NewLogger(t)
    
    adapter, err := NewStorageAdapter(mockConfig, mockLogger)
    assert.NoError(t, err)
    
    // Test success case
    result, err := adapter.Store(ctx, "test-ns", data, metadata)
    assert.NoError(t, err)
    assert.NotEmpty(t, result.URI)
}
```

### Integration Tests
- Use testcontainers for dependencies
- Test against local 0G network if available
- Verify circuit breaker behavior
- Test retry logic

## Rollout Plan

### Stage 1: Preparation (Week 1)
- ✅ Create new adapter implementation
- ✅ Add configuration and validation
- ✅ Implement circuit breaker

### Stage 2: Migration (Week 1-2)
- Update handlers to use new adapters
- Update services to use new adapters
- Add comprehensive tests
- Update documentation

### Stage 3: Cleanup (Week 2)
- Remove old implementations
- Clean up unused files
- Update all imports
- Run full test suite

### Stage 4: Validation (Week 2)
- Smoke tests in staging
- Performance benchmarks
- Load testing
- Production deployment

## Monitoring

### Key Metrics
- `zerog_storage_requests_total` - Total storage requests
- `zerog_storage_request_duration_seconds` - Request latency
- `zerog_storage_request_errors_total` - Error count
- `zerog_storage_stored_bytes_total` - Data volume
- Circuit breaker state changes

### Alerts
- Circuit breaker open > 5 minutes
- Error rate > 10%
- Request latency p99 > 5s
- No available storage nodes

## Rollback Plan

If issues occur:
1. Keep old implementation in place during migration
2. Feature flag to toggle between old/new implementation
3. Monitor error rates and circuit breaker states
4. Quick rollback via configuration change

## Questions & Answers

**Q: Why not just fix the existing code?**
A: The duplicate implementations and architectural issues are too pervasive. A clean refactor is more maintainable long-term.

**Q: Will this break existing functionality?**
A: No, the interfaces remain the same. This is an internal refactoring.

**Q: What about backward compatibility?**
A: All domain entities and interfaces remain unchanged. Only internal implementation changes.

**Q: How long will migration take?**
A: Estimated 1-2 weeks including testing and validation.

## Success Criteria

- ✅ Single, consolidated adapter implementation
- ✅ Circuit breaker pattern implemented
- ✅ >80% test coverage
- ✅ All linting checks pass
- ✅ Zero regression in functionality
- ✅ Improved observability
- ✅ Better error handling
- ✅ Comprehensive documentation

## Next Steps

1. ✅ Create new adapter implementation
2. Create inference adapter with circuit breaker
3. Update handlers and routes
4. Add comprehensive tests
5. Remove old implementations
6. Update documentation
7. Deploy to staging
8. Monitor and validate
9. Deploy to production
