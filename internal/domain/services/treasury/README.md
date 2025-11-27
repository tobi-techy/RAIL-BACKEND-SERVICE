# Treasury Engine

The Treasury Engine orchestrates currency conversion operations (USDC ↔ USD) and manages system buffer accounts to optimize capital efficiency through batched net settlement.

## Architecture

### Components

1. **Engine** (`engine.go`) - Core orchestrator for treasury operations
   - Buffer level monitoring
   - Conversion job creation and execution
   - Ledger integration for double-entry bookkeeping
   - Provider fallback and retry logic

2. **Scheduler** (`scheduler.go`) - Periodic execution manager
   - Net settlement cycles (default: every 5 minutes)
   - Job status monitoring (default: every 1 minute)
   - Graceful start/stop capabilities

3. **Provider Interface** (`provider.go`) - Abstraction for conversion providers
   - DueProvider implementation
   - Support for multiple providers (ZeroHash, etc.)
   - Provider selection based on health, capacity, priority

## Key Concepts

### Buffer Management

The system maintains three buffer accounts:

- **`system_buffer_usdc`** - On-chain USDC reserve for instant withdrawals
- **`system_buffer_fiat`** - Operational USD at conversion providers
- **`broker_operational`** - Pre-funded cash at Alpaca broker

Each buffer has configurable thresholds:
- **Min Threshold**: Alert and trigger conversion if below
- **Target Threshold**: Replenish to this level
- **Max Threshold**: Alert if exceeds (over-capitalized)

### Net Settlement

Instead of converting USDC/USD on every deposit/withdrawal, the engine:

1. Batches conversion demand over a time window
2. Checks buffer levels periodically
3. Executes conversions only when buffers need replenishment
4. Posts ledger entries upon completion

### Conversion Flow

```
1. Engine checks buffer levels
2. If buffer below min_threshold:
   - Create ConversionJob (pending status)
   - Select best provider
   - Execute conversion
   - Monitor status with provider
3. On provider completion:
   - Post ledger entries (double-entry)
   - Update buffer balances
   - Mark job as completed
```

## Usage

### Initializing the Engine

```go
package main

import (
    "context"
    "github.com/stack-service/stack_service/internal/domain/services/ledger"
    "github.com/stack-service/stack_service/internal/domain/services/treasury"
    "github.com/stack-service/stack_service/internal/infrastructure/repositories"
    "github.com/stack-service/stack_service/pkg/logger"
)

func main() {
    // Initialize dependencies
    log := logger.NewLogger()
    ledgerService := ledger.NewService(ledgerRepo, db, log)
    treasuryRepo := repositories.NewTreasuryRepository(db)
    
    // Create provider factory
    providerFactory := treasury.NewBaseProviderFactory()
    
    // Register Due provider
    dueFactory := treasury.NewDueProviderFactory(
        func() *due.Client { return dueClient },
        log,
    )
    providerFactory.Register("due", dueFactory.Create)
    
    // Create engine with custom config
    config := &treasury.EngineConfig{
        SchedulerInterval:      5 * time.Minute,
        HealthCheckInterval:    1 * time.Minute,
        ConversionTimeout:      30 * time.Minute,
        MaxRetries:             3,
        EnableAutoRebalance:    true,
    }
    
    engine := treasury.NewEngine(
        ledgerService,
        treasuryRepo,
        providerFactory,
        db,
        log,
        config,
    )
    
    // Create and start scheduler
    scheduler := treasury.NewScheduler(engine, log)
    if err := scheduler.Start(context.Background()); err != nil {
        log.Fatal("Failed to start treasury scheduler", "error", err)
    }
    
    // Graceful shutdown
    defer scheduler.Stop(context.Background())
}
```

### Manual Operations

#### Trigger Immediate Settlement Cycle

```go
if err := scheduler.TriggerImmediateCycle(ctx); err != nil {
    log.Error("Failed to trigger immediate cycle", "error", err)
}
```

#### Check Buffer Status

```go
bufferStatuses, err := engine.CheckBufferLevels(ctx)
if err != nil {
    return err
}

for _, status := range bufferStatuses {
    if status.NeedsReplenishment() {
        log.Warn("Buffer needs replenishment",
            "account", status.AccountType,
            "current", status.CurrentBalance,
            "target", status.TargetThreshold)
    }
}
```

#### Monitor Job Status

```go
if err := engine.MonitorConversionJobs(ctx); err != nil {
    log.Error("Job monitoring failed", "error", err)
}
```

## Configuration

### Environment Variables

```bash
# Treasury Engine
TREASURY_SCHEDULER_INTERVAL=5m
TREASURY_MONITOR_INTERVAL=1m
TREASURY_CONVERSION_TIMEOUT=30m
TREASURY_MAX_RETRIES=3
TREASURY_ENABLE_AUTO_REBALANCE=true

# Buffer Thresholds (configured in database via migrations)
# See migrations/058_create_treasury_tables.up.sql for seed data
```

### Database Configuration

Buffer thresholds are stored in `buffer_thresholds` table:

```sql
-- Example: Update USDC buffer thresholds
UPDATE buffer_thresholds
SET min_threshold = 10000.00,
    target_threshold = 50000.00,
    max_threshold = 100000.00
WHERE account_type = 'system_buffer_usdc';
```

Conversion providers in `conversion_providers` table:

```sql
-- Example: Add ZeroHash provider
INSERT INTO conversion_providers (
    name, provider_type, priority, status,
    supports_usdc_to_usd, supports_usd_to_usdc,
    min_conversion_amount, max_conversion_amount
) VALUES (
    'ZeroHash', 'zerohash', 2, 'active',
    true, true,
    100.00, 1000000.00
);
```

## Monitoring

### Key Metrics to Track

- **Buffer levels** - Current balance vs thresholds
- **Conversion job success rate** - Completed / Total jobs
- **Provider health** - Success rate per provider
- **Average conversion time** - Time from creation to completion
- **Stale job count** - Jobs stuck in processing

### Health Check Views

```sql
-- Buffer status
SELECT * FROM v_buffer_status;

-- Provider health
SELECT * FROM v_provider_health;

-- Active conversion jobs
SELECT * FROM v_active_conversion_jobs;
```

### Alerting

Set up alerts for:
- `buffer_status = 'CRITICAL_LOW'` - Immediate attention needed
- Conversion job failure rate >5%
- Provider health degraded
- Stale jobs >30 minutes old

## Extending

### Adding a New Conversion Provider

1. Implement the `ConversionProvider` interface:

```go
type MyProvider struct {
    client *myapi.Client
    config entities.ConversionProvider
    logger *logger.Logger
}

func (p *MyProvider) InitiateConversion(ctx, req) (*ConversionResponse, error) {
    // Call provider API to initiate conversion
}

func (p *MyProvider) GetConversionStatus(ctx, txID) (*ConversionStatusResponse, error) {
    // Check conversion status with provider
}

// ... implement other required methods
```

2. Register with the factory:

```go
providerFactory.Register("myprovider", func(config entities.ConversionProvider) (ConversionProvider, error) {
    return NewMyProvider(client, config, logger), nil
})
```

3. Add provider to database:

```sql
INSERT INTO conversion_providers (name, provider_type, priority, ...)
VALUES ('MyProvider', 'myprovider', 3, ...);
```

## Integration Points

### With Ledger Service

The engine posts ledger entries for every conversion:
- Debit from source buffer account
- Credit to destination buffer account

### With Funding/Withdrawal Services

Services should:
1. Check ledger balances before operations
2. Fire events when buffers change significantly
3. Let treasury handle all conversions asynchronously

### With Reconciliation Service

Reconciliation checks:
- Buffer ledger balances match actual provider/Circle balances
- All completed conversion jobs have corresponding ledger entries
- No orphaned or stuck jobs

## Troubleshooting

### Conversion Jobs Stuck

1. Check provider status: `SELECT * FROM v_provider_health`
2. Review stale jobs: `SELECT * FROM conversion_jobs WHERE status IN ('provider_submitted', 'provider_processing') AND submitted_at < NOW() - INTERVAL '30 minutes'`
3. Manually check status: `engine.CheckJobStatus(ctx, job)`

### Buffer Not Replenishing

1. Verify thresholds are correctly configured
2. Check source buffer has sufficient balance
3. Review provider capacity and limits
4. Check conversion job creation in logs

### High Failure Rate

1. Review provider health metrics
2. Check provider API credentials and connectivity
3. Validate amount limits and daily volume caps
4. Review error messages in `conversion_jobs.error_message`

## Testing

See `engine_test.go` for unit tests.

Integration testing should cover:
- Full conversion cycle with mock provider
- Buffer threshold breaches
- Provider fallback scenarios
- Ledger entry posting
- Job retry logic

## Next Steps

- [ ] Add webhook handlers for provider callbacks
- [ ] Implement ZeroHash provider
- [ ] Add Prometheus metrics export
- [ ] Build Grafana dashboards
- [ ] Implement emergency manual override endpoints
- [ ] Add conversion cost optimization algorithms
