# GopherProxy

**Layer 7 Reverse Proxy with Load Balancing and Redis-backed Service Discovery**

> A production-pattern reverse proxy written in Go, containerised with Docker, and observable via Prometheus + Grafana. Built to demonstrate distributed systems fundamentals: service discovery, health-checking, rate limiting, and graceful shutdown.

---

# Why this project exist?

> Built to understand how reverse proxies coordinate service discovery, health checking, observability, and request routing using Go without relying on NGINX or Traefik.

---

## What It Does

GopherProxy sits in front of multiple HTTP backends and:

1. **Discovers backends dynamically** — reads a Redis `SET` (`gopher_backends`) every 5 seconds; no restart required to add or remove a backend.
2. **Routes traffic** — round-robin across all `Alive` backends using a lock-free atomic counter.
3. **Health-checks independently** — a companion binary, **Sentinel**, probes each backend via TCP every 2 seconds and writes `SADD`/`SREM` to Redis.
4. **Rate-limits per client IP** — token-bucket limiter (2 req/s sustained, burst 5) via `golang.org/x/time/rate`.
5. **Exposes metrics** — five Prometheus metric series on `:2112/metrics`, scraped by Prometheus and visualised in Grafana.
6. **Shuts down cleanly** — 30-second drain window on SIGTERM/SIGINT.

---

## Features
- Round-robin load balancing
- Redis-backed service discovery
- Sentinel health checking
- Per-IP token bucket rate limiting
- Prometheus + Grafana observability
- Graceful shutdown
- Docker Compose deployment

---

## Project Highlights

- Built in Go using net/http and httputil.ReverseProxy
- Dynamic service discovery via Redis
- Separate control plane (Sentinel) and data plane (Proxy)
- Per-IP token bucket rate limiting
- Prometheus metrics with Grafana dashboards
- Load tested with k6
- 18 unit tests with race detector

---

## Architecture Overview

```
┌─────────────────────────────────────────────────────────┐
│                     Docker Network (gopher-net)         │
│                                                         │
│  k6 / curl ──▶  GopherProxy :8080                      │
│                  │  ├── Round-robin routing             │
│                  │  ├── Per-IP rate limiter             │
│                  │  └── Prometheus metrics :2112        │
│                  │                                      │
│                  ├──▶ Backend :8081 (mock)             │
│                  ├──▶ Backend :8082 (mock)             │
│                  └──▶ Backend :8083 (mock)             │
│                                                         │
│  Sentinel ──▶ TCP probe each backend every 2s          │
│           └──▶ Redis SET (gopher_backends)             │
│                  ▲                                      │
│  GopherProxy ────┘ (SMEMBERS every 5s)                 │
│                                                         │
│  Prometheus :9090 ──▶ scrapes :2112 every 15s          │
│  Grafana    :3000 ──▶ queries Prometheus               │
└─────────────────────────────────────────────────────────┘
```

---

## Repository Structure

```
.
├── main.go                  # GopherProxy — data plane (proxy, rate limiter, metrics)
├── main_test.go             # Unit tests (18 tests, race detector enabled)
├── sentinel/
│   ├── main.go              # Sentinel — control plane (TCP health checks → Redis)
│   └── main_test.go
├── mock_backends/
│   ├── server1/             # Python http.server on :8081
│   ├── server2/             # Python http.server on :8082
│   └── server3/             # Python http.server on :8083
├── Dockerfile               # Multi-stage build (proxy + sentinel via APP_NAME arg)
├── Dockerfile.sentinel      # Sentinel-specific build
├── docker-compose.yml       # Full stack: proxy, sentinel, redis, prometheus, grafana
├── prometheus.yml           # Scrape config (15s interval, gopherproxy_.* filter)
├── grafana/provisioning/    # Grafana datasource + dashboard provisioning
├── k6-load-test.js          # k6 scenarios: smoke, load, spike, sustained, rps200, rps4200
├── Makefile                 # Dev helpers: up, down, test, lint, k6-*
├── go.mod                   # Go 1.24.5; deps: prometheus/client_golang, redis/go-redis/v9, x/time
```

---

## Quick Start

### Prerequisites
- Docker + Docker Compose
- Go 1.24.5+ (for local dev)
- k6 (for load tests)

### Run the Full Stack

```bash
# Clone and enter the repo
git clone https://github.com/YogeshT22/gopherproxy
cd gopherproxy

# Start everything (proxy + sentinel + redis + prometheus + grafana)
make up

# Verify the proxy is responding
curl http://localhost:8080

# Check metrics
curl http://localhost:2112/metrics

# Open Grafana: http://localhost:3000  (admin/admin)
# Open Prometheus: http://localhost:9090
```

### Run Mock Backends (local, no Docker)

```bash
# Start three Python HTTP servers on :8081, :8082, :8083
make mock-backends

# Register them in Redis (one-time setup)
redis-cli -p 16379 SADD gopher_backends \
  "http://localhost:8081" \
  "http://localhost:8082" \
  "http://localhost:8083"
```

---

## Load Testing

All scenarios use `k6-load-test.js`:

```bash
make k6-smoke      # 10 VUs, 30s  — quick sanity check
make k6-load       # ramp 0→500 VUs over 6 min — realistic load
make k6-spike      # sudden jump to 500 VUs — stress test
make k6-sustained  # 200 VUs for 5 min — sustained throughput
make k6-rps200     # arrival-rate: 200 req/s for 2 min
```
---

## Endpoints Reference Table

| URL                             | Description                          |
| ------------------------------- | ------------------------------------ |
| `http://localhost:8080`         | Proxy - load-balanced entry point    |
| `http://localhost:2112/healthz` | Liveness probe - returns `ok`        |
| `http://localhost:2112/metrics` | Raw Prometheus metrics (text format) |
| `http://localhost:9090`         | Prometheus expression browser        |
| `http://localhost:9090/targets` | Scrape target health                 |
| `http://localhost:3000`         | Grafana - **GopherProxy Dashboard**  |

> **Grafana login:** `admin` / `admin` (or whatever is set in `.env`)

---

### Verified Results (local 4-core hardware, WSL2)

| Scenario | VUs | Duration | RPS | P95 Latency |
|----------|-----|----------|-----|-------------|
| sustained | 200 | 5 min | ~4,200 req/s | < 35 ms |
| load (spike phase) | 500 | 30s sustain | ~1,478 req/s | ~5.7 ms |

> **Source:** `summary.json` — 315,969 total requests; P95 duration 5.73 ms; 0 errors (429s are expected and counted separately).

Rate-limiter effectiveness: `rate_limit_count` counter shows ~315,518 throttled requests out of 315,969 total under the default burst-5 / 2 req/s config during the spike scenario.

---

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `REDIS_URL` | `localhost:16379` | Redis address (`host:port`) |
| `PROXY_PORT` | `8080` | Port the proxy listens on |
| `METRICS_PORT` | `2112` | Port for `/metrics` and `/healthz` |
| `LIMITER_TTL` | `10m` | How long before an idle IP's limiter is evicted |
| `LIMITER_JANITOR_INTERVAL` | `1m` | How often the janitor runs |
| `SENTINEL_TARGETS` | `host.docker.internal:8081,...` | Comma-separated `host:port` targets for Sentinel |

---

## Running Tests

```bash
# Unit tests with race detector
make test
# or: go test -race -count=1 ./...

# Lint (requires golangci-lint)
make lint
```

18 unit tests covering: round-robin correctness, dead-backend skipping, per-IP rate limiter isolation, middleware pass-through, `updateServerPool` deduplication, and concurrent safety.

---

## Stack

| Component | Version | Role |
|-----------|---------|------|
| Go | 1.24.5 | Proxy + Sentinel language |
| Redis | 7.4-alpine | Service registry (gopher_backends SET) |
| Prometheus | v3.2.1 | Metrics scraping |
| Grafana | 11.6.0 | Metrics visualisation |
| Docker Compose | v2 | Local orchestration |
| k6 | latest | Load testing |

---

## Known Limitations (POC Scope)

- Backends are **never removed** from the in-memory `ServerPool` — only their `Alive` flag is toggled. A long-running process accumulates stale entries.
- Rate limiter parameters require a process restart to change.
- No TLS on the proxy port (`:8080` is plain HTTP).
- Alertmanager is not configured.
- Redis has no authentication in the default compose setup.

---

## Production Notes

- **Redis port 16379** is exposed to the host for local debugging only - remove the `ports:` mapping before any real deployment
- **Grafana sign-up** is disabled (`GF_USERS_ALLOW_SIGN_UP=false`) and analytics reporting is off
- **Prometheus retains 15 days** of data in the `prometheus_data` named volume
- **All containers run as non-root** (UID/GID `1001`) and have CPU + memory limits set
- **Sentinel `kill -0 1` health check** - more reliable than `pgrep` on BusyBox/Alpine, where the full path (`/app/sentinel`) does not match an exact-name `pgrep -x` search

---

## Debugging Log

Real issues hit during development, recorded here as reference:

| Symptom                            | Root Cause                                                                                    | Fix                                                                       |
| ---------------------------------- | --------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------- |
| Metrics showed 0 traffic           | `pool` variable shadowed inside handler - handler read an empty instance                      | Removed `:=` re-declaration; handler closes over the outer `pool` pointer |
| `bind: forbidden` on port `6379`   | Hyper-V reserved the port range on Windows                                                    | Mapped Redis to high port `16379` on the host                             |
| Proxy crashed on empty registry    | Modulo by zero when `len(backends) == 0`                                                      | Defensive `if l == 0 { return nil }` before the `% l` operation           |
| `gopher-sentinel` always unhealthy | `pgrep -x sentinel` on BusyBox matches full path, not basename                                | Replaced with `kill -0 1`                                                 |
| Grafana panels showed "No data"    | Datasource UID auto-generated by Grafana didn't match `"uid": "prometheus"` in dashboard JSON | Added `uid: prometheus` to `datasource.yml`                               |

---

## License

MIT - see [LICENSE](LICENSE)

**Author:** Yogesh T · [GitHub @YogeshT22](https://github.com/YogeshT22)