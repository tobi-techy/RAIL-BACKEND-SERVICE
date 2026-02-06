# Performance Testing Suite

This directory contains performance testing tools for the RAIL Backend service.

## Overview

The performance testing suite includes:

1. **k6 Load Tests** - API endpoint load testing
2. **Database Benchmarks** - PostgreSQL performance testing
3. **CI/CD Integration** - Automated performance testing in CI/CD

## Prerequisites

- [k6](https://k6.io/docs/get-started/installation/) for API load testing
- PostgreSQL client tools (`psql`, `pgbench`) for database benchmarking
- Go 1.24+ for building the application

## Running k6 Tests

### Prerequisites

Install k6:
```bash
# macOS
brew install k6

# Linux
curl https://github.com/grafana/k6/releases/download/v0.54.0/k6-v0.54.0-linux-amd64.tar.gz -o k6.tar.gz
tar -xzf k6.tar.gz
cp k6-v0.54.0-linux-amd64/k6 /usr/local/bin/
```

### Configuration

Set environment variables:
```bash
export BASE_URL=http://localhost:8080  # API endpoint
export API_KEY=your-api-key             # Optional API key for authenticated tests
```

### Running Scenarios

**Smoke Test** (quick sanity check):
```bash
k6 run --scenario smoke_test test/performance/k6/scenarios.js
```

**Load Test** (normal load simulation):
```bash
k6 run --scenario load_test test/performance/k6/scenarios.js
```

**Stress Test** (beyond normal capacity):
```bash
k6 run --scenario stress_test test/performance/k6/scenarios.js
```

**Soak Test** (long-running stability test):
```bash
k6 run --scenario soak_test test/performance/k6/scenarios.js
```

**All Scenarios**:
```bash
k6 run test/performance/k6/scenarios.js
```

**Authentication Scenarios**:
```bash
k6 run test/performance/k6/auth_scenarios.js
```

### Output

Results are saved to:
- `stdout` - Real-time console output
- `test/performance/reports/k6-report-{timestamp}.json` - Detailed JSON report

## Database Benchmarks

### Prerequisites

Ensure PostgreSQL client tools are installed:
```bash
# macOS
brew install postgresql

# Ubuntu/Debian
sudo apt-get install postgresql-client
```

### Configuration

Set environment variables:
```bash
export DB_HOST=localhost
export DB_PORT=5432
export DB_NAME=rail_service
export DB_USER=postgres
export DB_PASSWORD=your-password
export ITERATIONS=1000
```

### Running Benchmarks

```bash
chmod +x test/performance/db_benchmark.sh
./test/performance/db_benchmark.sh
```

### Benchmark Types

1. **pgbench TPC-B** - Standard TPC-B like transaction benchmark
2. **pgbench Read-Only** - Read-heavy workload simulation
3. **Custom SQL Benchmarks**:
   - Simple SELECT
   - User Lookup by Email
   - Balance Query
   - Wallet Query
   - Transaction History
   - Join Queries
   - Aggregate Queries

### Output

Results are saved to `test/performance/reports/`:
- `pgbench_tpcb.log` - TPC-B benchmark results
- `pgbench_readonly.log` - Read-only benchmark results
- `db_benchmark_*.json` - Individual benchmark results
- `benchmark_summary.json` - Overall summary

## CI/CD Integration

The performance tests are integrated into GitHub Actions via `.github/workflows/performance-tests.yml`.

### Triggers

- **Scheduled**: Weekly on Sunday at midnight
- **Manual**: Trigger via GitHub Actions UI
- **On PR**: Comments on PR with results

### Environments

- **Staging**: Tests against staging environment
- **Production**: Tests against production environment (restricted)

### Configuration

Required secrets:
- `BASE_URL` - API endpoint URL
- `K6_CLOUD_TOKEN` - Optional k6 Cloud API token for cloud results

## Performance Thresholds

The test suite enforces the following thresholds:

### HTTP Request Duration
- 95th percentile < 500ms
- 99th percentile < 1000ms

### Error Rate
- HTTP failures < 1%

### Endpoint-Specific
| Endpoint | 95th Percentile |
|----------|----------------|
| Login | < 1000ms |
| Register | < 2000ms |
| Deposit | < 1500ms |
| Portfolio | < 500ms |

## Best Practices

1. **Warm-up**: Always run a smoke test before load tests
2. **Isolation**: Run performance tests in isolated environments
3. **Baseline**: Establish baseline metrics before making changes
4. **Monitoring**: Monitor system resources during tests
5. **Cleanup**: Clean up test data after tests

## Troubleshooting

### k6 Tests Fail Immediately
- Check if the API is running
- Verify `BASE_URL` environment variable
- Check for port conflicts

### Database Benchmarks Fail
- Verify PostgreSQL is running
- Check connection credentials
- Ensure database exists

### High Error Rates
- Check rate limiting configuration
- Verify Redis is available
- Monitor database connection pool

## Additional Resources

- [k6 Documentation](https://k6.io/docs/)
- [pgbench Documentation](https://www.postgresql.org/docs/current/pgbench.html)
- [Prometheus Metrics](https://prometheus.io/)
