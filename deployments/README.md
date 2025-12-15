# Deployment Guide

## Overview

This directory contains all deployment configurations for the RAIL service including:
- Kubernetes manifests
- Helm charts
- Terraform infrastructure as code
- CI/CD pipelines

## Quick Start

### Prerequisites
- kubectl configured with cluster access
- Helm 3.x installed
- Terraform 1.5+ installed (for infrastructure)
- Docker for local builds

### Deploy to Kubernetes

```bash
# Using kubectl
kubectl apply -f k8s/

# Using Helm
helm install rail-service ./helm/rail-service \
  --namespace production \
  --values ./helm/values-production.yaml
```

### Deploy Infrastructure with Terraform

```bash
cd terraform/environments/prod
terraform init
terraform plan
terraform apply
```

## Security

### Container Security
- Non-root user (UID 65534)
- Read-only root filesystem
- Dropped all capabilities
- Seccomp profile enabled

### Secrets Management
- AWS Secrets Manager integration via External Secrets Operator
- No secrets in Git or container images
- Automatic secret rotation support

### Network Security
- Network policies restrict pod communication
- TLS termination at ingress
- Rate limiting enabled

## CI/CD Pipeline

### Automated Workflows
- **Security Scanning**: Trivy, Gosec, Gitleaks
- **Testing**: Unit and integration tests with coverage
- **Build**: Multi-stage Docker builds with caching
- **Deploy**: Automated deployment to staging/production

### Deployment Strategies
- **Staging**: Rolling update on develop branch
- **Production**: Canary deployment (10% → 100%) on main branch
- **Rollback**: Automatic rollback on failure

## Monitoring

### Health Checks
- Liveness probe: `/health` endpoint
- Readiness probe: `/health` endpoint
- Startup delay: 30s

### Metrics
- Prometheus metrics exposed on `/metrics`
- Auto-scaling based on CPU/memory utilization

## Rollback

### Manual Rollback
```bash
# Using provided script
./scripts/rollback.sh

# Using Helm directly
helm rollback rail-service -n production
```

### Automatic Rollback
CI/CD pipeline automatically rolls back on deployment failure.

## Environment Configuration

### Staging
- 2-5 replicas
- Lower resource limits
- Develop branch auto-deploy

### Production
- 5-20 replicas
- Higher resource limits
- Canary deployment strategy
- Manual approval required
