package main

import (
	"context"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"
)

var logger = slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
	Level: slog.LevelInfo,
}))

// --- BUILD METADATA (injected by -ldflags at build time) ---
var (
	Version = "dev"
	Commit  = "unknown"
)

// getEnv returns an env variable's value or a fallback default.
func getEnv(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}

// getTargets reads a comma-separated list of host:port targets from
// SENTINEL_TARGETS env var, falling back to the built-in defaults.
func getTargets() []string {
	raw := os.Getenv("SENTINEL_TARGETS")
	if raw != "" {
		parts := strings.Split(raw, ",")
		for i := range parts {
			parts[i] = strings.TrimSpace(parts[i])
		}
		return parts
	}
	return []string{
		"host.docker.internal:8081",
		"host.docker.internal:8082",
		"host.docker.internal:8083",
	}
}

func checkTarget(ctx context.Context, rdb *redis.Client, target string) {
	redisKey := "gopher_backends"
	registryURL := "http://" + target

	dialCtx, cancel := context.WithTimeout(ctx, 1*time.Second)
	defer cancel()

	conn, err := (&net.Dialer{}).DialContext(dialCtx, "tcp", target)
	if err != nil {
		// Backend is DOWN — remove from registry.
		if removed, rerr := rdb.SRem(ctx, redisKey, registryURL).Result(); rerr != nil {
			logger.Error("Redis SRem failed", "target", target, "error", rerr)
		} else if removed > 0 {
			logger.Warn("backend deregistered (DOWN)", "target", target)
		}
		return
	}
	_ = conn.Close()

	// Backend is UP — ensure it is in the registry.
	if added, rerr := rdb.SAdd(ctx, redisKey, registryURL).Result(); rerr != nil {
		logger.Error("Redis SAdd failed", "target", target, "error", rerr)
	} else if added > 0 {
		logger.Info("backend registered (UP)", "target", target)
	}
}

func main() {
	// ── 1. Redis connection ─────────────────────────────────────────────────
	redisAddr := getEnv("REDIS_URL", "localhost:16379")
	rdb := redis.NewClient(&redis.Options{
		Addr:         redisAddr,
		DialTimeout:  5 * time.Second,
		ReadTimeout:  3 * time.Second,
		WriteTimeout: 3 * time.Second,
	})

	// Verify Redis at startup.
	pingCtx, pingCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer pingCancel()
	if _, err := rdb.Ping(pingCtx).Result(); err != nil {
		logger.Error("cannot reach Redis at startup", "addr", redisAddr, "error", err)
		os.Exit(1)
	}
	logger.Info("connected to Redis", "addr", redisAddr)

	// ── 2. Root context ─────────────────────────────────────────────────────
	rootCtx, rootCancel := context.WithCancel(context.Background())
	defer rootCancel()

	targets := getTargets()
	logger.Info("Sentinel started", "targets", targets, "version", Version, "commit", Commit)

	// ── 3. Monitoring loop ──────────────────────────────────────────────────
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-rootCtx.Done():
				return
			case <-ticker.C:
				for _, target := range targets {
					checkTarget(rootCtx, rdb, target)
				}
			}
		}
	}()

	// ── 4. Graceful shutdown ────────────────────────────────────────────────
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	logger.Info("shutdown signal received")
	rootCancel()
	_ = rdb.Close()
	logger.Info("Sentinel stopped cleanly")
}
