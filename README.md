# GopherProxy & Sentinel: Dynamic API Gateway & a custom-built Data Plane (Load Balancer).

![alt text](https://img.shields.io/badge/Language-Go-00ADD8?style=flat-square&logo=go) ![alt text](https://img.shields.io/badge/Container-Docker-2496ED?style=flat-square&logo=docker)

![alt text](https://img.shields.io/badge/Registry-Redis-DC382D?style=flat-square&logo=redis) ![alt text](https://img.shields.io/badge/License-MIT-yellow.svg?style=flat-square)

### PROJECT VERSION: 1.0 - DEC 31, 2025

**GopherProxy & Sentinel** is a production-grade, health-aware **Load Balancer** and **Service Discovery** engine built entirely in Go. It simulates a cloud-native environment locally, featuring a decoupled **Data Plane (Proxy)** and **Control Plane (Sentinel)**, managed via **Redis**, and fully observable through **Prometheus and Grafana**.

---

_**NOTE**_: _This project is a learning exercise and should not be used in production environments. It is designed to demonstrate core concepts of service discovery, load balancing, and observability in a simplified manner.

---

## Architecture Overview

The system follows a three-tier distributed architecture:

1.  **The Data Plane (GopherProxy):** A high-concurrency Go reverse proxy that routes traffic to healthy backends and enforces security via Rate Limiting.
2.  **The Control Plane (Sentinel):** An automated monitoring agent that pings backend ports and updates the shared Service Registry.
3.  **The Service Registry (Redis):** A persistent "Source of Truth" where backends are registered and de-duplicated.
4.  **The Observability Stack:** Prometheus scrapes metrics from the Proxy, and Grafana visualizes the traffic flow and backend health.

---

### Architecture Diagram

## ![arch diagram](assets/arch-diagram.png)

## Key Features

- **Decoupled Service Discovery:** Uses Redis Sets to manage dynamic backend registration without restarting the proxy.
- **High-Performance Concurrency:** Leverages `sync.RWMutex` for efficient multi-reader access and `sync/atomic` for thread-safe load balancing.
- **Security & Hardening:**
  - **Rate Limiting:** Implements a Token Bucket algorithm to prevent local DDoS.
  - **Graceful Shutdown:** Handles OS signals (`SIGINT`, `SIGTERM`) to ensure zero-drop connection closing.
  - **Isolation:** Runs as a non-privileged `gopheruser` inside the container.
- **Observability (SRE):** Exports custom Prometheus metrics including Request Counters and Healthy Backend Gauges.
- **Cloud-Native Optimization:** Multi-stage Docker build using `ARG` and `Static Linking`, resulting in a tiny **13.4MB** image.

---

## Tech Stack

- **Language:** Go (Standard Library, `httputil`, `context`)
- **Registry:** Redis
- **Monitoring:** Prometheus & Grafana
- **Deployment:** Docker, Docker Compose
- **Networking:** HTTP Reverse Proxy, TCP Dialing

---

## Performance Optimization

- **13.4MB Image Size:** Achieved by using Multi-Stage Docker builds and Static Linking (CGO_ENABLED=0). This is a 98% reduction from the standard golang image.
- **Static Binary:** The Go binary is stripped of debug symbols (-ldflags="-s -w"), resulting in sub-millisecond container startup times.

---

## 🕵️ The Debugging Journey (Lessons Learned)

| Challenge             | Discovery                                              | Solution                                                                                                                          |
| --------------------- | ------------------------------------------------------ | --------------------------------------------------------------------------------------------------------------------------------- |
| Variable Shadowing    | Metrics showed 0 traffic even when requests succeeded. | Realized pool := &ServerPool{} was re-declared in a local scope, causing the HTTP handler to read from an uninitialized instance. |
| Windows Port Conflict | bind: forbidden by access permissions on port 6379.    | Discovered Windows/Hyper-V port exclusions via netsh. Pivoted host mapping to a high-range port (16379).                          |
| Modulo-by-Zero Panic  | Proxy crashed when Redis registry was empty.           | Implemented a defensive check in the Round-Robin logic to return a 503 error if len(backends) == 0.                               |

---

## Getting Started

### Prerequisites

- Docker & Docker Compose
- Python (to run mock backend servers)

### 1. Build and Start the Cluster

```bash
docker-compose up --build
```

### 2. Start Local Backends (Microservices)

Open two separate terminals:

```bash
# Open new Terminal and run mock backend servers
cd mock_backends/server1 && python -m http.server 8081 --bind 0.0.0.0
# run second backend.
cd mock_backends/server2 && python -m http.server 8082 --bind 0.0.0.0
```

### 3. Generate Traffic

```bash
# Use another terminal to generate traffic to the proxy
# Observe the Rate Limiter (2 req/sec)
for i in {1..50}; do curl -I http://localhost:8080; done
```

- **Grafana**: http://localhost:3000 (admin/admin)
- **Prometheus**: http://localhost:9090

---

## License

This project is licensed under the MIT License - see the LICENSE file for details.

---

## Acknowledgments

- Inspired by the architecture of Envoy Proxy and Kubernetes Service Mesh.
- Thanks to the Go community for excellent documentation and libraries.

---

**Author**: Yogesh T - [GitHub Profile](https://github.com/YogeshT22) :)

---
