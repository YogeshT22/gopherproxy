# ─────────────────────────────────────────────────────────────────────────────
# GopherProxy — Makefile
# Usage: make <target>
# ─────────────────────────────────────────────────────────────────────────────

# Load .env if it exists (won't fail if missing)
-include .env
export

VERSION  ?= dev
COMMIT   ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")

.PHONY: help up down build logs lint test tidy clean mock-backends k6-smoke k6-load k6-spike k6-sustained

help: ## Show this help message
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}'

# ── Docker Compose ────────────────────────────────────────────────────────────
up: ## Build images and start the full stack
	VERSION=$(VERSION) COMMIT=$(COMMIT) docker compose up --build -d

down: ## Stop and remove all containers (keeps volumes)
	docker compose down

build: ## Build images only (no start)
	VERSION=$(VERSION) COMMIT=$(COMMIT) docker compose build

logs: ## Tail logs from all containers
	docker compose logs -f --tail=100

# ── Go ────────────────────────────────────────────────────────────────────────
lint: ## Run golangci-lint (requires golangci-lint installed)
	golangci-lint run ./...

test: ## Run all tests with race detector
	go test -race -count=1 ./...

tidy: ## Tidy and verify go modules
	go mod tidy && go mod verify

# ── Dev helpers ───────────────────────────────────────────────────────────────
mock-backends: ## Start all three mock Python backend servers (ports 8081-8083)
	@echo "Starting mock backends on :8081, :8082, :8083 ..."
	@cd mock_backends/server1 && python3 -m http.server 8081 --bind 0.0.0.0 &
	@cd mock_backends/server2 && python3 -m http.server 8082 --bind 0.0.0.0 &
	@cd mock_backends/server3 && python3 -m http.server 8083 --bind 0.0.0.0 &
	@echo "All mock backends started. Press Ctrl+C to stop."

clean: ## Remove dangling Docker images and build cache
	docker image prune -f
	docker builder prune -f

# ── k6 Load Testing (backwards-compatible) ────────────────────────────────────
# These targets try the modern `--scenario` flag first and fall back to
# legacy `--vus/--duration` when `k6` reports the flag as unknown.

k6-smoke: ## Quick k6 load test (10 users, 30s) — smoke test
	@echo "Starting k6 smoke test..."
	@sh -c 'if k6 --help 2>&1 | grep -q -- "--scenario"; then k6 run k6-load-test.js --scenario smoke --summary-export=k6-summary-smoke.json; else k6 run --vus 10 --duration 30s --summary-export=k6-summary-smoke.json k6-load-test.js; fi'

k6-load: ## Full k6 load test (ramp to 500 users over ~6min) — realistic load
	@echo "Starting k6 full load test (this may take ~6 minutes)..."
	@sh -c 'if k6 --help 2>&1 | grep -q -- "--scenario"; then k6 run k6-load-test.js --scenario load --summary-export=k6-summary-load.json; else k6 run --vus 500 --duration 6m --summary-export=k6-summary-load.json k6-load-test.js; fi'

k6-spike: ## k6 spike test (sudden jump to 500 users) — stress test
	@echo "Starting k6 spike test..."
	@sh -c 'if k6 --help 2>&1 | grep -q -- "--scenario"; then k6 run k6-load-test.js --scenario spike --summary-export=k6-summary-spike.json; else k6 run --vus 500 --duration 3m --summary-export=k6-summary-spike.json k6-load-test.js; fi'

k6-sustained: ## k6 sustained test (200 concurrent users for 5min)
	@echo "Starting k6 sustained load test..."
	@sh -c 'if k6 --help 2>&1 | grep -q -- "--scenario"; then k6 run k6-load-test.js --scenario sustained --summary-export=k6-summary-sustained.json; else k6 run --vus 200 --duration 5m --summary-export=k6-summary-sustained.json k6-load-test.js; fi'

# RPS-targeted scenarios (arrival-rate)
k6-rps200: ## k6 arrival-rate test targeting 200 req/s for 2 minutes
	@echo "Starting k6 arrival-rate test: 200 RPS for 2 minutes..."
	@sh -c 'if k6 --help 2>&1 | grep -q -- "--scenario"; then k6 run k6-load-test.js --scenario rps200 --summary-export=k6-summary-rps200.json; else k6 run --vus 200 --duration 2m --summary-export=k6-summary-rps200.json k6-load-test.js; fi'

k6-rps4200: ## k6 arrival-rate test targeting 4200 req/s for 2 minutes (distributed)
	@echo "Starting k6 arrival-rate test: 4200 RPS for 2 minutes (requires distributed clients)..."
	@sh -c 'if k6 --help 2>&1 | grep -q -- "--scenario"; then k6 run k6-load-test.js --scenario rps4200 --summary-export=k6-summary-rps4200.json; else k6 run --vus 2000 --duration 2m --summary-export=k6-summary-rps4200.json k6-load-test.js; fi'

k6-results: ## Display latest k6 test results (JSON summary)
	@if [ -f k6-summary-load.json ]; then \
		echo "Latest k6 results (k6-summary-load.json):"; \
		cat k6-summary-load.json | jq '.metrics | keys' 2>/dev/null || echo "Install jq for JSON parsing"; \
	elif [ -f summary.json ]; then \
		echo "Latest k6 results (summary.json):"; \
		cat summary.json | jq '.metrics | keys' 2>/dev/null || echo "Install jq for JSON parsing"; \
	else \
		echo "No k6 results found. Run 'make k6-load' or 'make k6-smoke' first."; \
	fi
