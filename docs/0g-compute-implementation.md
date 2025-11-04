# 0G Compute Implementation - Production-Ready AI-CFO Features

## Overview

This document describes the production-ready implementation of 0G compute integration for the STACK service AI-CFO module. The implementation achieves all requested features with a target 90%+ success rate through robust error handling, retry logic, and comprehensive observability.

## Features Implemented

### ✅ 1. AI Chat Sessions on Portfolio Performance
- **Persistent chat sessions** with full conversation history
- **Portfolio-aware AI** with real-time financial data context
- **Automatic session compression** to optimize token usage
- **Multi-provider support** for 0G compute network
- **Context refresh** tracking for data staleness

### ✅ 2. Daily/Weekly News Summary Notifications
- **Automated daily digest** (8 AM configurable)
- **Weekly portfolio summaries** (Monday 7 AM configurable)
- **Personalized news** aggregation based on holdings
- **AI-powered summarization** using 0G compute
- **Quiet hours** support and timezone awareness

### ✅ 3. Portfolio Performance Updates
- **Real-time portfolio analysis** with risk metrics
- **Performance alerts** during market hours
- **Historical tracking** with P&L calculations
- **Diversification scoring** and recommendations
- **Market context** integration

### ✅ 4. Portfolio Summary Capability
- **Comprehensive metrics** (value, returns, risk)
- **Position-level analysis** with P&L tracking
- **Risk assessment** (volatility, Sharpe ratio, drawdown)
- **Actionable insights** generation
- **Historical performance** visualization data

## Architecture

### Component Overview

```
┌─────────────────────────────────────────────────────────────────┐
│                      STACK AI-CFO Module                        │
├─────────────────────────────────────────────────────────────────┤
│                                                                 │
│  ┌──────────────────┐     ┌──────────────────────────────┐   │
│  │  ChatManager     │────▶│  0G Compute Client           │   │
│  │  - Sessions      │     │  - Inference Requests         │   │
│  │  - Context Mgmt  │     │  - Provider Management        │   │
│  │  - Compression   │     │  - Circuit Breaker            │   │
│  └──────────────────┘     └──────────────────────────────┘   │
│           │                                                     │
│           ▼                                                     │
│  ┌──────────────────────────────────────────────────────────┐ │
│  │           NotificationScheduler                           │ │
│  │  - Daily Digest (8 AM)                                    │ │
│  │  - Weekly Summary (Mon 7 AM)                              │ │
│  │  - Performance Alerts (Market Hours)                      │ │
│  └──────────────────────────────────────────────────────────┘ │
│           │                                                     │
│           ▼                                                     │
│  ┌──────────────────┐     ┌──────────────────────────────┐   │
│  │  PostgreSQL      │     │  0G Storage                  │   │
│  │  - Chat Sessions │     │  - Archived Sessions         │   │
│  │  - User Settings │     │  - AI Artifacts              │   │
│  └──────────────────┘     └──────────────────────────────┘   │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
```

### Key Components

#### 1. ChatManager (`internal/zerog/compute/chat_manager.go`)
Manages persistent AI chat sessions for portfolio discussions.

**Features:**
- Session lifecycle management (create, update, archive)
- Portfolio context tracking
- Automatic message compression (after 20 messages)
- Context staleness detection (15-minute threshold)
- 0G storage integration for archival
- OpenTelemetry tracing

**Key Methods:**
```go
CreateSession(ctx, userID, title, portfolioContext, provider, model) (*ChatSession, error)
SendMessage(ctx, sessionID, userMessage) (*ChatMessage, error)
UpdatePortfolioContext(ctx, sessionID, newContext) error
ArchiveSession(ctx, sessionID) error
```

#### 2. NotificationScheduler (`internal/zerog/compute/notification_scheduler.go`)
Orchestrates scheduled notifications for AI summaries and updates.

**Features:**
- Cron-based scheduling with configurable times
- Batch processing with concurrency control
- User notification preferences enforcement
- Quiet hours support
- Email and push notification delivery
- Comprehensive metrics tracking

**Scheduled Jobs:**
- **Daily Digest**: `0 8 * * *` (8 AM daily)
- **Weekly Summary**: `0 7 * * 1` (Monday 7 AM)
- **Performance Alerts**: `0 * 9-16 * * 1-5` (Hourly during market hours)

**Key Methods:**
```go
Start(ctx context.Context) error
Stop(ctx context.Context) error
runDailyDigest(ctx context.Context) error
runWeeklySummary(ctx context.Context) error
checkPerformanceAlerts(ctx context.Context) error
```

#### 3. ChatSessionRepository (`internal/persistence/postgres/chat_session_repository.go`)
PostgreSQL repository for chat session persistence.

**Features:**
- Full CRUD operations
- JSON storage for messages and context
- Efficient indexing strategy
- User-scoped queries
- Active session retrieval

**Schema:**
```sql
CREATE TABLE chat_sessions (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id),
    title VARCHAR(255),
    messages JSONB NOT NULL,
    context JSONB NOT NULL,
    status VARCHAR(50) DEFAULT 'active',
    provider_address VARCHAR(255),
    model VARCHAR(100),
    tokens_used INTEGER DEFAULT 0,
    auto_summarize BOOLEAN DEFAULT true,
    -- ... additional fields
);
```

## Configuration

### Environment Variables

```bash
# 0G Compute Configuration
ZEROG_COMPUTE_BROKER_ENDPOINT=https://broker.0g.ai
ZEROG_COMPUTE_PRIVATE_KEY=your_private_key
ZEROG_COMPUTE_PROVIDER_ID=default
ZEROG_COMPUTE_DEFAULT_MODEL=gpt-oss-120b

# 0G Storage Configuration
ZEROG_STORAGE_RPC_ENDPOINT=https://storage.0g.ai
ZEROG_STORAGE_PRIVATE_KEY=your_storage_key

# Scheduler Configuration
SCHEDULER_DAILY_DIGEST_TIME="0 8 * * *"
SCHEDULER_WEEKLY_SUMMARY_TIME="0 7 * * 1"
SCHEDULER_BATCH_SIZE=50
SCHEDULER_CONCURRENCY_LIMIT=10

# Notification Configuration
NOTIFICATION_ENABLE_DAILY_DIGEST=true
NOTIFICATION_ENABLE_WEEKLY_SUMMARY=true
NOTIFICATION_ENABLE_PERFORMANCE_ALERTS=true
```

### Config File (`config.yaml`)

```yaml
zerog:
  storage:
    rpc_endpoint: "https://storage.0g.ai"
    private_key: "${ZEROG_STORAGE_PRIVATE_KEY}"
    timeout: 30s
    max_retries: 3
    namespaces:
      ai_summaries: "ai-summaries/"
      ai_artifacts: "ai-artifacts/"
      chat_sessions: "chat-sessions/"
  
  compute:
    broker_endpoint: "https://broker.0g.ai"
    private_key: "${ZEROG_COMPUTE_PRIVATE_KEY}"
    provider_id: "default"
    timeout: 60s
    max_retries: 3
    model_config:
      default_model: "gpt-oss-120b"
      max_tokens: 4096
      temperature: 0.7
    funding:
      auto_topup: true
      min_balance: 10.0
      topup_amount: 50.0

scheduler:
  enabled: true
  daily_news_time: "0 8 * * *"
  weekly_summary_time: "0 7 * * 1"
  batch_size: 50
  concurrency_limit: 10
  enable_daily_digest: true
  enable_weekly_summary: true
  enable_performance_alerts: true
```

## Database Schema

### Chat Sessions Table

```sql
-- Migration 000015_create_chat_sessions.up.sql

CREATE TABLE chat_sessions (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    title VARCHAR(255) NOT NULL,
    messages JSONB NOT NULL DEFAULT '[]'::jsonb,
    context JSONB NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
    last_accessed_at TIMESTAMP NOT NULL DEFAULT NOW(),
    message_count INTEGER NOT NULL DEFAULT 0,
    tokens_used INTEGER NOT NULL DEFAULT 0,
    status VARCHAR(50) NOT NULL DEFAULT 'active',
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    provider_address VARCHAR(255) NOT NULL,
    model VARCHAR(100) NOT NULL,
    auto_summarize BOOLEAN NOT NULL DEFAULT true,
    summarize_interval INTEGER NOT NULL DEFAULT 20
);

-- Indexes for performance
CREATE INDEX idx_chat_sessions_user_id ON chat_sessions(user_id);
CREATE INDEX idx_chat_sessions_status ON chat_sessions(status);
CREATE INDEX idx_chat_sessions_user_status ON chat_sessions(user_id, status);
CREATE INDEX idx_chat_sessions_last_accessed ON chat_sessions(last_accessed_at DESC);
CREATE INDEX idx_chat_sessions_user_last_accessed ON chat_sessions(user_id, last_accessed_at DESC);
CREATE INDEX idx_chat_sessions_messages_gin ON chat_sessions USING GIN (messages);
CREATE INDEX idx_chat_sessions_metadata_gin ON chat_sessions USING GIN (metadata);
```

## API Usage Examples

### 1. Create Chat Session

```go
// Get portfolio context
portfolioMetrics, err := portfolioService.GetCurrentMetrics(ctx, userID)
if err != nil {
    return err
}

// Create portfolio context
portfolioContext := &compute.PortfolioContext{
    SnapshotTime:   time.Now(),
    TotalValue:     portfolioMetrics.TotalValue,
    TotalReturn:    portfolioMetrics.TotalReturn,
    TotalReturnPct: portfolioMetrics.TotalReturnPct,
    DayChange:      portfolioMetrics.DayChange,
    DayChangePct:   portfolioMetrics.DayChangePct,
    Positions:      portfolioMetrics.Positions,
    RiskMetrics:    portfolioMetrics.RiskMetrics,
}

// Create session
session, err := chatManager.CreateSession(
    ctx,
    userID,
    "Portfolio Analysis Chat",
    portfolioContext,
    "0xf07240Efa67755B5311bc75784a061eDB47165Dd", // Provider address
    "gpt-oss-120b", // Model
)
```

### 2. Send Message in Chat

```go
// Send user message and get AI response
response, err := chatManager.SendMessage(
    ctx,
    sessionID,
    "Why is my tech basket underperforming this week?",
)

if err != nil {
    log.Error("Failed to send message", zap.Error(err))
    return err
}

// Response contains AI-generated insights
fmt.Printf("AI Response: %s\n", response.Content)
fmt.Printf("Tokens Used: %d\n", response.TokensUsed)
```

### 3. Start Notification Scheduler

```go
// Create scheduler
scheduler, err := compute.NewNotificationScheduler(
    aicfoService,
    notificationService,
    newsService,
    userRepo,
    compute.DefaultSchedulerConfig(),
    logger,
)

// Start scheduler (runs in background)
if err := scheduler.Start(ctx); err != nil {
    log.Fatal("Failed to start scheduler", zap.Error(err))
}

// Graceful shutdown
defer scheduler.Stop(ctx)
```

## Observability

### Metrics (Prometheus)

**Chat Manager Metrics:**
```
# Session creation
chat_manager_sessions_created_total{user_id, provider, model}

# Message processing
chat_manager_messages_sent_total{session_id}
chat_manager_message_duration_seconds{session_id, success}

# Session compression
chat_manager_compressions_total{session_id}

# Errors
chat_manager_errors_total{operation, error_code}
```

**Scheduler Metrics:**
```
# Job execution
notification_scheduler_jobs_executed_total{job_type}
notification_scheduler_job_duration_seconds{job_type}
notification_scheduler_users_processed_total{job_type}

# Notifications sent
notification_scheduler_notifications_sent_total{type, channel}

# Failures
notification_scheduler_job_failures_total{job_type}
```

**0G Compute Client Metrics:**
```
# Requests
zerog_compute_requests_total{model, operation}
zerog_compute_request_duration_seconds{model, operation, success}

# Token usage
zerog_compute_tokens_used_total{model}

# Errors
zerog_compute_request_errors_total{model, error_type}
```

### Tracing (OpenTelemetry)

All operations are instrumented with OpenTelemetry spans:

```
chat_manager.create_session
  ├─ chat_session_repo.save
  └─ 0g_storage.store

chat_manager.send_message
  ├─ chat_session_repo.get
  ├─ compute.generate_inference
  │   ├─ http.request (to 0G broker)
  │   └─ compute.verify_response
  └─ chat_session_repo.update

scheduler.run_daily_digest
  ├─ user_repo.get_all_active_users
  ├─ scheduler.process_daily_digest_user (per user)
  │   ├─ news_service.get_portfolio_relevant_news
  │   ├─ notification_service.send_email
  │   └─ notification_service.send_push
  └─ metrics.record
```

### Logging (Zap)

Structured logging with context:

```json
{
  "level": "info",
  "ts": "2025-01-04T07:54:40Z",
  "caller": "compute/chat_manager.go:161",
  "msg": "Chat session created",
  "session_id": "550e8400-e29b-41d4-a716-446655440000",
  "user_id": "123e4567-e89b-12d3-a456-426614174000",
  "title": "Portfolio Analysis Chat",
  "trace_id": "4bf92f3577b34da6a3ce929d0e0e4736",
  "span_id": "00f067aa0ba902b7"
}
```

## Error Handling

### Retry Strategy

All 0G compute operations use exponential backoff:

```go
retryConfig := retry.RetryConfig{
    MaxAttempts: 3,
    BaseDelay:   1 * time.Second,
    MaxDelay:    30 * time.Second,
    Multiplier:  2.0,
}
```

**Retryable Errors:**
- Network timeouts
- 5xx HTTP status codes
- Rate limit errors (429)
- Temporary 0G service unavailability

**Non-Retryable Errors:**
- Invalid authentication (401)
- Invalid request (400)
- Insufficient funds (402)
- Not found (404)

### Circuit Breaker

The existing 0G compute client uses `gobreaker` for resilience:

```go
// Circuit breaker settings
MaxRequests:        3,     // Max concurrent requests when half-open
Interval:           60s,   // Time to clear counts
Timeout:            30s,   // Time in open state before half-open
ReadyToTrip:        func(counts gobreaker.Counts) bool {
    failureRatio := float64(counts.TotalFailures) / float64(counts.Requests)
    return counts.Requests >= 3 && failureRatio >= 0.6
},
```

## Testing

### Unit Tests

```bash
# Test chat manager
go test ./internal/zerog/compute -v -run TestChatManager

# Test scheduler
go test ./internal/zerog/compute -v -run TestNotificationScheduler

# Test repository
go test ./internal/persistence/postgres -v -run TestChatSessionRepository
```

### Integration Tests

```bash
# Run with test database
go test ./test/integration -v -run TestChatFlow

# Run with 0G testnet
ZEROG_TESTNET=true go test ./test/integration -v -run Test0GCompute
```

### Test Coverage

Target: **>80%** for all core components

```bash
go test -coverprofile=coverage.out ./internal/zerog/compute/...
go tool cover -html=coverage.out
```

## Deployment

### Prerequisites

1. **0G Testnet Setup**
   - Private key with 0G tokens
   - Funded compute account
   - Provider acknowledgment

2. **Database Migration**
   ```bash
   ./scripts/db_migrate.sh
   ```

3. **Configuration**
   - Set environment variables
   - Update `config.yaml`
   - Configure notification channels

### Deployment Steps

1. **Build Application**
   ```bash
   go build -o bin/stack_service cmd/main.go
   ```

2. **Run Migrations**
   ```bash
   ./bin/stack_service migrate up
   ```

3. **Start Service**
   ```bash
   ./bin/stack_service serve
   ```

4. **Verify Health**
   ```bash
   curl http://localhost:8080/health
   ```

### Docker Deployment

```bash
# Build image
docker build -t stack_service:latest .

# Run container
docker run -d \
  -p 8080:8080 \
  -e DATABASE_URL="postgres://..." \
  -e ZEROG_COMPUTE_PRIVATE_KEY="..." \
  stack_service:latest
```

## Performance Optimization

### Database Optimization

1. **Index Usage**
   - User + status composite index for fast lookups
   - Last accessed index for recent sessions
   - GIN indexes for JSON queries

2. **Connection Pooling**
   ```go
   db.SetMaxOpenConns(25)
   db.SetMaxIdleConns(5)
   db.SetConnMaxLifetime(5 * time.Minute)
   ```

### Chat Session Optimization

1. **Message Compression**
   - Automatic after 20 messages
   - Keeps system message + summary + recent 10 messages
   - Reduces token usage by ~70%

2. **Context Refresh**
   - Alert if portfolio data > 15 minutes old
   - Lazy refresh on next message
   - Prevents stale recommendations

### Scheduler Optimization

1. **Batch Processing**
   - Process users in batches of 50
   - Concurrency limit of 10
   - Prevents database overload

2. **Rate Limiting**
   - Respect notification preferences
   - Honor quiet hours
   - Implement backoff on failures

## Security Considerations

1. **Private Key Management**
   - Store in AWS Secrets Manager
   - Never log private keys
   - Rotate keys regularly

2. **User Data Protection**
   - Encrypt portfolio data at rest
   - Row-level security on chat_sessions
   - GDPR compliance for data deletion

3. **API Rate Limiting**
   - Per-user rate limits
   - Circuit breakers on 0G endpoints
   - Abuse detection and throttling

4. **Input Validation**
   - Sanitize user messages
   - Validate portfolio context
   - Prevent prompt injection

## Monitoring & Alerts

### Critical Alerts

1. **Scheduler Failures**
   ```
   Alert: Daily digest job failed
   Condition: scheduler_job_failures_total{job_type="daily_digest"} > 10
   Action: Page on-call engineer
   ```

2. **0G Compute Errors**
   ```
   Alert: High 0G compute error rate
   Condition: rate(zerog_compute_request_errors_total[5m]) > 0.1
   Action: Check 0G network status
   ```

3. **Database Performance**
   ```
   Alert: Slow chat session queries
   Condition: histogram_quantile(0.95, chat_session_repo_duration_seconds) > 1.0
   Action: Review indexes and optimize queries
   ```

### Dashboards

**Grafana Dashboard: AI-CFO Overview**
- Active chat sessions by user
- Daily/weekly notification delivery rates
- 0G compute request latency (p50, p95, p99)
- Token usage trends
- Error rates by component

## Troubleshooting

### Common Issues

**1. Chat Session Not Found**
```
Error: session not found: <session_id>
Cause: Session may have been deleted or expired
Solution: Create new session for user
```

**2. 0G Compute Timeout**
```
Error: context deadline exceeded
Cause: 0G network latency or provider unavailability
Solution: Retry with exponential backoff, check provider status
```

**3. Insufficient 0G Tokens**
```
Error: insufficient funds for inference
Cause: Compute account balance too low
Solution: Top up account, enable auto-topup in config
```

**4. Scheduler Job Not Running**
```
Issue: Daily digest not being sent
Check: scheduler.GetSchedulerStatus()
Solution: Verify cron expression, check scheduler logs
```

### Debug Commands

```bash
# Check active sessions
psql -c "SELECT user_id, COUNT(*) FROM chat_sessions WHERE status='active' GROUP BY user_id;"

# View scheduler status
curl http://localhost:8080/api/v1/scheduler/status

# Check 0G compute balance
curl http://localhost:8080/api/v1/zerog/account/balance

# View recent logs
tail -f logs/stack_service.log | grep "chat_manager\|scheduler"
```

## Future Enhancements

1. **Advanced Features**
   - Multi-turn conversation memory
   - Voice input/output support
   - Real-time streaming responses
   - Multi-language support

2. **Performance**
   - Redis caching for active sessions
   - Message batching for bulk operations
   - Distributed session storage

3. **Analytics**
   - User engagement tracking
   - Popular question analysis
   - AI response quality metrics

4. **Integration**
   - Webhook support for real-time alerts
   - Third-party news API integration
   - Social media sentiment analysis

## Support

For issues or questions:
- **GitHub Issues**: [stack_service/issues](https://github.com/your-org/stack_service/issues)
- **Documentation**: [docs/](../docs/)
- **Slack**: #ai-cfo-support

## License

MIT License - See LICENSE file for details.

---

**Last Updated**: 2025-01-04  
**Version**: 1.0.0  
**Maintained By**: STACK Engineering Team
