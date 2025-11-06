# Source Tree

**Version:** v0.2  
**Last Updated:** October 24, 2025

## Navigation
- **Previous:** [Database Schema](./05-database-schema.md)
- **Next:** [Infrastructure](./07-infrastructure.md)
- **[Index](./README.md)**

---

## 9. Source Tree

Given the choice of a **Go modular monolith** within a **monorepo**, the project structure should facilitate clear separation between modules, shared code, and infrastructure definitions. This structure assumes we're using Go workspaces within the monorepo.

```plaintext
stack-monorepo/
├── go.work                  # Go workspace file
├── go.mod                   # Root go.mod (can be minimal if using workspaces)
│
├── cmd/                     # Main application entrypoints
│   └── api/                 # Entrypoint for the main GraphQL API server
│       └── main.go
│
├── internal/                # Private application and library code
│   ├── api/                 # API layer (GraphQL handlers, resolvers)
│   │   ├── graph/           # GraphQL schema, generated code, resolvers
│   │   └── middleware/      # API middleware (auth, logging, etc.)
│   │
│   ├── core/                # Core business logic modules (domains)
│   │   ├── onboarding/
│   │   ├── wallet/
│   │   ├── funding/
│   │   ├── investing/
│   │   └── aicfo/
│   │
│   ├── adapters/            # External service integrations (clients)
│   │   ├── circle/
│   │   ├── Alpaca/
│   │   ├── authprovider/    # Auth0/Cognito client
│   │   ├── kycprovider/
│   │   └── zerog/           # 0G client
│   │
│   ├── persistence/         # Database interaction layer (repositories)
│   │   ├── postgres/        # PostgreSQL specific implementation
│   │   └── migrations/      # Database migration files
│   │
│   └── config/              # Configuration loading and management
│
├── pkg/                     # Shared libraries (can be imported by other projects)
│   └── common/              # Common utilities, types, errors
│
├── infrastructure/          # Infrastructure as Code (Terraform)
│   ├── aws/
│   │   ├── ecs/
│   │   ├── rds/
│   │   └── ...
│
├── api/                     # API specifications (GraphQL schema, OpenAPI for external?)
│   └── graph/
│       └── schema.graphqls
│
├── web/                     # Placeholder for React Native app (could be separate repo)
│
├── scripts/                 # Build, test, deployment scripts
│
├── .github/                 # GitHub specific files (Actions workflows)
│   └── workflows/
│
├── Dockerfile               # Dockerfile for the Go API service
├── docker-compose.yml       # For local development environment
├── .env.example             # Environment variable template
└── README.md
```

## Explanation

* **`go.work`:** Defines the Go workspace, allowing multiple modules within `internal/` and `pkg/` to be treated as part of the same build context.
* **`cmd/api/`:** Contains the `main.go` file which initializes and starts the Gin web server and GraphQL endpoint.
* **`internal/`:** Houses the core application logic, not intended to be imported by other Go projects outside this monorepo.
    * **`api/`:** Handles incoming GraphQL requests, routing them to the appropriate core service modules. Contains resolvers and API-specific middleware.
    * **`core/`:** Contains the primary business logic, organized by domain (Onboarding, Funding, etc.). Each domain module should define its own interfaces and structs.
    * **`adapters/`:** Implements clients for interacting with all external services (Circle, Alpaca, etc.), adhering to interfaces defined perhaps in `core/` or `pkg/`.
    * **`persistence/`:** Implements the Repository pattern for database interactions, specific to PostgreSQL (`lib/pq`).
    * **`config/`:** Handles loading configuration from environment variables or files.
* **`pkg/common/`:** Contains truly generic code (e.g., custom error types, utility functions, shared data structures) that *could* potentially be reused across different Go projects.
* **`infrastructure/`:** Terraform code for managing AWS resources.
* **`api/graph/`:** Location for the GraphQL schema files.
* **`web/`:** Placeholder. The React Native app might live here or in a separate repository depending on team preference.

---

**Next:** [Infrastructure](./07-infrastructure.md)
