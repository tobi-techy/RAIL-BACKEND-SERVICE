# Sandbox Testing

## Setup

### Environment Variables

```bash
# .env file
BRIDGE_API_KEY=your_sandbox_api_key
BRIDGE_BASE_URL=https://api.sandbox.bridge.xyz
WEBHOOK_SECRET=your_webhook_secret
```

### Run Integration Tests

```bash
# All tests
go test -tags=integration ./test/integration/...

# Specific test
go test -tags=integration ./test/integration/ -run TestIntegration_AccountFlow

# Verbose
go test -tags=integration -v ./test/integration/...
```

## Resources

- See `test/sandbox/Bridge_Setup.md` for Bridge-specific sandbox configuration.
- See [Bridge API Documentation](https://docs.bridge.xyz) for full API reference.
