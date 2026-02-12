# Multi-Region Deployment Architecture

## Overview

This document describes the multi-region deployment architecture for the RAIL Backend service, enabling high availability, disaster recovery, and low-latency access globally.

## Architecture Goals

1. **High Availability**: 99.9% uptime SLA across regions
2. **Disaster Recovery**: RTO < 15 minutes, RPO < 5 minutes
3. **Global Latency**: < 100ms response time for 95% of users
4. **Data Consistency**: Strong consistency for financial transactions
5. **Cost Optimization**: Pay for active/passive resources efficiently

## Regional Architecture

### Active-Active Configuration

```
                        ┌─────────────────────────────────────────────────────────┐
                        │                  AWS Global Infrastructure              │
                        │                                                         │
┌───────────────────────┼─────────────────────────────────────────────────────────┼───────────────────────┐
│     us-east-1         │                    Route 53                     │        eu-west-1       │
│   (Primary)           │                 Latency-Based                    │     (Secondary)        │
│                       │                  Routing                         │                       │
│  ┌───────────────┐    │                                                  │   ┌───────────────┐    │
│  │  Application  │    │                                                  │   │  Application  │    │
│  │  ALB (443)    │    │                                                  │   │  ALB (443)    │    │
│  └───────┬───────┘    │                                                  │   └───────┬───────┘    │
│          │            │                                                  │           │            │
│  ┌───────▼───────┐    │                                                  │   ┌───────▼───────┐    │
│  │  ECS Cluster  │    │                                                  │   │  ECS Cluster  │    │
│  │  (6+ tasks)   │    │                                                  │   │  (6+ tasks)   │    │
│  └───────┬───────┘    │                                                  │   └───────┬───────┘    │
│          │            │                                                  │           │            │
│  ┌───────▼───────┐    │                                                  │   ┌───────▼───────┐    │
│  │  PostgreSQL   │    │                                                  │   │  Read Replica │    │
│  │  Primary      │◄───┼──────────────────────────────────────────────────┼───│  (async)      │    │
│  └───────────────┘    │                    S3 Cross-Region               │   └───────────────┘    │
│                       │                    Replication                    │                       │
│  ┌───────────────────┐│                    (optional)                    │   ┌───────────────────┐│
│  │ ElastiCache       ││                                                  │   │ ElastiCache       ││
│  │ Redis Cluster     ││                                                  │   │ Redis Cluster     ││
│  └───────────────────┘│                                                  │   └───────────────────┘│
└───────────────────────┼─────────────────────────────────────────────────────────┼───────────────────────┘
                        │                                                         │
                        │                    S3 + CloudFront CDN                   │
                        │                                                         │
                        └─────────────────────────────────────────────────────────┘
```

### Region Selection

| Region | Code | Use Case | Latency Target |
|--------|------|----------|----------------|
| US East (N. Virginia) | us-east-1 | Primary for Americas | < 50ms |
| EU West (Ireland) | eu-west-1 | Secondary for EMEA | < 100ms |
| Asia Pacific (Tokyo) | ap-northeast-1 | Optional APAC | < 150ms |

## Database Configuration

### PostgreSQL Multi-Region Setup

```yaml
# configs/config.yaml - Multi-region database configuration
database:
  # Primary region (writer)
  primary:
    host: "${PRIMARY_DB_HOST}"
    port: 5432
    name: "rail_service"
    ssl_mode: "require"
    max_connections: 100

  # Read replicas for each region
  read_replicas:
    - region: "us-east-1"
      host: "${US_EAST_1_DB_HOST}"
      weight: 60  # 60% of read traffic in primary region
    - region: "eu-west-1"  
      host: "${EU_WEST_1_DB_HOST}"
      weight: 30  # 30% of read traffic in EU
    - region: "ap-northeast-1"
      host: "${AP_NORTHEAST_1_DB_HOST}"
      weight: 10  # 10% for APAC

  # Replication settings
  replication:
    async: true  # Async replication for performance
    wal_level: "logical"
    max_wal_senders: 10
    wal_keep_size: "1GB"

  # Failover configuration
  failover:
    enabled: true
    promotion_timeout: 60  # seconds
    health_check_interval: 10  # seconds
```

### Connection Pooling

```go
// internal/infrastructure/database/multi_region.go

type MultiRegionConfig struct {
    Primary        ConnectionConfig
    ReadReplicas   []ReadReplicaConfig
    RoutingStrategy string  // "latency", "weight", "geo"
    FailoverEnabled bool
}

type ReadReplicaConfig struct {
    Region        string
    Host          string
    Weight        int  // Traffic distribution weight
    LatencyTarget int  // ms - for latency-based routing
}

// ReadWriteSplitter routes writes to primary, reads to replicas
type ReadWriteSplitter struct {
    primary      *sql.DB
    replicas     []*sql.DB
    weights      []int
    routingMode  string
}
```

## Redis Cluster Configuration

### Global Redis Topology

```yaml
# configs/config.yaml - Redis multi-region configuration
redis:
  # Local cluster for each region
  local_cluster:
    enabled: true
    mode: "cluster"  # or "sentinel" for HA
    nodes:
      - host: "${REDIS_NODE_1}"
        port: 6379
      - host: "${REDIS_NODE_2}"
        port: 6379
      - host: "${REDIS_NODE_3}"
        port: 6379

  # Global key distribution
  global_keys:
    prefix: "rail:global"
    # Keys that must be consistent across regions
    replicated_keys:
      - "session:*"
      - "rate_limit:*"
      - "token_blacklist:*"

  # Cross-region replication
  cross_region_replication:
    enabled: true
    strategy: "active-passive"  # or "active-active" for CRDT
    source_region: "us-east-1"
    target_regions:
      - "eu-west-1"
      - "ap-northeast-1"
    conflict_resolution: "last-write-wins"
```

## Load Balancing Strategy

### Latency-Based Routing

```yaml
# deployments/multi-region/route53-latency-policy.json
{
  "Name": "rail-backend-latency",
  "RoutingPolicy": "LATENCY",
  "HealthCheckConfig": {
    "HealthThreshold": 2,
    "UnhealthyThreshold": 3
  },
  "RegionRecords": [
    {
      "Region": "us-east-1",
      "SetIdentifier": "primary",
      "TTL": 30,
      "HealthCheck": "health-check-id-us-east-1"
    },
    {
      "Region": "eu-west-1",
      "SetIdentifier": "secondary", 
      "TTL": 30,
      "HealthCheck": "health-check-id-eu-west-1"
    }
  ]
}
```

### Application Load Balancer Configuration

```yaml
# deployments/multi-region/alb-config.yaml
us-east-1:
  alb:
    name: "rail-backend-primary"
    scheme: "internet-facing"
    security_groups:
      - "sg-primary-http"
      - "sg-primary-https"
    listeners:
      - port: 443
        protocol: "HTTPS"
        certificates:
          - "arn:aws:acm:us-east-1:123456789:certificate/xxx"
        rules:
          - priority: 1
            conditions:
              - host: "api.rail-service.com"
            actions:
              - type: "forward"
                target_group: "rail-backend-tg"
    
  target_groups:
    - name: "rail-backend-tg"
      port: 8080
      protocol: "HTTP"
      health_check:
        path: "/health"
        interval: 30
        timeout: 10
        healthy_threshold: 2
        unhealthy_threshold: 3
      # Weighted target groups for blue/green deployment
      targets:
        - id: "arn:aws:ecs:us-east-1:123456789:task/xxx"
          weight: 100

eu-west-1:
  # Same structure as us-east-1
  alb:
    name: "rail-backend-secondary"
```

## Failover Strategy

### Automatic Failover Process

```
1. Health Check Failure Detection (30 seconds)
   ├─ Route 53 health checks detect unhealthy endpoint
   ├─ ELB marks target group as unhealthy
   └─ CloudWatch Alarm triggers

2. Traffic Routing (1-2 minutes)
   ├─ Route 53 removes failed region from DNS
   ├─ Latency-based routing directs to healthy region
   └─ Global Accelerator adjusts routing

3. Database Failover (60-120 seconds)
   ├─ PostgreSQL automatic promotion of read replica
   ├─ Connection strings updated in Secrets Manager
   └─ Application reconnects with new credentials

4. Application Recovery (30-60 seconds)
   ├─ ECS tasks scale up in healthy region
   ├─ Application validates database connectivity
   └─ Health checks pass, traffic restored

Total RTO: 2-4 minutes
```

### Failover Configuration

```yaml
# deployments/multi-region/failover-config.yaml
failover:
  enabled: true
  
  # Detection
  health_checks:
    - type: "http"
      path: "/health"
      interval: 10  # seconds
      timeout: 5
      threshold: 3  # consecutive failures before failover
      
  # DNS failover
  dns:
    provider: "route53"
    ttl: 30  # seconds
    evaluation_periods: 1
    
  # Database failover
  database:
    promotion_timeout: 120  # seconds
    wal_replication: true
    logical_replication: true
    
  # Notification
  notifications:
    sns_topic: "arn:aws:sns:us-east-1:123456789:rail-failover"
    pagerduty_integration: true
    
  # Rollback
  rollback:
    enabled: true
    wait_period: 300  # seconds before auto-rollback
```

## Data Replication Strategy

### Asynchronous Replication

```
Write Path:
┌─────────┐     ┌─────────────┐     ┌──────────────────┐
│ Client  │────▶│ Application │────▶│ PostgreSQL       │
│ Request │     │ (us-east-1) │     │ Primary          │
└─────────┘     └─────────────┘     └────────┬─────────┘
                                             │
                          WAL Stream         │ Async Replication
                                             ▼
                            ┌─────────────────────────────┐
                            │ PostgreSQL Read Replicas    │
                            │ - eu-west-1 (30% traffic)  │
                            │ - ap-northeast-1 (10%)     │
                            └─────────────────────────────┘

Read Path:
┌─────────┐     ┌─────────────┐     ┌──────────────────┐
│ Client  │────▶│ Latency     │────▶│ Nearest Replica  │
│ Request │     │ Router      │     │ (lowest latency) │
└─────────┘     └─────────────┘     └──────────────────┘
```

### Cross-Region S3 Replication

```yaml
# deployments/multi-region/s3-replication.yaml
source:
  bucket: "rail-audit-logs-us-east-1"
  region: "us-east-1"

destinations:
  - bucket: "rail-audit-logs-eu-west-1"
    region: "eu-west-1"
    storage_class: "STANDARD_IA"
    replication_time:
      minutes: 15  # Typical replication time

  - bucket: "rail-backups-us-west-2"
    region: "us-west-2"
    storage_class: "GLACIER"
    replication_time:
      hours: 24

# S3 Object Lock for compliance (WORM)
object_lock:
  enabled: true
  mode: "COMPLIANCE"  # Cannot be overwritten/deleted
  retention_days: 2555  # 7 years for PCI-DSS
```

## Deployment Procedures

### Blue-Green Deployment

```bash
#!/bin/bash
# deployments/multi-region/deploy-blue-green.sh

set -e

DEPLOY_REGION=${1:-us-east-1}
BLUE_GREEN_COLOR=${2:-blue}

# Variables
CLUSTER_NAME="rail-backend-${DEPLOY_REGION}"
SERVICE_NAME="rail-backend-${DEPLOY_REGION}"
TASK_DEFINITION="rail-backend:${IMAGE_TAG}"
DESIRED_COUNT=6

echo "Deploying ${BLUE_GREEN_COLOR} environment in ${DEPLOY_REGION}..."

# Update task definition with new image
aws ecs register-task-definition \
  --family "rail-backend-${BLUE_GREEN_COLOR}" \
  --task-role-arn "arn:aws:iam::123456789:role/rail-backend-task" \
  --network-mode "awsvpc" \
  --container-definitions "$(cat <<EOF
[
  {
    "name": "rail-backend",
    "image": "${ECR_REPOSITORY_URL}:${IMAGE_TAG}",
    "cpu": 1024,
    "memory": 2048,
    "essential": true,
    "portMappings": [{"containerPort": 8080, "protocol": "tcp"}],
    "environment": [
      {"name": "ENVIRONMENT", "value": "production"},
      {"name": "REGION", "value": "${DEPLOY_REGION}"}
    ],
    "logConfiguration": {
      "logDriver": "awslogs",
      "options": {
        "awslogs-group": "/ecs/rail-backend-${DEPLOY_REGION}",
        "awslogs-region": "${DEPLOY_REGION}",
        "awslogs-stream-prefix": "ecs"
      }
    },
    "healthCheck": {
      "command": ["CMD-SHELL", "curl -f http://localhost:8080/health || exit 1"],
      "interval": 30,
      "timeout": 5,
      "retries": 3,
      "startPeriod": 60
    }
  }
]
EOF
)"

# Update service with new task definition
aws ecs update-service \
  --cluster "${CLUSTER_NAME}" \
  --service "${SERVICE_NAME}-${BLUE_GREEN_COLOR}" \
  --task-definition "rail-backend-${BLUE_GREEN_COLOR}:${IMAGE_TAG}" \
  --desired-count "${DESIRED_COUNT}" \
  --deployment-configuration "maximumPercent=200,minimumHealthyPercent=100"

# Wait for deployment
aws ecs wait services-stable \
  --cluster "${CLUSTER_NAME}" \
  --services "${SERVICE_NAME}-${BLUE_GREEN_COLOR}"

# Verify health
HEALTH_STATUS=$(aws ecs describe-services \
  --cluster "${CLUSTER_NAME}" \
  --services "${SERVICE_NAME}-${BLUE_GREEN_COLOR}" \
  --query "services[0].deployments[0].rolloutState" \
  --output text)

if [ "$HEALTH_STATUS" == "COMPLETED" ]; then
  echo "✅ Deployment successful!"
  
  # Update ALB target group
  TARGET_GROUP_ARN=$(aws elbv2 describe-target-groups \
    --names "rail-backend-${BLUE_GREEN_COLOR}" \
    --query "TargetGroups[0].TargetGroupArn" \
    --output text)
  
  aws elbv2 register-targets \
    --target-group-arn "${TARGET_GROUP_ARN}" \
    --targets "Id=$(aws ecs list-container-instances \
      --cluster "${CLUSTER_NAME}" \
      --query "containerInstanceArns[0]" \
      --output text),Port=8080"
    
else
  echo "❌ Deployment failed, initiating rollback..."
  # Rollback logic here
fi
```

### Region Failover Test

```bash
#!/bin/bash
# deployments/multi-region/test-failover.sh

set -e

PRIMARY_REGION="us-east-1"
SECONDARY_REGION="eu-west-1"

echo "=== Multi-Region Failover Test ==="

# 1. Check current health
echo "1. Checking current health..."
PRIMARY_HEALTH=$(aws elbv2 describe-target-health \
  --target-group-arn "${PRIMARY_TG_ARN}" \
  --query 'TargetHealthDescriptions[0].TargetHealth.State' \
  --output text)

SECONDARY_HEALTH=$(aws elbv2 describe-target-health \
  --target-group-arn "${SECONDARY_TG_ARN}" \
  --query 'TargetHealthDescriptions[0].TargetHealth.State' \
  --output text)

echo "Primary region health: ${PRIMARY_HEALTH}"
echo "Secondary region health: ${SECONDARY_HEALTH}"

# 2. Simulate primary failure
echo "2. Simulating primary region failure..."
# This would normally be done by triggering a canary deployment
# or by using AWS Fault Injection Service

# 3. Verify traffic shift
echo "3. Verifying traffic shift..."
sleep 60

NEW_TRAFFIC=$(aws route53 get-traffic-policy-instance \
  --id "${ROUTE53_INSTANCE_ID}" \
  --query 'TrafficPolicyInstance.TrafficPolicy' \
  --output json | jq '.Rules[0].Items[0].Weight')

echo "Traffic distribution: ${NEW_TRAFFIC}"

# 4. Verify database failover
echo "4. Verifying database failover..."
PGPASSWORD="${DB_PASSWORD}" psql \
  --host "${SECONDARY_DB_HOST}" \
  --port 5432 \
  -c "SELECT pg_is_in_recovery();"

# 5. Test application endpoints
echo "5. Testing application endpoints..."
curl -f "https://api.rail-service.com/health"
curl -f "https://api.rail-service.com/api/v1/users/me" \
  -H "Authorization: Bearer ${TEST_TOKEN}"

echo "=== Failover Test Complete ==="
```

## Monitoring and Observability

### Cross-Region Metrics

```yaml
# deployments/multi-region/cloudwatch-dashboard.json
{
  "widgets": [
    {
      "type": "metric",
      "x": 0, "y": 0,
      "width": 12, "height": 6,
      "properties": {
        "title": "Request Count by Region",
        "metrics": [
          ["AWS/ApplicationELB", "RequestCount", "Region", "us-east-1", "LoadBalancer", "app/rail-backend-primary"],
          [".", ".", ".", "eu-west-1", ".", "app/rail-backend-secondary"]
        ],
        "period": 60,
        "stat": "Sum"
      }
    },
    {
      "type": "metric",
      "x": 12, "y": 0,
      "width": 12, "height": 6,
      "properties": {
        "title": "Latency by Region",
        "metrics": [
          ["AWS/ApplicationELB", "AverageLatency", "Region", "us-east-1", "LoadBalancer", "app/rail-backend-primary"],
          [".", ".", ".", "eu-west-1", ".", "app/rail-backend-secondary"]
        ],
        "period": 60,
        "stat": "Average"
      }
    },
    {
      "type": "metric",
      "x": 0, "y": 6,
      "width": 12, "height": 6,
      "properties": {
        "title": "Database Replication Lag",
        "metrics": [
          ["AWS/RDS", "ReplicaLag", "DBInstanceIdentifier", "rail-backend-eu-west-1"]
        ],
        "period": 60,
        "stat": "Maximum"
      }
    },
    {
      "type": "metric",
      "x": 12, "y": 6,
      "width": 12, "height": 6,
      "properties": {
        "title": "5xx Errors by Region",
        "metrics": [
          ["AWS/ApplicationELB", "HTTPCode_Backend_5XX", "Region", "us-east-1", "LoadBalancer", "app/rail-backend-primary"],
          [".", ".", ".", "eu-west-1", ".", "app/rail-backend-secondary"]
        ],
        "period": 60,
        "stat": "Sum"
      }
    }
  ]
}
```

### Alerting Configuration

```yaml
# deployments/multi-region/alerts.yaml
alerts:
  # Primary region down
  - name: "PrimaryRegionDown"
    condition: "PrimaryRegionHealthy == false"
    severity: "critical"
    action: "InitiateFailover"
    
  # Replication lag too high
  - name: "ReplicationLagHigh"
    condition: "ReplicaLag > 30"  # seconds
    severity: "warning"
    action: "NotifyOnCall"
    
  # Cross-region latency degraded
  - name: "CrossRegionLatencyHigh"
    condition: "Latency > 500"  # ms for 95th percentile
    severity: "warning"
    action: "InvestigateNetwork"
    
  # Health check failures
  - name: "HealthCheckFailing"
    condition: "FailedHealthChecks > 3"
    severity: "critical"
    action: "FailoverIfPrimary"
```

## Cost Optimization

### Resource Sizing by Region

| Resource | Primary (us-east-1) | Secondary (eu-west-1) | Cost Strategy |
|----------|--------------------|-----------------------|---------------|
| ECS Tasks | 6 (100% capacity) | 6 (warm standby) | Use scheduled scaling |
| RDS Primary | db.r6g.2xlarge | db.r6g.xlarge (replica) | Reserved instances |
| ElastiCache | cache.r6g.large | cache.r6g.large (replica) | Use replication |
| ALB | Standard | Standard | Single ALB per region |
| Data Transfer | $0.02/GB | $0.02/GB | Compress data |

### Scheduled Scaling

```yaml
# deployments/multi-region/scheduled-scaling.yaml
schedules:
  - name: "WeekdayPrimaryScale"
    schedule: "cron(0 8 * * 1-5 *)"  # 8 AM weekdays
    timezone: "America/New_York"
    scaling:
      primary:
        min: 6
        max: 12
      secondary:
        min: 2
        max: 6
        
  - name: "WeekendPrimaryScale"
    schedule: "cron(0 20 * * 5-0 *)"  # 8 PM Fri-Sun
    timezone: "America/New_York"
    scaling:
      primary:
        min: 4
        max: 8
      secondary:
        min: 2
        max: 4
```

## Rollback Procedures

### Manual Rollback

```bash
#!/bin/bash
# deployments/multi-region/rollback.sh

DEPLOY_REGION=${1:-us-east-1}
PREVIOUS_TASK_DEF=$(aws ecs list-task-definitions \
  --family-prefix "rail-backend" \
  --status "ACTIVE" \
  --sort "DESC" \
  --query "taskDefinitionArns[1]" \
  --output text)

aws ecs update-service \
  --cluster "rail-backend-${DEPLOY_REGION}" \
  --service "rail-backend-${DEPLOY_REGION}" \
  --task-definition "${PREVIOUS_TASK_DEF}" \
  --desired-count 6
```

### Automatic Rollback

```yaml
# deployments/multi-region/rollback-config.yaml
rollback:
  enabled: true
  trigger:
    error_rate_threshold: 0.05  # 5% error rate
    latency_threshold: 2000  # 2 seconds p99
    health_check_failure: true
    
  actions:
    - pause_deployment
    - send_notification
    - rollback_to_previous
    - alert_on_call
    
  timing:
    evaluation_period: 60  # seconds
    wait_after_trigger: 120  # seconds
    max_rollback_time: 300  # seconds
```
