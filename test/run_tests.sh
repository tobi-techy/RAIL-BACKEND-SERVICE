#!/bin/bash

# Test Runner Script for STACK Service
# Executes comprehensive test suite with proper setup and reporting

set -e  # Exit on any command failure

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Configuration
PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TEST_OUTPUT_DIR="$PROJECT_ROOT/test/reports"
COVERAGE_FILE="$TEST_OUTPUT_DIR/coverage.out"
COVERAGE_HTML="$TEST_OUTPUT_DIR/coverage.html"

# Default test configuration
RUN_UNIT_TESTS=true
RUN_INTEGRATION_TESTS=true
RUN_COVERAGE=true
VERBOSE=false
PARALLEL=true
RACE_DETECTION=true

# Parse command line arguments
while [[ $# -gt 0 ]]; do
  case $1 in
    --unit-only)
      RUN_INTEGRATION_TESTS=false
      shift
      ;;
    --integration-only)
      RUN_UNIT_TESTS=false
      shift
      ;;
    --no-coverage)
      RUN_COVERAGE=false
      shift
      ;;
    --verbose|-v)
      VERBOSE=true
      shift
      ;;
    --no-parallel)
      PARALLEL=false
      shift
      ;;
    --no-race)
      RACE_DETECTION=false
      shift
      ;;
    --help|-h)
      echo "Usage: $0 [options]"
      echo "Options:"
      echo "  --unit-only          Run only unit tests"
      echo "  --integration-only   Run only integration tests"
      echo "  --no-coverage        Skip coverage reporting"
      echo "  --verbose, -v        Enable verbose output"
      echo "  --no-parallel        Disable parallel test execution"
      echo "  --no-race            Disable race condition detection"
      echo "  --help, -h           Show this help message"
      exit 0
      ;;
    *)
      echo "Unknown option $1"
      exit 1
      ;;
  esac
done

# Helper functions
print_header() {
    echo -e "\n${BLUE}================================================================${NC}"
    echo -e "${BLUE}  $1${NC}"
    echo -e "${BLUE}================================================================${NC}\n"
}

print_success() {
    echo -e "${GREEN}✅ $1${NC}"
}

print_error() {
    echo -e "${RED}❌ $1${NC}"
}

print_warning() {
    echo -e "${YELLOW}⚠️  $1${NC}"
}

print_info() {
    echo -e "${BLUE}ℹ️  $1${NC}"
}

# Setup test environment
setup_test_environment() {
    print_header "Setting up test environment"
    
    # Create test reports directory
    mkdir -p "$TEST_OUTPUT_DIR"
    
    # Set test environment variables
    export TEST_DB_URL="${TEST_DB_URL:-postgres://test_user:test_pass@localhost:5432/stack_test}"
    export TEST_WEBHOOK_SECRET="${TEST_WEBHOOK_SECRET:-test-webhook-secret-for-testing-only}"
    export TEST_JWT_SECRET="${TEST_JWT_SECRET:-test-jwt-secret-for-testing-only}"
    export LOG_LEVEL="${LOG_LEVEL:-warn}"  # Reduce noise in tests
    export GIN_MODE="test"
    
    print_info "Project root: $PROJECT_ROOT"
    print_info "Test reports: $TEST_OUTPUT_DIR"
    print_info "Database URL: $TEST_DB_URL"
    print_success "Test environment configured"
}

# Check dependencies
check_dependencies() {
    print_header "Checking dependencies"
    
    # Check Go version
    if ! command -v go &> /dev/null; then
        print_error "Go is not installed"
        exit 1
    fi
    
    GO_VERSION=$(go version | grep -o 'go[0-9]\+\.[0-9]\+')
    print_info "Go version: $GO_VERSION"
    
    # Download dependencies
    print_info "Downloading Go modules..."
    cd "$PROJECT_ROOT"
    go mod download
    go mod tidy
    
    print_success "Dependencies checked"
}

# Build test flags
build_test_flags() {
    local flags=()
    
    if [[ "$VERBOSE" == true ]]; then
        flags+=("-v")
    fi
    
    if [[ "$PARALLEL" == true ]]; then
        flags+=("-parallel" "4")
    fi
    
    if [[ "$RACE_DETECTION" == true ]]; then
        flags+=("-race")
    fi
    
    if [[ "$RUN_COVERAGE" == true ]]; then
        flags+=("-cover" "-coverprofile=$COVERAGE_FILE")
    fi
    
    flags+=("-timeout" "30m")  # Set reasonable timeout
    
    echo "${flags[@]}"
}

# Run unit tests
run_unit_tests() {
    print_header "Running Unit Tests"
    
    local test_flags
    test_flags=($(build_test_flags))
    
    local unit_test_paths=(
        "./internal/domain/services/funding/..."
        "./internal/domain/entities/..."
        "./internal/infrastructure/adapters/circle/..."
        "./pkg/retry/..."
        "./pkg/webhook/..."
    )
    
    print_info "Test flags: ${test_flags[*]}"
    print_info "Test paths: ${unit_test_paths[*]}"
    
    if go test "${test_flags[@]}" "${unit_test_paths[@]}"; then
        print_success "Unit tests passed"
        return 0
    else
        print_error "Unit tests failed"
        return 1
    fi
}

# Run integration tests
run_integration_tests() {
    print_header "Running Integration Tests"
    
    local test_flags
    test_flags=($(build_test_flags))
    
    # Remove coverage from integration tests to avoid conflicts
    local integration_flags=()
    for flag in "${test_flags[@]}"; do
        if [[ "$flag" != "-cover" && "$flag" != "-coverprofile=$COVERAGE_FILE" ]]; then
            integration_flags+=("$flag")
        fi
    done
    
    print_info "Integration test flags: ${integration_flags[*]}"
    
    if go test "${integration_flags[@]}" "./test/integration/..."; then
        print_success "Integration tests passed"
        return 0
    else
        print_error "Integration tests failed"
        return 1
    fi
}

# Generate coverage report
generate_coverage_report() {
    if [[ "$RUN_COVERAGE" != true || ! -f "$COVERAGE_FILE" ]]; then
        print_warning "Coverage report skipped or coverage file not found"
        return 0
    fi
    
    print_header "Generating Coverage Report"
    
    # Generate HTML coverage report
    go tool cover -html="$COVERAGE_FILE" -o "$COVERAGE_HTML"
    
    # Print coverage summary
    local coverage_percent
    coverage_percent=$(go tool cover -func="$COVERAGE_FILE" | grep total | grep -o '[0-9.]\+%')
    
    print_info "Coverage report generated: $COVERAGE_HTML"
    print_info "Total coverage: $coverage_percent"
    
    # Coverage thresholds
    local coverage_num
    coverage_num=$(echo "$coverage_percent" | sed 's/%//')
    
    if (( $(echo "$coverage_num >= 80.0" | bc -l) )); then
        print_success "Coverage meets target (≥80%): $coverage_percent"
    elif (( $(echo "$coverage_num >= 70.0" | bc -l) )); then
        print_warning "Coverage below target but acceptable (≥70%): $coverage_percent"
    else
        print_error "Coverage below minimum threshold (<70%): $coverage_percent"
        return 1
    fi
    
    return 0
}

# Run linting and static analysis
run_linting() {
    print_header "Running Static Analysis"
    
    # Run go fmt check
    print_info "Checking code formatting..."
    if ! gofmt_output=$(gofmt -l . 2>&1); then
        print_error "gofmt check failed"
        return 1
    fi
    
    if [[ -n "$gofmt_output" ]]; then
        print_error "Code is not properly formatted. Run 'go fmt ./...' to fix:"
        echo "$gofmt_output"
        return 1
    fi
    
    print_success "Code formatting check passed"
    
    # Run go vet
    print_info "Running go vet..."
    if go vet ./...; then
        print_success "go vet passed"
    else
        print_error "go vet failed"
        return 1
    fi
    
    # Run golangci-lint if available
    if command -v golangci-lint &> /dev/null; then
        print_info "Running golangci-lint..."
        if golangci-lint run; then
            print_success "golangci-lint passed"
        else
            print_error "golangci-lint failed"
            return 1
        fi
    else
        print_warning "golangci-lint not found, skipping"
    fi
    
    return 0
}

# Main execution
main() {
    local start_time
    start_time=$(date +%s)
    
    print_header "STACK Service Test Suite Runner"
    
    # Setup
    setup_test_environment
    check_dependencies
    
    # Run static analysis
    if ! run_linting; then
        print_error "Static analysis failed"
        exit 1
    fi
    
    # Run tests
    local test_results=()
    
    if [[ "$RUN_UNIT_TESTS" == true ]]; then
        if run_unit_tests; then
            test_results+=("Unit tests: PASSED")
        else
            test_results+=("Unit tests: FAILED")
            exit 1
        fi
    fi
    
    if [[ "$RUN_INTEGRATION_TESTS" == true ]]; then
        if run_integration_tests; then
            test_results+=("Integration tests: PASSED")
        else
            test_results+=("Integration tests: FAILED")
            exit 1
        fi
    fi
    
    # Generate reports
    if ! generate_coverage_report; then
        print_error "Coverage report generation failed"
        exit 1
    fi
    
    # Summary
    local end_time
    end_time=$(date +%s)
    local duration=$((end_time - start_time))
    
    print_header "Test Summary"
    
    for result in "${test_results[@]}"; do
        if [[ "$result" == *"PASSED"* ]]; then
            print_success "$result"
        else
            print_error "$result"
        fi
    done
    
    print_info "Total execution time: ${duration}s"
    print_success "All tests completed successfully!"
    
    # Open coverage report if requested
    if [[ "$RUN_COVERAGE" == true && -f "$COVERAGE_HTML" ]]; then
        print_info "Coverage report available at: $COVERAGE_HTML"
        if command -v open &> /dev/null; then
            print_info "Opening coverage report in browser..."
            open "$COVERAGE_HTML"
        fi
    fi
}

# Cleanup function
cleanup() {
    print_info "Cleaning up test environment..."
    # Add any necessary cleanup here
}

# Set up cleanup trap
trap cleanup EXIT

# Run main function
main "$@"