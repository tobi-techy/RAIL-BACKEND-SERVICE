#!/bin/bash

# Alpaca Integration Testing Setup Script

echo "🚀 Setting up Alpaca Integration Testing Environment"

# Check if .env file exists
if [ ! -f .env ]; then
    echo "❌ .env file not found. Creating from template..."
    cp .env.example .env
fi

# Add Alpaca test credentials to .env if not present
if ! grep -q "ALPACA_CLIENT_ID" .env; then
    echo "" >> .env
    echo "# Alpaca Sandbox Credentials" >> .env
    echo "ALPACA_CLIENT_ID=YOUR_SANDBOX_CLIENT_ID" >> .env
    echo "ALPACA_SECRET_KEY=YOUR_SANDBOX_SECRET_KEY" >> .env
    echo "ALPACA_ENVIRONMENT=sandbox" >> .env
    echo "✅ Added Alpaca environment variables to .env"
    echo "⚠️  Please update with your actual sandbox credentials"
fi

# Create test data directory
mkdir -p test/data/alpaca

# Run basic connectivity test
echo "🔍 Testing basic connectivity..."

# Check if Go modules are up to date
echo "📦 Updating Go modules..."
go mod tidy

# Run the manual test script
echo "🧪 Running Alpaca integration test..."
go run scripts/test_alpaca.go

echo "✅ Setup complete!"
echo ""
echo "Next steps:"
echo "1. Sign up at https://broker-app.alpaca.markets/"
echo "2. Get your sandbox API keys"
echo "3. Update .env with your credentials"
echo "4. Run: go test ./test/integration/alpaca_integration_test.go -v"
echo "5. Monitor your test activities in the Broker Dashboard"
