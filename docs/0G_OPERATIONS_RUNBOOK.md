# 0G Network Operations Runbook

## Overview
This runbook provides operational procedures for managing the 0G Network integration in production.

## Architecture Components

### Storage Layer
- **Client**: `internal/zerog/clients/storage.go`
- **Namespace Manager**: `internal/zerog/storage/namespace.go`
- **Backup Manager**: `internal/zerog/recovery/backup.go`

### Compute Layer
- **Client**: `internal/zerog/compute/client.go`
- **Inference Gateway**: `internal/zerog/inference/gateway.go`

### Supporting Services
- **Circuit Breaker**: `pkg/circuitbreaker/breaker.go`
- **Secrets Manager**: `pkg/secrets/vault.go`
- **Metrics Collector**: `internal/zerog/metrics/collector.go`
- **Quota Manager**: `internal/zerog/quota/manager.go`

## Deployment Procedures

### Pre-Deployment Checklist
- [ ] All secrets configured in vault/environment
- [ ] Database migrations applied
- [ ] Health checks passing on staging
- [ ] Load tests completed
- [ ] Backup procedures tested
- [ ] Rollback plan documented
- [ ] Monitoring dashboards configured
- [ ] Alert rules configured

### Deployment Steps
1. **Backup Current State**
   ```bash
   # Backup database
   pg_dump -h $DB_HOST -U $DB_USER $DB_NAME > backup_$(date +%Y%m%d_%H%M%S).sql
   
   # Record current version
   git rev-parse HEAD > deployed_version.txt
   ```

2. **Deploy New Version**
   ```bash
   # Build image
   docker build -t stack_service:$VERSION .
   
   # Push to registry
   docker push stack_service:$VERSION
   
   # Update deployment
   kubectl set image deployment/stack-service app=stack_service:$VERSION
   ```

3. **Verify Deployment**
   ```bash
   # Check health
   curl https://api.stack.com/health
   
   # Check 0G health
   curl https://api.stack.com/api/v1/zerog/health
   
   # Monitor logs
   kubectl logs -f deployment/stack-service
   ```

## Monitoring & Alerts

### Key Metrics
- `zerog.storage.uploads.total` - Total uploads
- `zerog.storage.downloads.total` - Total downloads
- `zerog.storage.errors.total` - Storage errors
- `zerog.compute.requests.total` - Inference requests
- `zerog.compute.tokens.total` - Token usage
- `zerog.cost.total.usd` - Total costs
- `zerog.quota.usage.percent` - Quota usage

### Alert Thresholds
- **Critical**: Error rate > 5%, Circuit breaker open
- **Warning**: Error rate > 1%, Quota usage > 80%
- **Info**: Cost increase > 20%, Unusual traffic patterns

### Grafana Dashboards
- **0G Overview**: System health, request rates, error rates
- **Cost Tracking**: Daily/monthly costs, per-user costs
- **Performance**: Latency percentiles, throughput
- **Quotas**: User quota usage, limit violations

## Incident Response

### Storage Failures

**Symptom**: Upload/download failures
```bash
# Check storage health
curl https://api.stack.com/api/v1/zerog/storage/health

# Check indexer connectivity
curl https://indexer-storage-testnet-turbo.0g.ai/health

# Review logs
kubectl logs -l app=stack-service | grep "storage"

# Check circuit breaker state
curl https://api.stack.com/api/v1/zerog/metrics | jq '.circuit_breaker'
```

**Resolution**:
1. Verify RPC endpoints are accessible
2. Check private key configuration
3. Verify indexer is responding
4. Restart service if circuit breaker stuck open
5. Escalate if issue persists > 15 minutes

### Compute Failures

**Symptom**: Inference request failures
```bash
# Check compute health
curl https://api.stack.com/api/v1/zerog/compute/health

# Check provider status
curl https://api.stack.com/api/v1/zerog/compute/providers

# Review authentication
kubectl get secret zerog-compute-key
```

**Resolution**:
1. Verify broker endpoint connectivity
2. Check authentication signature generation
3. Verify provider acknowledgment
4. Check account balance
5. Rotate keys if authentication failing

### Quota Exceeded

**Symptom**: Users hitting quota limits
```bash
# Check user quota
curl https://api.stack.com/api/v1/users/$USER_ID/quota

# Review usage patterns
kubectl logs -l app=stack-service | grep "quota exceeded"

# Check cost accumulation
curl https://api.stack.com/api/v1/zerog/costs
```

**Resolution**:
1. Verify quota limits are correct
2. Check for abuse/unusual patterns
3. Increase limits if legitimate usage
4. Contact user if suspicious activity
5. Implement rate limiting if needed

### Data Loss Prevention

**Symptom**: Storage ID not found
```bash
# Check backup records
psql -c "SELECT * FROM zerog_storage_backups WHERE storage_id='$STORAGE_ID'"

# Verify data on 0G network
curl https://indexer-storage-testnet-turbo.0g.ai/file/$STORAGE_ID

# Check replication status
curl https://api.stack.com/api/v1/zerog/storage/$STORAGE_ID/replicas
```

**Resolution**:
1. Check backup table for storage ID
2. Attempt recovery from replicas
3. Verify checksum matches
4. Re-upload if data corrupted
5. Notify user if unrecoverable

## Maintenance Procedures

### Daily Tasks
- Review error logs
- Check quota usage trends
- Verify backup completion
- Monitor cost accumulation

### Weekly Tasks
- Review performance metrics
- Analyze cost trends
- Update capacity planning
- Test disaster recovery

### Monthly Tasks
- Security audit
- Cost optimization review
- Quota limit adjustments
- Performance tuning

## Disaster Recovery

### Data Recovery Procedure
1. **Identify Lost Data**
   ```sql
   SELECT * FROM zerog_storage_backups 
   WHERE storage_id = '$STORAGE_ID';
   ```

2. **Verify Backup Integrity**
   ```bash
   # Download from 0G
   curl -X POST https://api.stack.com/api/v1/zerog/storage/retrieve \
     -d '{"storage_id": "$STORAGE_ID"}'
   
   # Verify checksum
   sha256sum downloaded_file
   ```

3. **Restore Data**
   ```bash
   # Re-upload if needed
   curl -X POST https://api.stack.com/api/v1/zerog/storage/upload \
     -F "file=@recovered_file"
   ```

### Service Recovery
1. **Rollback Deployment**
   ```bash
   kubectl rollout undo deployment/stack-service
   kubectl rollout status deployment/stack-service
   ```

2. **Database Rollback**
   ```bash
   psql -f backup_YYYYMMDD_HHMMSS.sql
   ```

3. **Verify Recovery**
   ```bash
   curl https://api.stack.com/health
   curl https://api.stack.com/api/v1/zerog/health
   ```

## Performance Tuning

### Storage Optimization
- Adjust replica count based on criticality
- Implement caching for frequently accessed data
- Batch uploads when possible
- Use compression for large files

### Compute Optimization
- Cache inference results
- Batch requests when possible
- Use appropriate model for task
- Implement request queuing

### Cost Optimization
- Monitor per-user costs
- Implement tiered pricing
- Set budget alerts
- Optimize storage retention

## Security Procedures

### Key Rotation
```bash
# Generate new key
openssl rand -hex 32 > new_key.txt

# Update in vault
vault kv put secret/zerog/storage private_key=@new_key.txt

# Restart service
kubectl rollout restart deployment/stack-service

# Verify
curl https://api.stack.com/api/v1/zerog/health
```

### Access Audit
```sql
-- Review namespace access
SELECT * FROM zerog_storage_backups 
WHERE user_id = '$USER_ID' 
ORDER BY backed_up_at DESC;

-- Check quota violations
SELECT user_id, COUNT(*) 
FROM quota_violations 
GROUP BY user_id 
ORDER BY COUNT(*) DESC;
```

## Escalation Contacts

- **On-Call Engineer**: Slack #stack-oncall
- **0G Network Support**: support@0g.ai
- **Infrastructure Team**: infra@stack.com
- **Security Team**: security@stack.com

## Useful Commands

```bash
# Check all 0G services
kubectl get pods -l component=zerog

# View recent errors
kubectl logs -l app=stack-service --since=1h | grep ERROR

# Check circuit breaker status
curl https://api.stack.com/api/v1/zerog/circuit-breaker

# Force quota reset
curl -X POST https://api.stack.com/api/v1/admin/quotas/reset

# Export metrics
curl https://api.stack.com/metrics | grep zerog
```
