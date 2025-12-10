# Postman Collection Generator

Automatically generate comprehensive Postman collections from the RAIL API codebase.

## Features

- **Auto-discovery**: Automatically extracts all endpoints from Go route files
- **Organized**: Groups endpoints by category (Auth, Wallets, Investment, etc.)
- **Documented**: Includes descriptions, example requests, and responses
- **Maintainable**: Easy to update when new endpoints are added

## Usage

### Generate Collection

```bash
cd /Users/tobi/Development/RAIL_BACKEND
python3 scripts/postman_generator/generate.py
```

This will create `postman_collection_generated.json` in the project root.

### Custom Output Path

```bash
python3 scripts/postman_generator/generate.py path/to/output.json
```

### Import to Postman

1. Open Postman
2. Click "Import" button
3. Select the generated JSON file
4. Collection will be imported with all endpoints organized by category

## Adding New Endpoint Metadata

When you add a new endpoint, update `endpoint_metadata.json` with:

```json
{
  "HandlerName": {
    "description": "What this endpoint does",
    "body": {
      "field": "example value"
    },
    "response": {
      "result": "example response"
    }
  }
}
```

Then regenerate the collection:

```bash
python3 scripts/postman_generator/generate.py
```

## File Structure

```
postman_generator/
├── README.md                    # This file
├── generate.py                  # Main script
├── endpoint_extractor.py        # Extracts endpoints from Go files
├── collection_builder.py        # Builds Postman collection JSON
└── endpoint_metadata.json       # Endpoint descriptions and examples
```

## How It Works

1. **Extract**: Scans `internal/api/routes/*.go` files for endpoint definitions
2. **Group**: Organizes endpoints by category (Auth, Wallets, etc.)
3. **Enrich**: Adds descriptions, example bodies, and responses from metadata
4. **Build**: Generates Postman Collection v2.1.0 JSON format
5. **Save**: Writes to output file

## Endpoint Categories

- 🏥 Health & Monitoring
- 🔐 Authentication
- 🚀 Onboarding
- 👤 Users
- 🔒 Security
- 💰 Wallets
- 💸 Funding
- 📈 Investment
- 📊 Portfolio
- 📉 Analytics
- 💹 Market
- 🔄 Scheduled Investments
- ⚖️ Rebalancing
- 👥 Copy Trading
- 🪙 Roundups
- 🤖 AI Chat
- 📰 News
- 🔔 Webhooks
- ⚙️ Admin

## Automation

### Git Hook (Pre-commit)

Add to `.git/hooks/pre-commit`:

```bash
#!/bin/bash
python3 scripts/postman_generator/generate.py
git add postman_collection_generated.json
```

### CI/CD Integration

Add to your CI pipeline:

```yaml
- name: Generate Postman Collection
  run: python3 scripts/postman_generator/generate.py
  
- name: Upload Collection
  uses: actions/upload-artifact@v2
  with:
    name: postman-collection
    path: postman_collection_generated.json
```

## Troubleshooting

### No endpoints found

- Check that `internal/api/routes/` directory exists
- Verify Go route files use standard patterns: `router.GET("/path", handler)`

### Missing metadata

- Add endpoint details to `endpoint_metadata.json`
- Handler name must match the function name in Go code

### Import errors in Postman

- Validate JSON: `python3 -m json.tool postman_collection_generated.json`
- Check Postman Collection v2.1.0 schema compliance

## Contributing

To add support for new endpoint patterns:

1. Update `endpoint_extractor.py` regex patterns
2. Add grouping logic in `get_grouped_endpoints()`
3. Add metadata to `endpoint_metadata.json`
4. Regenerate collection

## License

MIT
