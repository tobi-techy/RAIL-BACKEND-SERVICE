<story-context id="bmad/bmm/workflows/4-implementation/story-context/template" v="1.0">
  <metadata>
    <epicId>1</epicId>
    <storyId>2</storyId>
    <title>Passcode Authentication</title>
    <status>drafted</status>
    <generatedAt>2025-11-03</generatedAt>
    <generator>BMAD Story Context Workflow</generator>
    <sourceStoryPath>docs/stories/1-2-passcode-authentication.md</sourceStoryPath>
  </metadata>

  <story>
    <asA>user</asA>
    <iWant>authenticate using my passcode</iWant>
    <soThat>securely access the app</soThat>
    <tasks>- [ ] Implement passcode verification endpoint (AC: 1, 5)
  - [ ] Create VerifyPasscode handler in onboarding service
  - [ ] Add passcode verification logic with secure hash comparison
  - [ ] Implement proper error responses for invalid passcodes
- [ ] Implement secure passcode hashing (AC: 2, 3)
  - [ ] Use bcrypt/PBKDF2 with minimum 10 rounds for hash verification
  - [ ] Validate minimum 4-character passcode requirements
  - [ ] Store and retrieve passcode_hash from users table
- [ ] Add security and error handling (AC: 4, 6)
  - [ ] Implement rate limiting for passcode verification attempts
  - [ ] Add security logging for failed verification attempts
  - [ ] Handle edge cases (user not found, corrupted hash, etc.)
- [ ] Update GraphQL API (AC: 1, 5)
  - [ ] Add verifyPasscode mutation to GraphQL schema
  - [ ] Implement GraphQL resolver for passcode verification
  - [ ] Return appropriate authentication tokens on success</tasks>
  </story>

  <acceptanceCriteria>1. Users can authenticate using their passcode with proper verification
2. Passcode hashing uses secure algorithms (bcrypt/PBKDF2 with minimum 10 rounds)
3. Minimum 6-character passcodes required for security
4. Proper error handling for invalid passcodes and rate limiting
5. Passcode verification returns appropriate success/failure responses
6. Failed verification attempts are logged for security monitoring</acceptanceCriteria>

  <artifacts>
    <docs>
      <artifact>
        <path>docs/tech-spec-epic-1.md</path>
        <title>Epic Technical Specification: Onboarding & Wallet Management</title>
        <section>Passcode-Verification</section>
        <snippet>Passcode support for secure app login (hashing and verification). Passcode_hash uses secure hashing (bcrypt/PBKDF2)</snippet>
        <reason>Defines passcode requirements, hashing algorithms, and security standards</reason>
      </artifact>
      <artifact>
        <path>docs/prd.md</path>
        <title>Product Requirements Document</title>
        <section>Passcode-support-for-app-login</section>
        <snippet>Support for passcode-based app access. Passcode support for app login</snippet>
        <reason>Business requirements for passcode authentication feature</reason>
      </artifact>
      <artifact>
        <path>docs/architecture.md</path>
        <title>STACK Architecture Document</title>
        <section>Onboarding-Service-Module</section>
        <snippet>Handles user sign-up, profile management, KYC/AML orchestration, passcode setup/verification</snippet>
        <reason>Architecture patterns and service boundaries for authentication</reason>
      </artifact>
      <artifact>
        <path>docs/architecture/4-data-models.md</path>
        <title>Data Models</title>
        <section>users-table</section>
        <snippet>passcode_hash: String (Hashed passcode for app login)</snippet>
        <reason>Database schema for passcode storage and validation</reason>
      </artifact>
    </docs>
    <code>
      <artifact>
        <path>internal/domain/services/passcode/service.go</path>
        <kind>service</kind>
        <symbol>VerifyPasscode</symbol>
        <lines>1-100</lines>
        <reason>Existing passcode verification service implementation</reason>
      </artifact>
      <artifact>
        <path>internal/api/handlers/security_handlers.go</path>
        <kind>handler</kind>
        <symbol>VerifyPasscode</symbol>
        <lines>140-180</lines>
        <reason>HTTP handler for passcode verification endpoint</reason>
      </artifact>
      <artifact>
        <path>internal/infrastructure/repositories/user_repository.go</path>
        <kind>repository</kind>
        <symbol>GetPasscodeMetadata</symbol>
        <lines>654-690</lines>
        <reason>Database operations for passcode metadata retrieval</reason>
      </artifact>
      <artifact>
        <path>internal/domain/entities/security_entities.go</path>
        <kind>entity</kind>
        <symbol>PasscodeVerifyRequest</symbol>
        <lines>22-25</lines>
        <reason>Data structures for passcode verification requests</reason>
      </artifact>
    </code>
    <dependencies>
      <dependency>
        <name>golang.org/x/crypto</name>
        <version>v0.42.0</version>
        <purpose>Password hashing and cryptographic operations</purpose>
      </dependency>
      <dependency>
        <name>github.com/lib/pq</name>
        <version>v1.10.9</version>
        <purpose>PostgreSQL database driver</purpose>
      </dependency>
      <dependency>
        <name>github.com/go-redis/redis/v8</name>
        <version>v8.11.5</version>
        <purpose>Redis client for rate limiting and session management</purpose>
      </dependency>
      <dependency>
        <name>github.com/gin-gonic/gin</name>
        <version>v1.11.0</version>
        <purpose>HTTP web framework for API endpoints</purpose>
      </dependency>
    </dependencies>
  </artifacts>

  <constraints>Follow Repository Pattern for database access. Use secure bcrypt/PBKDF2 hashing with minimum 10 rounds. Implement rate limiting for security. Use structured JSON logging with correlation IDs. Validate all input at API boundary. Handle errors gracefully without exposing internals.</constraints>
  <interfaces>
    <interface>
      <name>PasscodeService.VerifyPasscode</name>
      <kind>service-method</kind>
      <signature>func (s *Service) VerifyPasscode(ctx context.Context, userID uuid.UUID, passcode string) error</signature>
      <path>internal/domain/services/passcode/service.go</path>
    </interface>
    <interface>
      <name>SecurityHandlers.VerifyPasscode</name>
      <kind>http-handler</kind>
      <signature>func (h *SecurityHandlers) VerifyPasscode(c *gin.Context)</signature>
      <path>internal/api/handlers/security_handlers.go</path>
    </interface>
    <interface>
      <name>UserRepository.GetPasscodeMetadata</name>
      <kind>repository-method</kind>
      <signature>func (r *UserRepository) GetPasscodeMetadata(ctx context.Context, userID uuid.UUID) (*entities.PasscodeMetadata, error)</signature>
      <path>internal/infrastructure/repositories/user_repository.go</path>
    </interface>
  </interfaces>
  <tests>
    <standards>Unit tests for service methods, integration tests for API endpoints, security testing for rate limiting. Use testify/assert for assertions and mocking. Test error conditions and edge cases.</standards>
    <locations>internal/domain/services/passcode/*_test.go, internal/api/handlers/*_test.go, internal/infrastructure/repositories/*_test.go</locations>
    <ideas>1. Test passcode verification with valid credentials - AC 1, 5
2. Test bcrypt hashing with minimum rounds requirement - AC 2
3. Test minimum passcode length validation - AC 3  
4. Test rate limiting for failed attempts - AC 4
5. Test error responses for invalid passcodes - AC 1, 5
6. Test security logging of failed attempts - AC 6</ideas>
  </tests>
</story-context>
