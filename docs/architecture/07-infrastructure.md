# Infrastructure and Deployment

**Version:** v0.2  
**Last Updated:** October 24, 2025

## Navigation
- **Previous:** [Source Tree](./06-source-tree.md)
- **Next:** [Error Handling](./08-error-handling.md)
- **[Index](./README.md)**

---

## 10. Infrastructure and Deployment

This section defines the cloud infrastructure setup on AWS and the CI/CD process for deploying the Go backend application.

### 10.1 Infrastructure as Code

* **Tool:** Terraform v1.6.x.
* **Location:** `infrastructure/aws/` within the monorepo.
* **Approach:** Define reusable Terraform modules for core components (ECS Service, RDS Database, SQS Queue, ElastiCache Redis Cluster). Manage environments (dev, staging, prod) using Terraform workspaces or separate state files. State will be stored remotely (e.g., in S3 with locking via DynamoDB).

### 10.2 Deployment Strategy

* **Strategy:** Blue/Green deployments for the Go API service on AWS Fargate (ECS). This allows for zero-downtime releases. Traffic shifting will be managed via Application Load Balancer (ALB) target groups or potentially AWS CodeDeploy.
* **CI/CD Platform:** GitHub Actions.
* **Pipeline Configuration:** Workflow files located in `.github/workflows/`. Separate workflows for build/test (on PR), staging deploy (on merge to `main`), and production deploy (manual trigger or tag).

### 10.3 Environments

* **Development:** Local development using Docker Compose to spin up Postgres, Redis, and potentially mocks for external services.
* **Staging:** Deployed on AWS, mirroring production infrastructure but scaled down. Used for end-to-end testing and QA. Connected to partner sandbox APIs.
* **Production:** Deployed on AWS, configured for high availability and scalability. Connected to partner production APIs.

### 10.4 Environment Promotion Flow

```text
[Local Dev] -> [Feature Branch PR] -> [CI Tests Pass] -> [Merge to main] -> [Staging Deploy] -> [Manual QA/Tests] -> [Promote to Production (Manual Trigger/Tag)] -> [Production Deploy (Blue/Green)]
```

### 10.5 Rollback Strategy

* **Primary Method:** Utilize ECS Blue/Green deployment capabilities. If the "green" deployment fails health checks or post-deployment tests, traffic remains on the "blue" (previous stable) version. Manual rollback involves redirecting ALB traffic back to the blue target group.
* **Trigger Conditions:** Failed health checks post-deployment, critical error rate spikes detected by monitoring, failed automated post-deployment smoke tests.
* **Database Rollback:** Handled via migration tooling (e.g., `goose down`). Critical schema changes should be backward compatible where possible to facilitate easier rollbacks. Non-reversible migrations require careful planning and potential data migration rollbacks.

---

**Next:** [Error Handling](./08-error-handling.md)
