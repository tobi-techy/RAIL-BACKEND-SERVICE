# Ledger System Deployment Checklist

## Pre-Deployment Verification

### Database Migrations

- [ ] **Backup production database**
  ```bash
  pg_dump $DATABASE_URL > backup_$(date +%Y%m%d_%H%M%S).sql
  ```

- [ ] **Test migrations on staging**
  ```bash
  # Run migrations
  migrate -path migrations -database $STAGING_DATABASE_URL up
  
  # Verify tables created
  psql $STAGING_DATABASE_URL -c "\dt ledger*"
  psql $STAGING_DATABASE_URL -c "\dt conversion*"
  psql $STAGING_DATABASE_URL -c "\dt buffer*"
  ```

- [ ] **Test migration rollback**
  ```bash
  migrate -path migrations -database $STAGING_DATABASE_URL down 1
  migrate -path migrations -database $STAGING_DATABASE_URL up
  ```

- [ ] **Verify data integrity**
  ```sql
  -- Check system accounts created
  SELECT * FROM ledger_accounts WHERE user_id IS NULL;
  
  -- Check buffer thresholds seeded
  SELECT * FROM buffer_thresholds;
  
  -- Check conversion providers seeded
  SELECT * FROM conversion_providers;
  ```

### Code Compilation

- [ ] **Build compiles successfully**
  ```bash
  go build ./cmd/api
  ```

- [ ] **All tests pass**
  ```bash
  go test ./... -v
  ```

- [ ] **No golangci-lint errors**
  ```bash
  golangci-lint run
  ```

### Configuration

- [ ] **Environment variables configured**
  ```bash
  # Ledger Configuration
  LEDGER_SHADOW_MODE=true
  LEDGER_STRICT_MODE=false
  
  # Treasury Configuration
  TREASURY_SCHEDULER_INTERVAL=5m
  TREASURY_MONITOR_INTERVAL=1m
  TREASURY_CONVERSION_TIMEOUT=30m
  
  # Onchain Configuration
  ONCHAIN_DEPOSIT_POLL_INTERVAL=30s
  ONCHAIN_BUFFER_ALERT_THRESHOLD=5000.0
  
  # Circle Configuration
  CIRCLE_API_KEY=<secret>
  CIRCLE_WEBHOOK_SECRET=<secret>
  
  # Due Configuration
  DUE_API_KEY=<secret>
  DUE_API_SECRET=<secret>
  ```

- [ ] **Secrets stored in AWS Secrets Manager**
  ```bash
  aws secretsmanager create-secret \
    --name stack-service/circle-api-key \
    --secret-string "<api-key>"
  
  aws secretsmanager create-secret \
    --name stack-service/circle-webhook-secret \
    --secret-string "<webhook-secret>"
  ```

- [ ] **Feature flags configured**
  ```bash
  USE_LEDGER_INTEGRATION=true
  USE_ENHANCED_BALANCE_SERVICE=false  # Enable in Phase 4.2
  ENABLE_TREASURY_SCHEDULER=true
  ENABLE_ONCHAIN_ENGINE=true
  ```

### Infrastructure

- [ ] **Database connection pool sized appropriately**
  ```bash
  DB_MAX_OPEN_CONNS=25
  DB_MAX_IDLE_CONNS=5
  DB_CONN_MAX_LIFETIME=5m
  ```

- [ ] **Redis available for rate limiting**
  ```bash
  redis-cli -h $REDIS_HOST ping
  ```

- [ ] **CloudWatch log groups created**
  ```bash
  aws logs create-log-group --log-group-name /aws/ecs/stack-service
  aws logs create-log-group --log-group-name /aws/ecs/stack-service/treasury
  aws logs create-log-group --log-group-name /aws/ecs/stack-service/ledger
  ```

## Phase 1: Ledger Foundation Deployment

### Pre-Deployment

- [ ] **Run migration 056 (ledger tables)**
  ```bash
  migrate -path migrations -database $DATABASE_URL up 1
  ```

- [ ] **Run migration 057 (initial state)**
  ```bash
  migrate -path migrations -database $DATABASE_URL up 1
  ```

- [ ] **Verify system accounts created**
  ```sql
  SELECT id, account_type, currency, balance 
  FROM ledger_accounts 
  WHERE user_id IS NULL;
  ```

### Deployment

- [ ] **Deploy application with ledger service enabled**
  ```bash
  kubectl apply -f k8s/deployment.yaml
  kubectl rollout status deployment/stack-service
  ```

- [ ] **Verify ledger service initializes**
  ```bash
  kubectl logs -f deployment/stack-service | grep "Ledger"
  ```

### Post-Deployment Validation

- [ ] **Test ledger transaction creation**
  ```bash
  curl -X POST http://localhost:8080/internal/ledger/test \
    -H "Content-Type: application/json" \
    -d '{"user_id":"<uuid>","amount":"100.00"}'
  ```

- [ ] **Verify double-entry integrity**
  ```sql
  -- All transactions should have balanced debits/credits
  SELECT 
    lt.id,
    SUM(CASE WHEN le.entry_type = 'debit' THEN le.amount ELSE 0 END) as debits,
    SUM(CASE WHEN le.entry_type = 'credit' THEN le.amount ELSE 0 END) as credits
  FROM ledger_transactions lt
  JOIN ledger_entries le ON le.transaction_id = lt.id
  GROUP BY lt.id
  HAVING SUM(CASE WHEN le.entry_type = 'debit' THEN le.amount ELSE 0 END) 
      != SUM(CASE WHEN le.entry_type = 'credit' THEN le.amount ELSE 0 END);
  ```

- [ ] **Check CloudWatch metrics**
  - `ledger_transaction_created_total`
  - `ledger_transaction_duration_ms`
  - `ledger_account_balance_checked_total`

## Phase 2: Treasury Engine Deployment

### Pre-Deployment

- [ ] **Run migration 058 (treasury tables)**
  ```bash
  migrate -path migrations -database $DATABASE_URL up 1
  ```

- [ ] **Verify providers and thresholds seeded**
  ```sql
  SELECT * FROM conversion_providers;
  SELECT * FROM buffer_thresholds;
  ```

- [ ] **Configure Due API credentials**
  ```bash
  aws secretsmanager update-secret \
    --secret-id stack-service/due-api-key \
    --secret-string "<api-key>"
  ```

### Deployment

- [ ] **Deploy with treasury scheduler enabled**
  ```bash
  ENABLE_TREASURY_SCHEDULER=true
  kubectl apply -f k8s/deployment.yaml
  ```

- [ ] **Verify treasury engine initializes**
  ```bash
  kubectl logs -f deployment/stack-service | grep "Treasury"
  ```

- [ ] **Verify scheduler starts**
  ```bash
  kubectl logs -f deployment/stack-service | grep "Treasury Scheduler started"
  ```

### Post-Deployment Validation

- [ ] **Check buffer status**
  ```sql
  SELECT * FROM v_buffer_status;
  ```

- [ ] **Monitor first settlement cycle**
  ```bash
  kubectl logs -f deployment/stack-service | grep "settlement cycle"
  ```

- [ ] **Verify no conversion jobs created (buffers healthy)**
  ```sql
  SELECT * FROM conversion_jobs 
  WHERE created_at > NOW() - INTERVAL '1 hour';
  ```

- [ ] **Manually trigger conversion job (testing)**
  ```bash
  curl -X POST http://localhost:8080/internal/treasury/trigger-cycle \
    -H "Authorization: Bearer $ADMIN_TOKEN"
  ```

- [ ] **Check CloudWatch metrics**
  - `treasury_buffer_check_total`
  - `treasury_conversion_jobs_created_total`
  - `treasury_settlement_cycle_duration_ms`

## Phase 3: Onchain Engine Deployment

### Pre-Deployment

- [ ] **Configure Circle webhook endpoint**
  - URL: `https://api.yourservice.com/webhooks/circle/transfers`
  - Events: `transfers.created`, `transfers.completed`, `transfers.failed`
  - Verify webhook secret matches `CIRCLE_WEBHOOK_SECRET`

- [ ] **Test webhook signature verification**
  ```bash
  curl -X POST http://localhost:8080/webhooks/circle/transfers \
    -H "X-Circle-Signature: test-signature" \
    -H "Content-Type: application/json" \
    -d '{"notificationType":"transfers.completed",...}'
  ```

### Deployment

- [ ] **Deploy with onchain engine enabled**
  ```bash
  ENABLE_ONCHAIN_ENGINE=true
  kubectl apply -f k8s/deployment.yaml
  ```

- [ ] **Verify webhook routes registered**
  ```bash
  kubectl logs deployment/stack-service | grep "webhooks/circle"
  ```

### Post-Deployment Validation

- [ ] **Test deposit processing (testnet)**
  - Send testnet USDC to Circle wallet
  - Monitor webhook received
  - Verify ledger entries created
  - Check user balance updated

- [ ] **Test withdrawal execution (testnet)**
  - Create withdrawal request
  - Monitor onchain engine logs
  - Verify Circle transfer executed
  - Check transaction hash returned

- [ ] **Monitor buffer levels**
  ```bash
  curl http://localhost:8080/internal/onchain/buffer-status \
    -H "Authorization: Bearer $ADMIN_TOKEN"
  ```

- [ ] **Check CloudWatch metrics**
  - `onchain_deposits_processed_total`
  - `onchain_withdrawals_executed_total`
  - `onchain_buffer_level`
  - `circle_webhook_received_total`

## Phase 4: Flow Integration (Shadow Mode)

### Phase 4.1: Enable Shadow Mode

- [ ] **Deploy with shadow mode enabled**
  ```bash
  LEDGER_SHADOW_MODE=true
  LEDGER_STRICT_MODE=false
  kubectl apply -f k8s/deployment.yaml
  ```

- [ ] **Monitor dual-write operations**
  ```bash
  kubectl logs -f deployment/stack-service | grep "Shadow mode"
  ```

- [ ] **Check for discrepancies (should be none)**
  ```bash
  kubectl logs deployment/stack-service | grep "discrepancy"
  ```

- [ ] **Run for 48 hours monitoring logs**
  - No discrepancies logged
  - Both ledger and legacy tables updated
  - Performance acceptable

### Phase 4.2: Switch Reads to Ledger

- [ ] **Deploy with ledger reads enabled**
  ```bash
  BALANCE_SOURCE=ledger
  USE_ENHANCED_BALANCE_SERVICE=true
  kubectl apply -f k8s/deployment.yaml
  ```

- [ ] **Monitor balance query performance**
  ```sql
  -- Compare query times
  EXPLAIN ANALYZE 
  SELECT * FROM v_user_balances WHERE user_id = '<uuid>';
  ```

- [ ] **Run load test**
  ```bash
  k6 run load-test-balance-api.js
  ```

- [ ] **Verify response times acceptable**
  - p50 < 100ms
  - p95 < 200ms
  - p99 < 500ms

### Phase 4.3: Enable Strict Mode

- [ ] **Deploy with strict mode (staging first)**
  ```bash
  LEDGER_STRICT_MODE=true
  kubectl apply -f k8s/staging/deployment.yaml
  ```

- [ ] **Monitor for failures (none expected)**
  ```bash
  kubectl logs -f deployment/stack-service | grep "strict mode"
  ```

- [ ] **Run for 48+ hours without errors**

- [ ] **Deploy strict mode to production**
  ```bash
  kubectl apply -f k8s/production/deployment.yaml
  ```

### Phase 4.4: Cut Over to Ledger Only

- [ ] **Verify no discrepancies for 72+ hours**
  ```sql
  -- Manual reconciliation check
  SELECT COUNT(*) FROM (
    SELECT 
      u.id,
      la.balance as ledger_balance,
      b.buying_power as legacy_balance
    FROM users u
    JOIN ledger_accounts la ON la.user_id = u.id 
      AND la.account_type = 'usdc_balance'
    LEFT JOIN balances b ON b.user_id = u.id
    WHERE ABS(la.balance - COALESCE(b.buying_power, 0)) > 0.01
  ) discrepancies;
  ```

- [ ] **Deploy with shadow mode disabled**
  ```bash
  LEDGER_SHADOW_MODE=false
  kubectl apply -f k8s/deployment.yaml
  ```

- [ ] **Stop writing to legacy `balances` table**

- [ ] **Monitor for 7 days**

- [ ] **Mark `balances` table as deprecated**
  ```sql
  COMMENT ON TABLE balances IS 'DEPRECATED: Use ledger_accounts instead. To be archived 2025-XX-XX';
  ```

## Phase 5: Reconciliation Service (Future)

- [ ] Deploy reconciliation service
- [ ] Schedule daily reconciliation jobs
- [ ] Configure alerting for discrepancies
- [ ] Archive legacy tables after 90 days

## Monitoring & Alerting Setup

### CloudWatch Alarms

- [ ] **High Discrepancy Rate**
  ```bash
  aws cloudwatch put-metric-alarm \
    --alarm-name ledger-high-discrepancy-rate \
    --comparison-operator GreaterThanThreshold \
    --evaluation-periods 1 \
    --metric-name discrepancy_count \
    --namespace StackService \
    --period 300 \
    --statistic Sum \
    --threshold 10
  ```

- [ ] **Ledger Write Failures**
  ```bash
  aws cloudwatch put-metric-alarm \
    --alarm-name ledger-write-failures \
    --comparison-operator LessThanThreshold \
    --evaluation-periods 2 \
    --metric-name write_success_rate \
    --namespace StackService \
    --period 300 \
    --statistic Average \
    --threshold 0.99
  ```

- [ ] **Low System Buffer**
  ```bash
  aws cloudwatch put-metric-alarm \
    --alarm-name low-system-buffer \
    --comparison-operator LessThanThreshold \
    --evaluation-periods 1 \
    --metric-name system_buffer_usdc \
    --namespace StackService \
    --period 60 \
    --statistic Average \
    --threshold 5000
  ```

- [ ] **Treasury Conversion Failures**
  ```bash
  aws cloudwatch put-metric-alarm \
    --alarm-name treasury-conversion-failures \
    --comparison-operator GreaterThanThreshold \
    --evaluation-periods 1 \
    --metric-name conversion_jobs_failed \
    --namespace StackService \
    --period 300 \
    --statistic Sum \
    --threshold 5
  ```

### Grafana Dashboards

- [ ] **Ledger Dashboard**
  - Transaction volume
  - Account balances
  - Write latency
  - Error rate

- [ ] **Treasury Dashboard**
  - Buffer levels
  - Conversion jobs status
  - Provider health
  - Settlement cycle duration

- [ ] **Onchain Dashboard**
  - Deposits processed
  - Withdrawals executed
  - Webhook latency
  - Buffer discrepancies

### PagerDuty Integration

- [ ] **Configure critical alerts**
  - Ledger write failures
  - Critical buffer low
  - High discrepancy rate

- [ ] **Configure warning alerts**
  - Conversion job failures
  - Performance degradation
  - Buffer below target

## Rollback Procedures

### Emergency Rollback

- [ ] **Disable ledger integration immediately**
  ```bash
  kubectl set env deployment/stack-service \
    LEDGER_SHADOW_MODE=false \
    BALANCE_SOURCE=legacy \
    USE_LEDGER_INTEGRATION=false
  ```

- [ ] **Verify services healthy**
  ```bash
  kubectl rollout status deployment/stack-service
  curl https://api.yourservice.com/health
  ```

### Database Rollback

- [ ] **Rollback migrations if needed**
  ```bash
  # Backup first!
  pg_dump $DATABASE_URL > backup_before_rollback.sql
  
  # Rollback
  migrate -path migrations -database $DATABASE_URL down 3
  ```

### Restore from Backup

- [ ] **If catastrophic failure**
  ```bash
  # Stop service
  kubectl scale deployment/stack-service --replicas=0
  
  # Restore database
  psql $DATABASE_URL < backup_YYYYMMDD_HHMMSS.sql
  
  # Restart service
  kubectl scale deployment/stack-service --replicas=3
  ```

## Post-Deployment

### Day 1

- [ ] Monitor all metrics for anomalies
- [ ] Review logs for errors
- [ ] Verify all features working
- [ ] Check system buffer levels

### Week 1

- [ ] Run reconciliation manually daily
- [ ] Compare ledger vs legacy balances
- [ ] Review performance metrics
- [ ] Fix any discovered issues

### Week 2

- [ ] Review alert thresholds
- [ ] Optimize slow queries if needed
- [ ] Update documentation
- [ ] Train support team

### Month 1

- [ ] Full system audit
- [ ] Performance optimization
- [ ] Plan for legacy deprecation
- [ ] Document lessons learned

## Sign-Off

### Technical Lead
- [ ] Code reviewed and approved
- [ ] Architecture validated
- [ ] Tests passing

### DevOps
- [ ] Infrastructure ready
- [ ] Monitoring configured
- [ ] Rollback tested

### Product
- [ ] Feature flags configured
- [ ] Rollout plan approved
- [ ] Support team notified

### Security
- [ ] Security review completed
- [ ] Secrets properly stored
- [ ] Audit logging enabled

---

**Deployment Date:** _________________

**Deployed By:** _________________

**Approved By:** _________________
