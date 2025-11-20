# Swagger API Documentation Guide

## Quick Start

### Accessing Swagger UI

1. **Start the application:**
   ```bash
   make run
   # or
   go run cmd/main.go
   ```

2. **Open Swagger UI in your browser:**
   ```
   http://localhost:8080/swagger/index.html
   ```

3. **You should see the interactive API documentation with all endpoints listed**

## Regenerating Documentation

### Using Make

```bash
make swagger
```

### Manual Generation

```bash
# Install swag if not already installed
go install github.com/swaggo/swag/cmd/swag@latest

# Generate documentation
swag init -g cmd/main.go -o docs/swagger --parseDependency --parseInternal
```

### After Code Changes

Regenerate Swagger docs whenever you:
- Add new API endpoints
- Modify request/response structures
- Update endpoint descriptions
- Change authentication requirements

## Using Swagger UI

### Testing Endpoints

1. **Authenticate:**
   - Click the "Authorize" button (lock icon) at the top right
   - Enter your JWT token in the format: `Bearer <your-token>`
   - Click "Authorize" then "Close"

2. **Try an endpoint:**
   - Expand any endpoint (e.g., `GET /wallet/addresses`)
   - Click "Try it out"
   - Fill in required parameters
   - Click "Execute"
   - View the response below

### Getting a Test Token

1. **Register a user:**
   ```bash
   curl -X POST http://localhost:8080/api/v1/auth/register \
     -H "Content-Type: application/json" \
     -d '{"email":"test@example.com","password":"Test123!"}'
   ```

2. **Verify with code (check logs for code):**
   ```bash
   curl -X POST http://localhost:8080/api/v1/auth/verify-code \
     -H "Content-Type: application/json" \
     -d '{"email":"test@example.com","code":"123456"}'
   ```

3. **Copy the `accessToken` from the response**

4. **Use in Swagger UI:**
   - Click "Authorize"
   - Paste: `Bearer <accessToken>`

## Swagger Annotations Reference

### Basic Endpoint Annotation

```go
// HandlerName handles the endpoint
// @Summary Short description
// @Description Detailed description
// @Tags category
// @Accept json
// @Produce json
// @Param paramName path string true "Parameter description"
// @Param body body RequestType true "Request body description"
// @Success 200 {object} ResponseType "Success description"
// @Failure 400 {object} ErrorResponse "Error description"
// @Security BearerAuth
// @Router /api/v1/endpoint [get]
func HandlerName(c *gin.Context) {
    // Handler implementation
}
```

### Common Annotations

#### Tags
Groups endpoints in Swagger UI:
```go
// @Tags auth
// @Tags wallets
// @Tags funding
```

#### Parameters
```go
// Path parameter
// @Param id path string true "User ID"

// Query parameter
// @Param page query int false "Page number"

// Body parameter
// @Param request body entities.LoginRequest true "Login credentials"
```

#### Responses
```go
// Success response
// @Success 200 {object} entities.User "User details"

// Error responses
// @Failure 400 {object} entities.ErrorResponse "Bad request"
// @Failure 401 {object} entities.ErrorResponse "Unauthorized"
// @Failure 404 {object} entities.ErrorResponse "Not found"
// @Failure 500 {object} entities.ErrorResponse "Internal error"
```

#### Security
```go
// Requires authentication
// @Security BearerAuth

// No authentication required (omit this line)
```

## File Structure

```
docs/
├── swagger/
│   ├── docs.go          # Generated Go documentation
│   ├── swagger.json     # OpenAPI JSON specification
│   └── swagger.yaml     # OpenAPI YAML specification
├── API_DOCUMENTATION.md # Human-readable API docs
└── SWAGGER_GUIDE.md     # This file
```

## Swagger Configuration

The main Swagger configuration is in `cmd/main.go`:

```go
// @title Stack Service API
// @version 1.0
// @description GenZ Web3 Multi-Chain Investment Platform API
// @termsOfService http://swagger.io/terms/

// @contact.name API Support
// @contact.url http://www.stackservice.com/support
// @contact.email support@stackservice.com

// @license.name Apache 2.0
// @license.url http://www.apache.org/licenses/LICENSE-2.0.html

// @host localhost:8080
// @BasePath /api/v1

// @securityDefinitions.apikey BearerAuth
// @in header
// @name Authorization
// @description Type "Bearer" followed by a space and JWT token.
```

## Troubleshooting

### Swagger UI Not Loading

1. **Check if application is running:**
   ```bash
   curl http://localhost:8080/health
   ```

2. **Verify Swagger files exist:**
   ```bash
   ls -la docs/swagger/
   ```

3. **Regenerate documentation:**
   ```bash
   make swagger
   ```

### Endpoints Not Showing

1. **Ensure handler has Swagger annotations**
2. **Regenerate documentation:**
   ```bash
   make swagger
   ```
3. **Restart the application**

### Authentication Not Working

1. **Get a fresh token:**
   - Register and verify a new user
   - Or login with existing credentials

2. **Format token correctly:**
   - Must include "Bearer " prefix
   - Example: `Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...`

3. **Check token expiry:**
   - Access tokens expire after 7 days by default
   - Use refresh token to get a new access token

### Type Definitions Not Found

If you see errors like "cannot find type definition":

1. **Use fully qualified type names:**
   ```go
   // ❌ Wrong
   // @Param request body LoginRequest true "Login"
   
   // ✅ Correct
   // @Param request body entities.LoginRequest true "Login"
   ```

2. **Ensure types are exported (start with capital letter)**

3. **Regenerate with parse flags:**
   ```bash
   swag init -g cmd/main.go -o docs/swagger --parseDependency --parseInternal
   ```

## Best Practices

### 1. Keep Annotations Updated

Update Swagger annotations whenever you modify:
- Endpoint paths
- Request/response structures
- Authentication requirements
- Parameter types

### 2. Use Descriptive Summaries

```go
// ❌ Bad
// @Summary Get user

// ✅ Good
// @Summary Get authenticated user profile
```

### 3. Document All Parameters

```go
// @Param id path string true "User ID (UUID format)"
// @Param page query int false "Page number (default: 1)"
// @Param limit query int false "Items per page (default: 20, max: 100)"
```

### 4. Include Example Responses

```go
// @Success 200 {object} entities.User "User profile retrieved successfully"
// @Failure 404 {object} entities.ErrorResponse "User not found"
```

### 5. Group Related Endpoints

Use consistent tags:
```go
// @Tags auth        // All authentication endpoints
// @Tags wallets     // All wallet endpoints
// @Tags funding     // All funding endpoints
```

## Advanced Features

### Custom Response Examples

Add example values to your structs:

```go
type LoginRequest struct {
    Email    string `json:"email" example:"user@example.com"`
    Password string `json:"password" example:"SecurePass123!"`
}
```

### Enum Values

Document enum values:

```go
type Status string

const (
    StatusActive   Status = "active"   // @enum active
    StatusInactive Status = "inactive" // @enum inactive
)
```

### Deprecated Endpoints

Mark deprecated endpoints:

```go
// @Deprecated
// @Summary Old endpoint (deprecated)
// @Description Use /api/v1/new-endpoint instead
```

## Integration with CI/CD

### GitHub Actions Example

```yaml
name: Generate Swagger Docs

on:
  push:
    branches: [ main, develop ]

jobs:
  swagger:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v2
      
      - name: Set up Go
        uses: actions/setup-go@v2
        with:
          go-version: 1.21
      
      - name: Install swag
        run: go install github.com/swaggo/swag/cmd/swag@latest
      
      - name: Generate Swagger docs
        run: make swagger
      
      - name: Commit changes
        run: |
          git config --local user.email "action@github.com"
          git config --local user.name "GitHub Action"
          git add docs/swagger/
          git commit -m "Update Swagger documentation" || echo "No changes"
          git push
```

## Resources

- **Swag Documentation**: https://github.com/swaggo/swag
- **OpenAPI Specification**: https://swagger.io/specification/
- **Swagger UI**: https://swagger.io/tools/swagger-ui/
- **Go Swagger**: https://goswagger.io/

## Support

For issues with Swagger documentation:

1. Check this guide
2. Review Swag documentation
3. Check existing annotations in handlers
4. Open an issue on GitHub

## Quick Commands Reference

```bash
# Generate Swagger docs
make swagger

# Start application
make run

# View Swagger UI
open http://localhost:8080/swagger/index.html

# Clean generated files
make clean

# Run tests
make test

# Format code
make fmt
```
