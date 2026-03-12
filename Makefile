# ─────────────────────────────────────────────────────────────────────────────
# GopherProxy — Makefile
# Usage: make <target>
# ─────────────────────────────────────────────────────────────────────────────

# Load .env if it exists (won't fail if missing)
-include .env
export

VERSION  ?= dev
COMMIT   ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")

.PHONY: help up down build logs lint test tidy clean mock-backends

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
