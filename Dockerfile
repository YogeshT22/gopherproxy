# --- STAGE 1: The Builder ---
FROM golang:1.24.5-alpine AS builder

# This argument tells Docker which folder/app to build
ARG APP_NAME=gopher-proxy


WORKDIR /app

# Install git/ca-certificates if needed
RUN apk add --no-cache git

# Copy and download dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy the rest of the source code
COPY . .

# Build the binary
# -ldflags="-s -w" removes debug info to shrink the size
# CGO_ENABLED=0 makes it a static binary (no OS dependencies)
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o gopher-proxy .

# --- STAGE 2: The Final Image ---
FROM alpine:3.18
WORKDIR /usr/local/bin/

# Security: Create a non-privileged user
RUN adduser -D gopheruser
USER gopheruser

# Copy only the compiled binary from the builder
COPY --from=builder /app/gopher-proxy .

# Standard Proxy Port
EXPOSE 8080

# Start the proxy
ENTRYPOINT ["./gopher-proxy"]
