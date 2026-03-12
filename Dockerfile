# ─── Stage 1: Builder ────────────────────────────────────────────────────────
FROM golang:1.24.5-alpine3.21 AS builder

# Which sub-app to build: "." for the proxy root, "sentinel" for the sentinel.
ARG APP_NAME=.
# Optional: bake version metadata into the binary.
ARG VERSION=dev
ARG COMMIT=unknown

WORKDIR /app

# ca-certificates needed at runtime for TLS; git needed by some go modules.
RUN apk add --no-cache ca-certificates git

# Download dependencies first (better layer caching).
COPY go.mod go.sum ./
RUN go mod download && go mod verify

# Copy full source.
COPY . .

# Build the selected app.
WORKDIR /app/${APP_NAME}
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go build \
  -ldflags="-s -w -X main.Version=${VERSION} -X main.Commit=${COMMIT}" \
  -trimpath \
  -o /out/app \
  .

# ─── Stage 2: Runtime ────────────────────────────────────────────────────────
FROM alpine:3.21

# Copy CA bundle so the binary can make outbound TLS calls.
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

ARG APP_NAME=.

WORKDIR /app

# Create a non-root user with a fixed UID/GID for predictability.
RUN addgroup -g 1001 -S gophergroup && \
  adduser  -u 1001 -S gopheruser -G gophergroup

COPY --from=builder /out/app ./app

# Hand off ownership before switching user.
RUN chown gopheruser:gophergroup /app/app

USER gopheruser

# Document the ports this container listens on.
EXPOSE 8080 2112

# Docker will poll /healthz every 30 s; 3 failures = unhealthy.
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD wget -qO- http://localhost:2112/healthz || exit 1

ENTRYPOINT ["/app/app"]
