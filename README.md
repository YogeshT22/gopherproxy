<div align="center">

# GopherProxy & Sentinel

**A production-grade reverse proxy with dynamic service discovery, per-IP rate limiting, and full observability - built in Go.**

[![Go](https://img.shields.io/badge/Go-1.24.5-00ADD8?style=flat-square&logo=go&logoColor=white)](https://go.dev) [![Docker](https://img.shields.io/badge/Docker-Compose-2496ED?style=flat-square&logo=docker&logoColor=white)](https://docs.docker.com/compose/) [![Redis](https://img.shields.io/badge/Redis-7.4-DC382D?style=flat-square&logo=redis&logoColor=white)](https://redis.io) [![Prometheus](https://img.shields.io/badge/Prometheus-v3.2.1-E6522C?style=flat-square&logo=prometheus&logoColor=white)](https://prometheus.io) [![Grafana](https://img.shields.io/badge/Grafana-11.6.0-F46800?style=flat-square&logo=grafana&logoColor=white)](https://grafana.com) [![License](https://img.shields.io/badge/License-MIT-yellow?style=flat-square)](LICENSE)

</div>

![Logo](assets/logo.png)

---

## Overview

GopherProxy is a **reverse proxy** (Data Plane) that load-balances traffic across a dynamic pool of backends using round-robin. Its companion, **Sentinel** (Control Plane), continuously TCP-probes each backend and updates a **Redis Set** as the live source of truth. The proxy polls Redis every 5 seconds and updates its pool — no restarts required when backends come or go.

The entire stack ships as a single `docker compose up` with Prometheus scraping metrics every 15 s and a **pre-provisioned Grafana dashboard** that auto-loads on first boot.

---

## Architecture

```
                           ┌──────────────────────────────┐
  HTTP Clients ───────────▶│       GopherProxy :8080      │
                           │  per-IP rate limiter (burst=5)│
                           │  round-robin (skip dead)      │
                           └──────────────┬───────────────┘
                                          │ proxies to
                   ┌──────────────────────┼──────────────────────┐
                   ▼                      ▼                      ▼
             Backend :8081          Backend :8082          Backend :8083
                   ▲                      ▲                      ▲
                   └──────────────────────┴──────────────────────┘
                                          │ TCP probe every 2s
                           ┌──────────────┴───────────────┐
                           │         Sentinel             │
                           └──────────────┬───────────────┘
                                          │ SAdd / SRem
                           ┌──────────────▼───────────────┐
                           │     Redis  (gopher_backends) │
                           └──────────────┬───────────────┘
                                          │ SMembers every 5s
                           ┌──────────────▼───────────────┐
                           │       GopherProxy pool       │
                           └──────────────────────────────┘

  GopherProxy :2112/metrics ◀── Prometheus scrape (15s)
  Prometheus ◀──────────────── Grafana queries
```

---

## Components

| Service         | Role                                        | Ports (host)                     |
| --------------- | ------------------------------------------- | -------------------------------- |
| **GopherProxy** | Reverse proxy · rate limiting · metrics     | `8080` (proxy), `2112` (metrics) |
| **Sentinel**    | TCP health prober · Redis registry writer   | — (internal)                     |
| **Redis**       | Live backend set (`gopher_backends`)        | `16379` → `6379`                 |
| **Prometheus**  | Scrapes `/metrics` · stores 15 days of data | `9090`                           |
| **Grafana**     | Pre-provisioned dashboard · no manual setup | `3000`                           |

---

## Features

### Proxy (Data Plane)

- **Round-robin load balancing** with dead-backend skip — `sync/atomic` counter + `sync.RWMutex` pool
- **Per-IP rate limiting** (Token Bucket) — each client IP gets its own isolated `rate.Limiter` via `sync.Map`; burst of 5, replenishes at 2 req/s
- **Dynamic backend pool** — polls Redis `SMembers` every 5 s, registers new backends without restart
- **Local TCP health check** — probes every 10 s with `context`-aware `DialContext`; logs state transitions (recovered / went down)
- **Structured JSON logging** (`log/slog`) — every request logged with `method`, `path`, `remote_addr`, `status`, `duration_ms`
- **Graceful shutdown** — 30 s drain on `SIGTERM`/`SIGINT`, cancels root context, drains both servers, closes Redis client
- **Dedicated metrics server** — `/metrics` and `/healthz` on a separate port (`:2112`), never reachable through the proxy path

### Sentinel (Control Plane)

- **TCP probe loop** — checks each backend every 2 s with a 1 s dial timeout
- **Idempotent registry** — `SAdd` on UP, `SRem` on DOWN; only logs on actual state transitions
- **Runtime target override** — `SENTINEL_TARGETS` env var (comma-separated `host:port`) replaces hard-coded defaults without rebuilding

### Operations

- **4 Prometheus metrics** —

  - `gopherproxy_processed_requests_total`, `gopherproxy_dropped_requests_total`, `gopherproxy_active_backends`, `gopherproxy_request_duration_seconds` histogram (per-backend label)

- **7-panel Grafana dashboard** — auto-provisioned; request rate, p50/p95/p99 latency, active backends gauge + timeseries, totals, success rate %
- **Non-root containers** — fixed UID/GID `1001` (`gopheruser:gophergroup`); `HEALTHCHECK` on every service
- **Multi-stage Docker build** — `CGO_ENABLED=0`, `-trimpath`, `-s -w`; final image ≈ 13 MB
- **Version stamped at build time** — `Version` and `Commit` injected via `-ldflags`
- **Resource limits** on every service — CPU and memory capped in `docker-compose.yml`
- **Redis persistence** — `appendonly yes`, `appendfsync everysec`, `maxmemory 128 MB` (LRU eviction)

---

## Quick Start

### Prerequisites

| Tool                    | Version                 |
| ----------------------- | ----------------------- |
| Docker + Docker Compose | v2+                     |
| Python 3                | any (for mock backends) |
| Go                      | 1.24+ (tests only)      |
| `make`                  | optional                |

### 1 — Configure environment

```bash
cp .env.example .env
```

Edit `.env` — at minimum change `GRAFANA_PASSWORD`:

```dotenv
VERSION=1.0.0
GRAFANA_USER=admin
GRAFANA_PASSWORD=changeme          # ← change this
SENTINEL_TARGETS=host.docker.internal:8081,host.docker.internal:8082,host.docker.internal:8083
```

### 2 — Build & start the stack

```bash
make up
# expands to: VERSION=... COMMIT=$(git rev-parse --short HEAD) docker compose up --build -d
```

Wait ~15 s for all health checks to pass:

```
gopher-redis       (healthy) ✔
gopher-proxy       (healthy) ✔
gopher-sentinel    (healthy) ✔
gopher-prometheus  up        ✔
gopher-grafana     up        ✔
```

### 3 — Start mock backends _(separate terminal)_

```bash
make mock-backends
# Starts Python HTTP servers on :8081  :8082  :8083
```

Sentinel detects them within 2 s and registers them in Redis. The proxy picks them up within the next 5 s poll.

### 4 — Send traffic

```bash
# 30 requests, 1/s — stays under rate limit, round-robins across all three backends
for i in $(seq 1 30); do curl -s http://localhost:8080/; sleep 1; done
```

You will see each response coming from a different backend:

```
<h1>GopherProxy Demo: Response from SERVER 1</h1>
<h1>GopherProxy Demo: Response from SERVER 2</h1>
<h1>GopherProxy Demo: Response from SERVER 3</h1>
...
```

---

## Endpoints

| URL                             | Description                          |
| ------------------------------- | ------------------------------------ |
| `http://localhost:8080`         | Proxy — load-balanced entry point    |
| `http://localhost:2112/healthz` | Liveness probe — returns `ok`        |
| `http://localhost:2112/metrics` | Raw Prometheus metrics (text format) |
| `http://localhost:9090`         | Prometheus expression browser        |
| `http://localhost:9090/targets` | Scrape target health                 |
| `http://localhost:3000`         | Grafana — **GopherProxy Dashboard**  |

> **Grafana login:** `admin` / `admin` (or whatever is set in `.env`)

---

## Rate Limiting

The proxy uses a **per-IP Token Bucket** limiter:

| Parameter              | Value                                |
| ---------------------- | ------------------------------------ |
| Burst                  | 5 requests                           |
| Replenish rate         | 1 token per 500 ms (2 req/s)         |
| Response when exceeded | `429 Too Many Requests`              |
| Counter metric         | `gopherproxy_dropped_requests_total` |

Each client IP gets its own isolated bucket stored in a `sync.Map` — a busy IP cannot affect others.

---

## Prometheus Metrics

All metrics are prefixed `gopherproxy_` and scraped from `:2112/metrics`:

| Metric                                 | Type      | Description                                           |
| -------------------------------------- | --------- | ----------------------------------------------------- |
| `gopherproxy_processed_requests_total` | Counter   | Requests successfully forwarded to a backend          |
| `gopherproxy_dropped_requests_total`   | Counter   | Requests dropped (rate-limited or no healthy backend) |
| `gopherproxy_active_backends`          | Gauge     | Number of backends currently marked alive             |
| `gopherproxy_request_duration_seconds` | Histogram | Latency per backend — labelled `backend="host:port"`  |

### Useful PromQL queries

```promql
# Request throughput (req/s)
rate(gopherproxy_processed_requests_total[1m])

# p50 / p95 / p99 latency per backend
histogram_quantile(0.99, rate(gopherproxy_request_duration_seconds_bucket[1m]))

# Drop rate (rate-limited req/s)
rate(gopherproxy_dropped_requests_total[1m])

# Success rate %
rate(gopherproxy_processed_requests_total[1m])
  /
(rate(gopherproxy_processed_requests_total[1m]) + rate(gopherproxy_dropped_requests_total[1m]))
* 100

# How many backends are alive
gopherproxy_active_backends
```

---

## Grafana Dashboard

The dashboard (`grafana/provisioning/dashboards/gopherproxy.json`) is **auto-loaded on first boot** — no manual import needed.

| Panel                    | Query                                                              |
| ------------------------ | ------------------------------------------------------------------ |
| Request Rate             | `rate(gopherproxy_processed_requests_total[1m])` + dropped overlay |
| Latency Percentiles      | `histogram_quantile(0.50/0.95/0.99, ...)` per backend              |
| Active Backends (gauge)  | `gopherproxy_active_backends`                                      |
| Backend Health Over Time | Same gauge as timeseries                                           |
| Total Processed          | `gopherproxy_processed_requests_total`                             |
| Total Dropped            | `gopherproxy_dropped_requests_total`                               |
| Success Rate %           | processed / (processed + dropped) × 100                            |

Set the time range to **Last 5 minutes** and auto-refresh to **10 s** while traffic flows to see all panels update live.

---

## Environment Variables

### GopherProxy

| Variable       | Default           | Description                        |
| -------------- | ----------------- | ---------------------------------- |
| `REDIS_URL`    | `localhost:16379` | Redis address (`host:port`)        |
| `PROXY_PORT`   | `8080`            | Port the proxy listens on          |
| `METRICS_PORT` | `2112`            | Port for `/metrics` and `/healthz` |

### Sentinel

| Variable           | Default                           | Description                                 |
| ------------------ | --------------------------------- | ------------------------------------------- |
| `REDIS_URL`        | `localhost:16379`                 | Redis address (`host:port`)                 |
| `SENTINEL_TARGETS` | `host.docker.internal:808{1,2,3}` | Comma-separated `host:port` list to monitor |

---

## Development

### Run tests

```bash
make test
# go test -race -count=1 ./...
```

**22 unit tests** across both packages — all run with the race detector:

| Package       | Tests                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                          |
| ------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `gopherproxy` | `TestGetNextPeer_EmptyPool`, `TestGetNextPeer_AllDead`, `TestGetNextPeer_RoundRobin`, `TestGetNextPeer_SkipsDeadBackend`, `TestBackend_SetAndIsAlive`, `TestUpdateServerPool_AddNew`, `TestUpdateServerPool_NoDuplicates`, `TestUpdateServerPool_InvalidURL`, `TestIPRateLimiter_AllowsBurst`, `TestIPRateLimiter_BlocksAfterBurst`, `TestIPRateLimiter_IsolatesIPs`, `TestLoggingMiddleware_PassesThrough`, `TestLimitMiddleware_Allows`, `TestLimitMiddleware_Blocks`, `TestGetEnv_UsesDefault`, `TestGetEnv_UsesEnvVar`, `TestServerPool_AtomicConcurrency` |
| `sentinel`    | `TestGetEnv_Default`, `TestGetEnv_FromEnvironment`, `TestGetTargets_Defaults`, `TestGetTargets_FromEnv`, `TestGetTargets_SingleEntry`                                                                                                                                                                                                                                                                                                                                                                                                                          |

### Lint

```bash
make lint   # requires golangci-lint
```

### Other Makefile targets

```bash
make help           # list all targets
make build          # build images only (no start)
make logs           # tail all container logs
make down           # stop stack, keep volumes
make clean          # prune dangling images + build cache
make tidy           # go mod tidy && go mod verify
```

### Hot-reload Prometheus config

```bash
curl -s -X POST http://localhost:9090/-/reload
```

---

## Project Structure

```
.
├── main.go                        # GopherProxy — Data Plane
├── main_test.go                   # 17 proxy unit tests
├── sentinel/
│   ├── main.go                    # Sentinel — Control Plane
│   └── main_test.go               # 5 sentinel unit tests
├── Dockerfile                     # Multi-stage proxy image
├── Dockerfile.sentinel            # Multi-stage sentinel image
├── docker-compose.yml             # Full stack (5 services)
├── prometheus.yml                 # Scrape config + relabelling
├── Makefile                       # Developer targets
├── .env.example                   # Environment variable template
├── .dockerignore                  # Keeps build context lean
├── go.mod / go.sum                # Module: github.com/YogeshT22/gopherproxy.git
├── grafana/
│   └── provisioning/
│       ├── datasources/
│       │   └── datasource.yml     # Prometheus datasource (uid: prometheus)
│       └── dashboards/
│           ├── dashboard.yml      # Dashboard provider config
│           └── gopherproxy.json   # 7-panel pre-built dashboard
└── mock_backends/
    ├── server1/index.html         # Mock on :8081
    ├── server2/index.html         # Mock on :8082
    └── server3/index.html         # Mock on :8083
```

---

## Dependencies

| Package                               | Version | Purpose                   |
| ------------------------------------- | ------- | ------------------------- |
| `github.com/prometheus/client_golang` | v1.23.2 | Metrics instrumentation   |
| `github.com/redis/go-redis/v9`        | v9.17.2 | Redis client              |
| `golang.org/x/time`                   | v0.14.0 | Token bucket rate limiter |

---

## Production Notes

- **Redis port 16379** is exposed to the host for local debugging only — remove the `ports:` mapping before any real deployment
- **Grafana sign-up** is disabled (`GF_USERS_ALLOW_SIGN_UP=false`) and analytics reporting is off
- **Prometheus retains 15 days** of data in the `prometheus_data` named volume
- **All containers run as non-root** (UID/GID `1001`) and have CPU + memory limits set
- **Sentinel `kill -0 1` health check** — more reliable than `pgrep` on BusyBox/Alpine, where the full path (`/app/sentinel`) does not match an exact-name `pgrep -x` search

---

## Debugging Log

Real issues hit during development, recorded here as reference:

| Symptom                            | Root Cause                                                                                    | Fix                                                                       |
| ---------------------------------- | --------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------- |
| Metrics showed 0 traffic           | `pool` variable shadowed inside handler — handler read an empty instance                      | Removed `:=` re-declaration; handler closes over the outer `pool` pointer |
| `bind: forbidden` on port `6379`   | Hyper-V reserved the port range on Windows                                                    | Mapped Redis to high port `16379` on the host                             |
| Proxy crashed on empty registry    | Modulo by zero when `len(backends) == 0`                                                      | Defensive `if l == 0 { return nil }` before the `% l` operation           |
| `gopher-sentinel` always unhealthy | `pgrep -x sentinel` on BusyBox matches full path, not basename                                | Replaced with `kill -0 1`                                                 |
| Grafana panels showed "No data"    | Datasource UID auto-generated by Grafana didn't match `"uid": "prometheus"` in dashboard JSON | Added `uid: prometheus` to `datasource.yml`                               |

---

## License

MIT — see [LICENSE](LICENSE)

**Author:** Yogesh T · [GitHub @YogeshT22](https://github.com/YogeshT22)
