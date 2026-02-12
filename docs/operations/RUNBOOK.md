# RAIL Backend Operations Runbook

## Table of Contents

1. [Overview](#overview)
2. [On-Call Procedures](#on-call-procedures)
3. [Incident Response](#incident-response)
4. [Troubleshooting Guides](#troubleshooting-guides)
5. [Deployment Procedures](#deployment-procedures)
6. [Emergency Procedures](#emergency-procedures)
7. [Post-Incident Review](#post-incident-review)

---

## Overview

This runbook provides comprehensive procedures for operating the RAIL Backend service in production. It covers day-to-day operations, incident response, and emergency procedures.

### Service Information

| Property | Value |
|----------|-------|
| Service Name | RAIL Backend |
| Environment | Production |
| Primary Region | us-east-1 |
| Secondary Region | eu-west-1 |
| SLA | 99.9% uptime |
| RTO | < 15 minutes |
| RPO | < 5 minutes |

### Key Contacts

| Role | Contact | Escalation |
|------|---------|------------|
| Primary On-Call | `@rail-oncall-primary` | Slack #ops-escalation |
| Secondary On-Call | `@rail-oncall-secondary` | Slack #ops-escalation |
| Engineering Lead | `@rail-eng-lead` | Slack #rail-leadership |
| SRE Team | `#sre-rail` | PagerDuty |
| Security Team | `#security-incidents` | security@rail-service.com |

### Monitoring Dashboards

- **Production Dashboard**: https://grafana.rail-service.com/d/rail-backend-production
- **APM Dashboard**: https://grafana.rail-service.com/d/rail-backend-apm
- **Multi-Region**: https://grafana.rail-service.com/d/rail-backend-multi-region
- **AlertManager**: https://alertmanager.rail-service.com

---

## On-Call Procedures

### Shift Schedule

On-call rotations are managed through PagerDuty with the following schedule:

```
Week 1: Engineer A (Primary), Engineer B (Secondary)
Week 2: Engineer C (Primary), Engineer A (Secondary)
Week 3: Engineer B (Primary), Engineer C (Secondary)
Week 4: Engineer A (Primary), Engineer B (Secondary)
```

### Handover Checklist

Before starting your on-call shift:

```bash
# 1. Check PagerDuty status
open https://rail.pagerduty.com/oncalls

# 2. Review open incidents
open https://rail.pagerduty.com/incidents

# 3. Check recent deployments
aws ecs list-services --cluster rail-backend-us-east-1

# 4. Review CloudWatch alarms
open https://console.aws.amazon.com/cloudwatch/home?region=us-east-1#alarms

# 5. Check Slack channels
# - #rail-alerts
# - #rail-incidents
# - #ops-war-room

# 6. Verify runbooks are accessible
ls -la docs/operations/
```

### Daily Checks (8:00 AM UTC)

Perform these checks at the start of each day:

```bash
#!/bin/bash
# scripts/ops/daily-check.sh

echo "=== RAIL Backend Daily Check ==="
echo "Date: $(date -u +%Y-%m-%dT%H:%M:%SZ)"
echo ""

# 1. Check service health
echo "1. Service Health:"
curl -s https://api.rail-service.com/health | jq .
curl -s https://api.rail-service.com/ready | jq .

# 2. Check ECS tasks
echo "2. ECS Tasks:"
aws ecs list-tasks --cluster rail-backend-us-east-1 --desired-status RUNNING
aws ecs list-tasks --cluster rail-backend-eu-west-1 --desired-status RUNNING

# 3. Check RDS status
echo "3. RDS Status:"
aws rds describe-db-instances \
  --db-instance-identifier rail-backend-prod \
  --query 'DBInstances[0].{Status:DBInstanceStatus,ReplicaLag:SecondaryLagInternetAddress}'

# 4. Check ElastiCache
echo "4. ElastiCache Status:"
aws elasticache describe-cache-clusters \
  --cache-cluster-id rail-backend-cluster \
  --query 'CacheClusters[0].{Status:CacheClusterStatus,Nodes:NumCacheNodes}'

# 5. Check ALB health
echo "5. ALB Health:"
aws elbv2 describe-target-health \
  --target-group-arn $TARGET_GROUP_ARN_US_EAST_1
aws elbv2 describe-target-health \
  --target-group-arn $TARGET_GROUP_ARN_EU_WEST_1

# 6. Check recent errors
echo "6. Recent Errors (last hour):"
aws logs filter-log-events \
  --log-group-name /ecs/rail-backend-us-east-1 \
  --start-time $(($(date +%s) - 3600))000 \
  --filter-pattern "ERROR" \
  --query 'events[0:5].message'

echo ""
echo "=== Daily Check Complete ==="
```

### Weekly Tasks (Monday 9:00 AM UTC)

```bash
#!/bin/bash
# scripts/ops/weekly-check.sh

# 1. Review automated test results
echo "1. Automated Test Results:"
open https://github.com/rail-service/rail_backend/actions

# 2. Check certificate expiration
echo "2. SSL Certificate Status:"
aws acm list-certificates --query 'CertificateSummaryList[?DomainName==`rail-service.com`]'

# 3. Review cost optimization opportunities
echo "3. Cost Report:"
open https://console.aws.amazon.com/cost-management/home?region=us-east-1

# 4. Security audit
echo "4. Security Audit:"
aws securityhub get-findings \
  --filters '{"ResourceType": [{"Comparison": "EQUALS", "Value": "AWS::ECS::TaskDefinition"}]}'

# 5. Update documentation
echo "5. Check for documentation updates needed"
git status docs/operations/

# 6. Backup verification
echo "6. Backup Verification:"
./scripts/db/backup.sh list
```

---

## Incident Response

### Incident Severity Levels

| Severity | Description | Response Time | Examples |
|----------|-------------|---------------|----------|
| SEV-1 | Critical - Service down | 15 min | Complete outage, data loss |
| SEV-2 | High - Degraded service | 30 min | Feature unavailable, high latency |
| SEV-3 | Medium - Limited impact | 2 hours | Non-critical feature down |
| SEV-4 | Low - Minimal impact | 24 hours | Cosmetic issues, warnings |

### Incident Response Process

```
┌─────────────────────────────────────────────────────────────────┐
│                    INCIDENT RESPONSE FLOW                        │
└─────────────────────────────────────────────────────────────────┘
                              │
                              ▼
              ┌───────────────────────────────┐
              │ 1. DETECTION                  │
              │ - Alert triggered             │
              │ - Customer report             │
              │ - Automated check failed      │
              └───────────────────────────────┘
                              │
                              ▼
              ┌───────────────────────────────┐
              │ 2. TRIAGE (0-15 min)          │
              │ - Assess severity             │
              │ - Declare incident            │
              │ - Notify stakeholders         │
              └───────────────────────────────┘
                              │
                              ▼
              ┌───────────────────────────────┐
              │ 3. RESPONSE                   │
              │ - Diagnose root cause         │
              │ - Implement fix               │
              │ - Monitor resolution          │
              └───────────────────────────────┘
                              │
                              ▼
              ┌───────────────────────────────┐
              │ 4. RESOLUTION                 │
              │ - Verify service restored     │
              │ - Close incident              │
              │ - Document timeline           │
              └───────────────────────────────┘
                              │
                              ▼
              ┌───────────────────────────────┐
              │ 5. POST-INCIDENT              │
              │ - Root cause analysis         │
              │ - Update runbooks             │
              │ - Implement preventive actions│
              └───────────────────────────────┘
```

### SEV-1 Critical Incident Response

**Immediate Actions (0-5 minutes):**

```bash
#!/bin/bash
# scripts/ops/incident-sev1.sh

# 1. Acknowledge PagerDuty alert
# (Done through PagerDuty mobile app or web)

# 2. Join incident channel
echo "Join Slack channel: #rail-incidents-[incident-id]"

# 3. Check service status
echo "=== Service Status Check ==="
curl -v https://api.rail-service.com/health
curl -v https://api.rail-service.com/ready

# 4. Check AWS console for issues
echo "Checking AWS services..."

# 5. Gather initial information
echo "=== Gathering Information ==="
aws ecs describe-services \
  --cluster rail-backend-us-east-1 \
  --service rail-backend-us-east-1

aws rds describe-db-instances \
  --db-instance-identifier rail-backend-prod

aws elbv2 describe-load-balancers \
  --names rail-backend-primary
```

**Communication Template:**

```
🚨 SEV-1 INCIDENT DECLARED

Incident ID: INC-[NUMBER]
Severity: SEV-1 - Critical
Status: Investigating
Lead: [ON-CALL ENGINEER]

Summary:
[Brief description of the issue]

Impact:
- [Impacted services]
- [Number of affected users]
- [Estimated revenue impact]

Current Actions:
1. [Action 1]
2. [Action 2]

Updates will be posted every 15 minutes.
```

### Common Incident Types

#### 1. High Error Rate

**Symptoms:**
- 5xx error rate > 1%
- Increased latency
- Failed health checks

**Troubleshooting:**

```bash
#!/bin/bash
# Check error rates
echo "=== Error Rate Analysis ==="
aws logs filter-log-events \
  --log-group-name /ecs/rail-backend-us-east-1 \
  --start-time $(($(date +%s) - 300))000 \
  --filter-pattern "ERROR" \
  --query 'events | length(@)'

# Check recent deployments
echo "=== Recent Deployments ==="
aws ecs list-task-definitions \
  --family-prefix rail-backend \
  --status ACTIVE \
  --query 'taskDefinitionArns[-5:]'

# Check database
echo "=== Database Connection Pool ==="
aws rds describe-db-instances \
  --db-instance-identifier rail-backend-prod \
  --query 'DBInstances[0].{Connections:PendingModifiedValues.MaximumConnections,Status:DBInstanceStatus}'

# Check Redis
echo "=== Redis Memory ==="
aws elasticache describe-cache-clusters \
  --cache-cluster-id rail-backend-cluster \
  --query 'CacheClusters[0].{Memory:CacheNodeType,Status:CacheClusterStatus}'
```

**Common Causes & Fixes:**

| Cause | Fix |
|-------|-----|
| Database connection pool exhausted | Scale up RDS or optimize queries |
| Memory pressure | Restart pods, increase memory limits |
| Rate limiting | Adjust rate limits or scale horizontally |
| Upstream dependency | Check Circle/Alpaca/Bridge APIs |

#### 2. Database Issues

**Symptoms:**
- Query timeouts
- Connection failures
- Replication lag > 30 seconds

**Troubleshooting:**

```bash
#!/bin/bash
# Check database health
echo "=== Database Health ==="
PGPASSWORD="$DB_PASSWORD" psql \
  -h "$DB_HOST" \
  -c "SELECT pg_is_in_recovery();"

# Check replication lag
PGPASSWORD="$DB_PASSWORD" psql \
  -h "$DB_HOST" \
  -c "SELECT * FROM pg_stat_replication;"

# Check active connections
PGPASSWORD="$DB_PASSWORD" psql \
  -h "$DB_HOST" \
  -c "SELECT count(*) FROM pg_stat_activity;"

# Check slow queries
PGPASSWORD="$DB_PASSWORD" psql \
  -h "$DB_HOST" \
  -c "SELECT pid, now() - pg_stat_activity.query_start AS duration, query FROM pg_stat_activity WHERE (state = 'active') ORDER BY duration DESC LIMIT 5;"

# Check disk space
PGPASSWORD="$DB_PASSWORD" psql \
  -h "$DB_HOST" \
  -c "SELECT pg_size_pretty(pg_database_size('rail_service'));"
```

**Emergency Actions:**

```bash
#!/bin/bash
# Failover to replica (if primary is unhealthy)
aws rds promote-read-replica \
  --db-instance-identifier rail-backend-eu-west-1

# Restart RDS (last resort)
aws rds reboot-db-instance \
  --db-instance-identifier rail-backend-prod \
  --force-failover
```

#### 3. High Latency

**Symptoms:**
- p95 latency > 1 second
- Increased request queue times
- Timeout errors

**Troubleshooting:**

```bash
#!/bin/bash
# Check latency metrics
echo "=== Latency Analysis ==="
aws cloudwatch get-metric-statistics \
  --namespace AWS/ApplicationELB \
  --metric-name AverageLatency \
  --start-time $(date -d '1 hour ago' -Iseconds) \
  --end-time $(date -Iseconds) \
  --period 60 \
  --statistics Average Maximum \
  --dimensions Name=LoadBalancer,Value=app/rail-backend-primary

# Check ECS metrics
echo "=== ECS CPU/Memory ==="
aws cloudwatch get-metric-statistics \
  --namespace AWS/ECS \
  --metric-name MemoryUtilization \
  --start-time $(date -d '1 hour ago' -Iseconds) \
  --end-time $(date -Iseconds) \
  --period 60 \
  --statistics Average Maximum \
  --dimensions Name=ClusterName,Value=rail-backend-us-east-1

# Trace analysis
echo "=== X-Ray Traces ==="
# Open AWS X-Ray console
open https://console.aws.amazon.com/xray/home?region=us-east-1
```

**Common Causes & Fixes:**

| Cause | Fix |
|-------|-----|
| CPU throttling | Scale up ECS tasks |
| Database slow queries | Add indexes, optimize queries |
| Network latency | Check VPC flow logs |
| External API slow | Contact Circle/Alpaca support |

---

## Troubleshooting Guides

### Service Won't Start

```bash
#!/bin/bash
# 1. Check logs
echo "=== Application Logs ==="
aws logs tail /ecs/rail-backend-us-east-1 --since 5m

# 2. Check task status
echo "=== ECS Task Status ==="
aws ecs describe-tasks \
  --cluster rail-backend-us-east-1 \
  --tasks $(aws ecs list-tasks --cluster rail-backend-us-east-1 --desired-status RUNNING --query 'taskArns[0]' --output text)

# 3. Check resource limits
echo "=== Resource Limits ==="
kubectl describe pod -n rail $(kubectl get pods -n rail -l app=rail-backend -o jsonpath='{.items[0].metadata.name}')

# 4. Verify configuration
echo "=== Configuration ==="
# Check environment variables
# Check secret mounts
# Verify database connectivity
```

### Authentication Failures

```bash
#!/bin/bash
# 1. Check JWT configuration
echo "=== JWT Configuration ==="
# Verify JWT_SECRET is set correctly
# Check token expiration

# 2. Check database for user issues
echo "=== User Database Check ==="
PGPASSWORD="$DB_PASSWORD" psql \
  -h "$DB_HOST" \
  -c "SELECT id, email, is_active, mfa_enabled FROM users WHERE email = 'user@example.com';"

# 3. Check Redis for session issues
echo "=== Redis Session Check ==="
redis-cli -h "$REDIS_HOST" KEYS "session:*"

# 4. Check audit logs for security events
echo "=== Security Audit Log ==="
# Check recent login failures
# Check for suspicious activity
```

### Payment Processing Issues

```bash
#!/bin/bash
# 1. Check Circle API status
echo "=== Circle API Status ==="
curl -s https://status.circle.com

# 2. Check Circle account
echo "=== Circle Account ==="
# Verify Circle API keys are valid
# Check Circle dashboard for alerts

# 3. Check transaction logs
echo "=== Recent Transactions ==="
# Look for failed transactions
# Check webhook delivery status

# 4. Check webhooks
echo "=== Webhook Status ==="
# Verify webhook endpoints are reachable
# Check webhook retry counts
```

### Webhook Failures

```bash
#!/bin/bash
# 1. Check webhook health
echo "=== Webhook Health ==="
curl -v https://api.rail-service.com/webhooks/chain-deposit -X POST \
  -H "Content-Type: application/json" \
  -d '{"test": true}'

# 2. Check Circle webhooks
echo "=== Circle Webhooks ==="
# Verify webhook URL is correct
# Check Circle dashboard for failures

# 3. Check Bridge webhooks
echo "=== Bridge Webhooks ==="
# Verify webhook URL is correct
# Check Bridge dashboard for failures

# 4. Check Alpaca webhooks
echo "=== Alpaca Webhooks ==="
# Verify webhook URL is correct
# Check Alpaca dashboard for failures
```

---

## Deployment Procedures

### Standard Deployment

```bash
#!/bin/bash
# scripts/deploy.sh

set -e

TAG=${1:-$(git rev-parse HEAD)}
REGION=${2:-us-east-1}
CLUSTER="rail-backend-${REGION}"

echo "=== RAIL Backend Deployment ==="
echo "Tag: ${TAG}"
echo "Region: ${REGION}"
echo "Cluster: ${CLUSTER}"
echo ""

# 1. Build and push Docker image
echo "1. Building Docker image..."
docker build -t rail-backend:${TAG} .
docker tag rail-backend:${TAG} ${ECR_URL}:${TAG}
docker push ${ECR_URL}:${TAG}

# 2. Update ECS service
echo "2. Updating ECS service..."
aws ecs update-service \
  --cluster ${CLUSTER} \
  --service rail-backend-${REGION} \
  --task-definition "rail-backend:${TAG}" \
  --desired-count 6 \
  --deployment-configuration maximumPercent=200,minimumHealthyPercent=100

# 3. Wait for deployment
echo "3. Waiting for deployment..."
aws ecs wait services-stable \
  --cluster ${CLUSTER} \
  --services rail-backend-${REGION}

# 4. Verify deployment
echo "4. Verifying deployment..."
./scripts/ops/health-check.sh

echo ""
echo "=== Deployment Complete ==="
```

### Rollback Procedure

```bash
#!/bin/bash
# scripts/rollback.sh

set -e

PREVIOUS_TAG=${1:-$(git rev-parse HEAD~1)}
REGION=${2:-us-east-1}
CLUSTER="rail-backend-${REGION}"

echo "=== Rolling Back ==="
echo "Rolling back to: ${PREVIOUS_TAG}"
echo ""

# 1. Find previous task definition
PREVIOUS_TASK=$(aws ecs list-task-definitions \
  --family-prefix rail-backend \
  --status ACTIVE \
  --query "taskDefinitionArns[-2]" \
  --output text)

echo "Previous task definition: ${PREVIOUS_TASK}"

# 2. Update service
echo "2. Rolling back ECS service..."
aws ecs update-service \
  --cluster ${CLUSTER} \
  --service rail-backend-${REGION} \
  --task-definition ${PREVIOUS_TASK} \
  --desired-count 6

# 3. Wait for rollback
echo "3. Waiting for rollback..."
aws ecs wait services-stable \
  --cluster ${CLUSTER} \
  --services rail-backend-${REGION}

# 4. Verify
echo "4. Verifying rollback..."
./scripts/ops/health-check.sh

echo ""
echo "=== Rollback Complete ==="
```

### Blue-Green Deployment

```bash
#!/bin/bash
# scripts/deploy-blue-green.sh

set -e

TAG=${1:-$(git rev-parse HEAD)}
REGION=${2:-us-east-1}

echo "=== Blue-Green Deployment ==="
echo "Deploying: ${TAG}"

# 1. Register new task definition
echo "1. Registering task definition..."
aws ecs register-task-definition \
  --family "rail-backend-blue" \
  --container-definitions "$(cat container-definitions.json)" \
  --task-role-arn "$TASK_ROLE_ARN"

# 2. Create new target group
echo "2. Creating target group..."
TG_BLUE=$(aws elbv2 create-target-group \
  --name "rail-backend-blue-${TAG}" \
  --port 8080 \
  --protocol HTTP \
  --vpc-id $VPC_ID \
  --health-check-path /health \
  --query 'TargetGroups[0].TargetGroupArn' \
  --output text)

# 3. Deploy to blue target group
echo "3. Deploying to blue target group..."
aws ecs update-service \
  --cluster "rail-backend-${REGION}" \
  --service "rail-backend-blue" \
  --task-definition "rail-backend-blue:${TAG}"

# 4. Wait for blue to be healthy
echo "4. Verifying blue deployment..."
aws elbv2 wait target-in-service \
  --target-group-arn ${TG_BLUE} \
  --targets Id=$(aws ecs list-container-instances \
    --cluster "rail-backend-${REGION}" \
    --query 'containerInstanceArns[0]' \
    --output text),Port=8080

# 5. Shift traffic (30% -> 50% -> 100%)
echo "5. Shifting traffic..."
for weight in 30 50 100; do
  echo "  Shifting to ${weight}%"
  aws elbv2 modify-rule \
    --rule-arn $RULE_ARN \
    --conditions Field=host,Values=api.rail-service.com \
    --actions "Type=forward,TargetGroupArn=${TG_BLUE},Weight=${weight}"
  sleep 30
done

echo ""
echo "=== Blue-Green Deployment Complete ==="
```

---

## Emergency Procedures

### Database Emergency Recovery

```bash
#!/bin/bash
# scripts/ops/db-emergency-recovery.sh

set -e

echo "=== Database Emergency Recovery ==="

# 1. Check current state
echo "1. Checking current state..."
aws rds describe-db-instances \
  --db-instance-identifier rail-backend-prod \
  --query 'DBInstances[0].{Status:DBInstanceStatus,MultiAZ:MultiAZ}'

# 2. If primary is down, promote replica
echo "2. Promoting read replica..."
aws rds promote-read-replica \
  --db-instance-identifier rail-backend-eu-west-1 \
  --backup-retention-period 7

# 3. Wait for promotion
echo "3. Waiting for promotion (this may take several minutes)..."
aws rds wait db-instance-available \
  --db-instance-identifier rail-backend-eu-west-1

# 4. Update application configuration
echo "4. Updating application configuration..."
# Update DATABASE_URL in Secrets Manager
aws secretsmanager update-secret \
  --secret-id rail/prod/database \
  --secret-string "{\"host\":\"rail-backend-eu-west-1.c123456789.us-east-1.rds.amazonaws.com\"}"

# 5. Restart application
echo "5. Restarting application..."
aws ecs update-service \
  --cluster rail-backend-us-east-1 \
  --service rail-backend-us-east-1 \
  --force-new-deployment

echo ""
echo "=== Database Recovery Complete ==="
```

### Service Emergency Scale-Up

```bash
#!/bin/bash
# scripts/ops/emergency-scale-up.sh

set -e

REGION=${1:-us-east-1}
CLUSTER="rail-backend-${REGION}"

echo "=== Emergency Scale-Up ==="

# Scale up to maximum capacity
echo "Scaling up to 12 instances..."
aws ecs update-service \
  --cluster ${CLUSTER} \
  --service rail-backend-${REGION} \
  --desired-count 12 \
  --maximum-percent 200 \
  --minimum-healthy-percent 50

# Wait for scaling
echo "Waiting for instances to be running..."
aws ecs wait services-stable \
  --cluster ${CLUSTER} \
  --services rail-backend-${REGION}

echo "Scale-up complete!"
```

### DNS Emergency Failover

```bash
#!/bin/bash
# scripts/ops/dns-failover.sh

set -e

TARGET_REGION=${1:-eu-west-1}

echo "=== DNS Emergency Failover ==="

# Get hosted zone ID
HOSTED_ZONE_ID=$(aws route53 list-hosted-zones-by-name \
  --dns-name rail-service.com \
  --query 'HostedZones[0].Id' \
  --output text)

# Update DNS to point to secondary region
aws route53 change-resource-record-sets \
  --hosted-zone-id ${HOSTED_ZONE_ID} \
  --change-batch "$(cat <<EOF
{
  "Changes": [{
    "Action": "UPSERT",
    "ResourceRecordSet": {
      "Name": "api.rail-service.com",
      "Type": "A",
      "SetIdentifier": "${TARGET_REGION}-emergency",
      "TTL": 60,
      "MultiValueAnswer": true,
      "ResourceRecords": [
        {"Value": "alb.rail-backend-${TARGET_REGION}.elb.amazonaws.com"}
      ]
    }
  }]
}
EOF
)"

echo "DNS updated. Failover may take up to 60 seconds to propagate."
```

---

## Post-Incident Review

### Incident Report Template

```markdown
# Incident Report: INC-[NUMBER]

## Summary
- **Date:** [Date]
- **Duration:** [Duration]
- **Severity:** [SEV-1/2/3/4]
- **Status:** [Closed/Resolved]

## Timeline
| Time (UTC) | Event |
|------------|-------|
| HH:MM | Incident detected |
| HH:MM | Incident declared |
| HH:MM | Root cause identified |
| HH:MM | Fix implemented |
| HH:MM | Service restored |

## Impact
- **Users Affected:** [Number]
- **Transactions Impacted:** [Number]
- **Revenue Impact:** [Amount]
- ** SLA Impact:** [Yes/No]

## Root Cause
[Detailed explanation of root cause]

## Resolution
[How the issue was fixed]

## Lessons Learned
### What Went Well
- 

### What Could Be Improved
- 

## Action Items
| Action | Owner | Due Date | Status |
|--------|-------|----------|--------|
| | | | |
```

### Continuous Improvement

After each incident, update:

1. **Runbooks** - Add new troubleshooting steps
2. **Monitoring** - Add new alerts if needed
3. **Architecture** - Implement preventive changes
4. **Testing** - Add regression tests
5. **Documentation** - Update architecture diagrams

---

## Quick Reference

### Essential Commands

```bash
# Service health
curl https://api.rail-service.com/health

# Check deployment status
aws ecs describe-services --cluster rail-backend-us-east-1 --service rail-backend-us-east-1

# View logs
aws logs tail /ecs/rail-backend-us-east-1 --since 5m

# Check alerts
aws cloudwatch describe-alarms --state-value ALARM

# View metrics
aws cloudwatch get-metric-statistics --namespace AWS/ECS --metric-name MemoryUtilization
```

### Key URLs

| Service | URL |
|---------|-----|
| Production API | https://api.rail-service.com |
| Grafana | https://grafana.rail-service.com |
| PagerDuty | https://rail.pagerduty.com |
| AWS Console | https://console.aws.amazon.com |
| GitHub | https://github.com/rail-service/rail_backend |
| CI/CD | https://github.com/rail-service/rail_backend/actions |

### Emergency Contacts

| Contact | Phone | Slack |
|---------|-------|-------|
| On-Call Primary | PagerDuty | `@rail-oncall-primary` |
| On-Call Secondary | PagerDuty | `@rail-oncall-secondary` |
| SRE Team | N/A | `#sre-rail` |
| Security | N/A | `#security-incidents` |

---

**Document Version:** 1.0  
**Last Updated:** 2026-01-22  
**Next Review:** 2026-02-22
