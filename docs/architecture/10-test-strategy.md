# Test Strategy and Standards

**Version:** v0.2  
**Last Updated:** October 24, 2025

## Navigation
- **Previous:** [Coding Standards](./09-coding-standards.md)
- **[Index](./README.md)**

---

## 13. Test Strategy and Standards

This section defines the testing approach for the Go backend, ensuring code quality, reliability, and alignment with requirements.

### 13.1 Testing Philosophy
* **Approach:** Test-Driven Development (TDD) is encouraged but not strictly required. Comprehensive tests **must** be written for all critical paths, business logic, and integrations. Focus on testing behavior, not implementation details.
* **Coverage Goals:** Aim for >80% code coverage for core business logic modules. Integration tests should cover all major API endpoints and external interactions.
* **Test Pyramid:** Emphasize unit tests for isolated logic, followed by integration tests for module/service interactions, and a smaller set of end-to-end (E2E) tests covering critical user flows (though E2E might be owned by a separate QA process or team).

### 13.2 Test Types and Organization
* **Unit Tests:**
    * **Framework:** Go standard `testing` package, potentially augmented with `stretchr/testify` for assertions and mocking (`gomock` or `testify/mock`).
    * **File Convention:** `*_test.go` in the same package as the code under test.
    * **Location:** Same directory as the source code.
    * **Mocking:** Mock external dependencies (database repositories, external API adapters) using interfaces and mocking libraries.
    * **AI Agent Requirements:** Generate unit tests covering primary success paths, error conditions, and edge cases for all exported functions/methods in core modules. Follow Arrange-Act-Assert pattern.
* **Integration Tests:**
    * **Scope:** Test interactions between internal modules (e.g., API layer calling a core service module which interacts with persistence) and between the service and external dependencies (database, SQS, Redis, potentially mocked external APIs).
    * **Location:** Potentially in a separate `_test` package within the module or a dedicated top-level `test/integration` directory.
    * **Test Infrastructure:**
        * **Database:** Use **Testcontainers** to spin up a real PostgreSQL container for integration tests.
        * **Cache (Redis):** Use **Testcontainers** for a Redis container.
        * **Queue (SQS):** Use localstack or a similar AWS emulator, or Testcontainers with an SQS-compatible image.
        * **External APIs (Circle, Alpaca):** Use **WireMock** or a Go HTTP mocking library (like `go-vcr`) to stub responses during integration tests.
* **End-to-End (E2E) Tests (Minimal for Backend Focus):**
    * **Framework:** TBD (Potentially simple Go HTTP client tests, or dedicated frameworks like Cypress/Playwright if testing via UI).
    * **Scope:** Verify critical API flows from request to response, potentially including database state changes.
    * **Environment:** Run against a deployed staging environment.

### 13.3 Test Data Management
* **Strategy:** Use helper functions or fixture libraries within tests to create necessary prerequisite data (e.g., creating a user before testing wallet creation). Avoid relying on a shared, mutable test database state.
* **Fixtures:** Store reusable setup data (e.g., JSON payloads for mocks) in `testdata` subdirectories.
* **Cleanup:** Tests should clean up after themselves where possible (e.g., deleting created records), especially when interacting with external mocked services or shared resources like containers. Testcontainers helps manage container lifecycles.

### 13.4 Continuous Testing
* **CI Integration:** GitHub Actions will run `go test ./...` (including `-race` flag) on every pull request. Build must pass tests before merging.
* **Performance Tests:** Basic load testing (e.g., using `k6`, `vegeta`, or `go-wrk`) can be added to the CI pipeline for key endpoints, run against the staging environment.
* **Security Tests:** Integrate static analysis security testing (SAST) tools (like `gosec`) into the CI pipeline.

---

**[Back to Index](./README.md)**
