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

# IMPORTANT: change working directory to the selected app
WORKDIR /app/${APP_NAME}

# Build the binary
# -ldflags="-s -w" removes debug info to shrink the size
# CGO_ENABLED=0 makes it a static binary (no OS dependencies)
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o app .

# --- STAGE 2 ---
FROM alpine:3.18

ARG APP_NAME=gopher-proxy

WORKDIR /usr/local/bin/
RUN adduser -D gopheruser
USER gopheruser

COPY --from=builder /app/${APP_NAME}/app .

ENTRYPOINT ["./app"]
