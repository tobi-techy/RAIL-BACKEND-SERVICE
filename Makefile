.PHONY: build run test clean docker-build docker-run lint security-scan postman-collection sim sim-live sim-stub sim-soak miriam-eval classify seed-catalog

VERSION ?= $(shell git describe --tags --always --dirty)
COMMIT ?= $(shell git rev-parse --short HEAD)
BUILD_TIME ?= $(shell date -u '+%Y-%m-%d_%H:%M:%S')
LDFLAGS := -ldflags "-X github.com/rail-service/rail_service/pkg/version.Version=$(VERSION) \
	-X github.com/rail-service/rail_service/pkg/version.GitCommit=$(COMMIT) \
	-X github.com/rail-service/rail_service/pkg/version.BuildTime=$(BUILD_TIME) \
	-w -s"

build:
	@echo "Building rail-service..."
	CGO_ENABLED=0 go build $(LDFLAGS) -o bin/rail_service cmd/main.go

run:
	@echo "Running rail-service..."
	go run cmd/main.go

# Seed the investment asset catalog from the provider's published strategies.
# This is required before ANY strategy (retirement tiers included) can be
# created: the validator refuses allocation legs that are not in the catalog.
#   make seed-catalog              # preview only, writes nothing
#   make seed-catalog CONFIRM=1    # write
#   make seed-catalog COLLECTION=top_performing LIMIT=25 CONFIRM=1
# Needs INVESTMENT_GLIDER_ENABLED=true and INVESTMENT_GLIDER_API_KEY in the env.
seed-catalog:
	@echo "Ingesting provider asset catalog..."
	go run cmd/main.go seed-catalog -collection $(or $(COLLECTION),curated) -limit $(or $(LIMIT),50) $(if $(CONFIRM),-confirm,)

# Set the asset class of one catalog asset. Required between seed-catalog and
# the retirement tier bootstrap: discovered assets start as class "unknown",
# and tier files fail closed on any class that is not tier-eligible.
#   make classify ID=solana:...:<address> CLASS=treasury          # preview
#   make classify ID=solana:...:<address> CLASS=treasury APPLY=1  # write
classify:
	@echo "Classifying catalog asset..."
	go run cmd/main.go classify-asset -id $(ID) -class $(CLASS) $(if $(APPLY),-apply,)

test:
	@echo "Running tests..."
	go test -v -race -coverprofile=coverage.out ./...

# Operator Core golden eval (messaging/eval path). Requires a running API with
# EVAL_ENABLED=true, EVAL_TOKEN, and a funded USER_ID.
#   make miriam-eval EVAL_TOKEN=... USER_ID=...
miriam-eval:
	@echo "Running Miriam golden eval scenarios..."
	@./scripts/miriam_golden/run.sh

test-coverage:
	@echo "Generating coverage report..."
	go test -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html

lint:
	@echo "Running linters..."
	golangci-lint run ./...

# Type-check the integration-tagged tests. The `test` target above runs
# `go test ./...` with no build tag, so test/integration/ is never compiled --
# which is how it silently rotted into not building at all (stale calls to the
# Bridge adapter and to entities/). This does NOT run those tests; they need a
# live database and third-party sandbox credentials. It only proves they still
# compile, so rot is caught in CI instead of on the day someone needs them.
vet-integration:
	@echo "Type-checking integration-tagged tests..."
	go vet -tags=integration ./test/integration/...

sim:
	@echo "Running Miriam simulation impact grader..."
	@echo "Requires SIM_DATABASE_URL (a disposable Postgres) and AI provider keys."
	go run ./cmd/miriam-sim --scenarios test/simulation/scenarios --out ./sim-out $(SIM_ARGS)

sim-live:
	@echo "Running Miriam simulation with live conversation + shareable card..."
	@echo "Requires SIM_DATABASE_URL and AI keys. Writes sim-out/card.txt for sharing."
	go run ./cmd/miriam-sim --scenarios test/simulation/scenarios --out ./sim-out \
		--live --share ./sim-out/card.txt \
		--git-sha "$(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)" $(SIM_ARGS)

sim-stub:
	@echo "Running Miriam simulation harness self-test (offline stub, no API keys)..."
	go run ./cmd/miriam-sim --scenarios test/simulation/scenarios --stub-miriam $(SIM_ARGS)

sim-soak:
	@echo "Running Miriam continuous soak (generated scenarios, budget-governed)..."
	@echo "Requires SIM_DATABASE_URL and AI keys. Set a stop condition via SOAK_ARGS."
	@echo "Example: make sim-soak SOAK_ARGS='--soak-duration 120h --budget-usd 40 --soak-workers 3'"
	go run ./cmd/miriam-sim --soak --soak-out ./sim-out --git-sha "$(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)" $(SOAK_ARGS)

security-scan:
	@echo "Running security scans..."
	gosec -fmt=json -out=gosec-report.json ./...
	trivy fs --security-checks vuln,config .

postman-collection:
	@echo "Generating Postman collection from codebase..."
	python3 scripts/postman_generator/generate.py postman_collection_generated.json
	@echo "✅ Collection generated: postman_collection_generated.json"

docker-build:
	@echo "Building Docker image..."
	docker build -f Dockerfile.secure -t rail-service:$(VERSION) .

docker-run:
	@echo "Running Docker container..."
	docker run -p 8080:8080 rail-service:$(VERSION)

clean:
	@echo "Cleaning..."
	rm -rf bin/ coverage.out coverage.html gosec-report.json

deps:
	@echo "Downloading dependencies..."
	go mod download
	go mod verify

migrate-up:
	@echo "Running migrations..."
	go run cmd/main.go migrate

migrate-down:
	@echo "Rolling back migrations..."
	migrate -path migrations -database "$(DATABASE_URL)" down

dev:
	@echo "Starting development environment..."
	docker-compose up -d
	@sleep 5
	$(MAKE) migrate-up
	$(MAKE) run

stop:
	@echo "Stopping development environment..."
	docker-compose down
# Alpaca Integration Testing
.PHONY: test-alpaca setup-alpaca
test-alpaca:
	@echo "🧪 Running Alpaca Integration Tests..."
	go run scripts/test_alpaca.go
	go test -v ./test/integration/alpaca_integration_test.go

setup-alpaca:
	@echo "🚀 Setting up Alpaca testing environment..."
	./scripts/setup_alpaca_test.sh
