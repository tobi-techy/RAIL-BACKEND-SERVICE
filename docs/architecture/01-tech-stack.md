# Tech Stack

**Version:** v0.2  
**Last Updated:** October 24, 2025

## Navigation
- **Previous:** [Overview](./00-overview.md)
- **Next:** [Data Models](./02-data-models.md)
- **[Index](./README.md)**

---

## 3. Tech Stack

This section defines the specific technologies and versions that **MUST** be used for the STACK MVP implementation. These choices are based on the **Go** backend requirement, **React Native** frontend, and integration with **Circle** and **Alpaca**.

**Rationale:** The stack prioritizes mature ecosystems, type safety where possible (Go/TypeScript), and alignment with our chosen partners and cloud platform (AWS). Specific versions are pinned to ensure consistency and avoid unexpected breaking changes.

### 3.1 Cloud Infrastructure
* **Provider:** AWS
* **Key Services:** Fargate (ECS), RDS (PostgreSQL), **ElastiCache (Redis)**, SQS, Secrets Manager, S3, API Gateway (REST/GraphQL endpoint), CloudFront, WAF
* **Deployment Regions:** TBD (e.g., us-east-1)

### 3.2 Technology Stack Table

| Category           | Technology                    | Version         | Purpose                                          | Rationale                                                                 |
| :----------------- | :---------------------------- | :-------------- | :----------------------------------------------- | :------------------------------------------------------------------------ |
| **Backend Lang** | Go                            | 1.21.x          | Primary backend language                         | Mandated requirement, performant, strong concurrency model                |
| **Backend Fmwk** | **Gin** | **v1.11.0** | Web framework for API                            | User specified, high-performance, minimalist Go framework                 |
| **Database** | PostgreSQL                    | 15.x            | Primary relational data store                    | Mature, reliable, supports JSONB, RDS managed service       |
| **ORM/DB Driver** | **lib/pq** | **Latest** | PostgreSQL driver for Go                         | User specified, standard Go driver for PostgreSQL                         |
| **Migration Tool** | TBD (e.g., goose, migrate)    | TBD             | Database schema migrations                       | Need standard Go migration tool                                           |
| **Queueing** | AWS SQS                       | N/A (AWS SDK)   | Asynchronous task processing (funding flow)      | Managed service, integrates well with Go SDK                              |
| **Cache** | **Redis** | **7.x** | Caching layer                                    | User specified via ecosystem choice, use with ElastiCache                 |
| **Cache Client (Go)**| TBD (e.g., go-redis)        | TBD             | Go client library for Redis                      | Needed to interact with Redis from Go                                     |
| **Frontend Lang** | TypeScript                    | 5.x             | Language for React Native app                    | Type safety, improves developer experience                                |
| **Frontend Fmwk** | React Native                  | 0.72.x          | Cross-platform mobile framework                  | Specified requirement                                      |
| **State Mgmt (FE)**| TBD (e.g., Zustand, Redux TK) | TBD             | Frontend state management                        | Choice depends on app complexity, Zustand is lighter                      |
| **API Style** | GraphQL                       | TBD (Lib: gqlgen)| API for mobile client                            | Approved, efficient data fetching for mobile, Go lib needed              |
| **API Docs (BE)** | **gin-swagger** | **Latest** | Swagger/OpenAPI generation for Gin             | User specified, standard for documenting Gin APIs                         |
| **Auth Provider** | TBD (Auth0 / Cognito)         | N/A             | User identity and authentication                 | Standard OIDC providers, need final selection                             |
| **Wallet** | Circle API                    | Latest          | Developer Wallets (custody only)               | Mandated requirement                                       |
| **Off-Ramp/On-Ramp** | Due API                      | Latest          | USDC <-> USD conversion, Virtual Accounts, Recipient Mgmt | Technical constraint resolution                             |
| **KYC/AML** | Sumsub API                   | Latest          | Identity verification and compliance           | Regulatory requirement                                      |
| **Brokerage** | Alpaca API               | Latest          | Stock/Options trading, Custody                 | Mandated requirement                                       |
| **AI/Storage** | 0G                            | Latest          | AI CFO features, data storage                  | Specified requirement                                      |
| **IaC Tool** | Terraform                     | 1.6.x           | Infrastructure as Code                           | Industry standard, manages AWS resources                                  |
| **CI/CD** | GitHub Actions                | N/A             | Continuous Integration/Deployment                | Integrates with GitHub repo                       |
| **Containerization**| Docker                        | Latest          | Container builds                                 | Standard for Fargate deployment                                           |
| **Logging (BE)** | **Zap** | **Latest** | Structured logging in Go                         | User specified, performant structured logger                              |
| **Tracing (BE)** | **OpenTelemetry** | **Latest** | Distributed tracing                              | User specified, standard for observability                                |
| **Metrics (BE)** | **Prometheus Client (Go)** | **Latest** | Exposing application metrics                     | User specified via ecosystem choice                                       |
| **Circuit Breaker**| **gobreaker** | **Latest** | Resilience pattern for external calls            | User specified via ecosystem choice                                       |
| **Monitoring** | AWS CloudWatch / Datadog      | N/A             | Metrics, Logs, Traces                            | CloudWatch default, Datadog if more advanced features needed             |

---

**Next:** [Data Models](./02-data-models.md)
