.PHONY: test test-unit test-integration test-all

# Run unit tests
test-unit:
	go test -v ./test/unit/...

# Run integration tests
test-integration:
	go test -tags=integration -v ./test/integration/...

# Run all tests
test-all: test-unit test-integration

# Run tests with coverage
test-coverage:
	go test -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out

# Run specific test
test-run:
	go test -v -run $(TEST) ./test/...

# Lint code
lint:
	golangci-lint run

# Format code
fmt:
	go fmt ./...

# Build
build:
	go build -o bin/stack_service cmd/main.go

# Run locally
run:
	go run cmd/main.go

# Clean
clean:
	rm -rf bin/ coverage.out
