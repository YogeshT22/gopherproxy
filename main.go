package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"
	"golang.org/x/time/rate"
)

// --- BUILD METADATA (injected by -ldflags at build time) ---
var (
	Version = "dev"
	Commit  = "unknown"
)

// --- METRICS ---
var (
	opsProcessed = promauto.NewCounter(prometheus.CounterOpts{
		Name: "gopherproxy_processed_requests_total",
		Help: "The total number of processed requests",
	})
	opsDropped = promauto.NewCounter(prometheus.CounterOpts{
		Name: "gopherproxy_dropped_requests_total",
		Help: "The total number of requests dropped (rate-limited or no backend)",
	})
	activeBackends = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "gopherproxy_active_backends",
		Help: "The number of backends currently marked as ALIVE",
	})
	requestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "gopherproxy_request_duration_seconds",
		Help:    "Histogram of request durations in seconds",
		Buckets: prometheus.DefBuckets,
	}, []string{"backend"})
)

// --- STRUCTURED LOGGER ---
var logger = slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
	Level: slog.LevelInfo,
}))

// --- PER-IP RATE LIMITER ---
type ipRateLimiter struct {
	limiters sync.Map
	r        rate.Limit
	b        int
}

func newIPRateLimiter(r rate.Limit, b int) *ipRateLimiter {
	return &ipRateLimiter{r: r, b: b}
}

func (i *ipRateLimiter) getLimiter(ip string) *rate.Limiter {
	val, _ := i.limiters.LoadOrStore(ip, rate.NewLimiter(i.r, i.b))
	return val.(*rate.Limiter)
}

func (i *ipRateLimiter) limitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			ip = r.RemoteAddr
		}
		if !i.getLimiter(ip).Allow() {
			opsDropped.Inc()
			http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// --- LOGGING MIDDLEWARE ---
type responseWriter struct {
	http.ResponseWriter
	status int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.status = code
	rw.ResponseWriter.WriteHeader(code)
}

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rw, r)
		logger.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"remote_addr", r.RemoteAddr,
			"status", rw.status,
			"duration_ms", time.Since(start).Milliseconds(),
		)
	})
}

// --- BACKEND LOGIC ---
type Backend struct {
	URL          *url.URL
	Alive        bool
	mux          sync.RWMutex
	ReverseProxy *httputil.ReverseProxy
}

func (b *Backend) SetAlive(alive bool) {
	b.mux.Lock()
	b.Alive = alive
	b.mux.Unlock()
}

func (b *Backend) IsAlive() bool {
	b.mux.RLock()
	defer b.mux.RUnlock()
	return b.Alive
}

type ServerPool struct {
	backends []*Backend
	current  uint64
	mux      sync.RWMutex
}

func (s *ServerPool) GetNextPeer() *Backend {
	s.mux.RLock()
	defer s.mux.RUnlock()

	l := uint64(len(s.backends))
	if l == 0 {
		return nil
	}

	next := atomic.AddUint64(&s.current, 1)
	for i := 0; i < len(s.backends); i++ {
		idx := (next + uint64(i)) % l
		if s.backends[idx].IsAlive() {
			return s.backends[idx]
		}
	}
	return nil
}

func (s *ServerPool) HealthCheck(ctx context.Context) {
	s.mux.RLock()
	backends := s.backends
	s.mux.RUnlock()

	aliveCount := 0
	for _, b := range backends {
		alive := isBackendAlive(ctx, b.URL)
		wasAlive := b.IsAlive()
		b.SetAlive(alive)
		if alive {
			aliveCount++
			if !wasAlive {
				logger.Info("backend recovered", "url", b.URL.String())
			}
		} else if wasAlive {
			logger.Warn("backend went down", "url", b.URL.String())
		}
	}
	activeBackends.Set(float64(aliveCount))
}

func isBackendAlive(ctx context.Context, u *url.URL) bool {
	dialCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(dialCtx, "tcp", u.Host)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func updateServerPool(s *ServerPool, endpoints []string, transport *http.Transport) {
	s.mux.Lock()
	defer s.mux.Unlock()

	for _, addr := range endpoints {
		exists := false
		for _, b := range s.backends {
			if b.URL.String() == addr {
				exists = true
				break
			}
		}
		if !exists {
			u, err := url.Parse(addr)
			if err != nil {
				logger.Error("invalid backend URL, skipping", "addr", addr, "error", err)
				continue
			}
			proxy := httputil.NewSingleHostReverseProxy(u)
			proxy.Transport = transport
			proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
				logger.Error("proxy error", "backend", u.String(), "error", err)
				opsDropped.Inc()
				http.Error(w, "Backend unavailable", http.StatusBadGateway)
			}
			s.backends = append(s.backends, &Backend{URL: u, Alive: true, ReverseProxy: proxy})
			logger.Info("backend registered", "url", addr)
		}
	}
}

// getEnv returns the value of an env variable or a fallback default.
func getEnv(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
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

	// Verify Redis is reachable at startup.
	pingCtx, pingCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer pingCancel()
	if _, err := rdb.Ping(pingCtx).Result(); err != nil {
		logger.Error("cannot reach Redis at startup", "addr", redisAddr, "error", err)
		os.Exit(1)
	}
	logger.Info("connected to Redis", "addr", redisAddr, "version", Version, "commit", Commit)

	// ── 2. Root context cancelled on shutdown ───────────────────────────────
	rootCtx, rootCancel := context.WithCancel(context.Background())
	defer rootCancel()

	// ── 3. Metrics / health server ──────────────────────────────────────────
	metricsPort := getEnv("METRICS_PORT", "2112")
	metricsMux := http.NewServeMux()
	metricsMux.Handle("/metrics", promhttp.Handler())
	metricsMux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintln(w, "ok")
	})
	metricsServer := &http.Server{
		Addr:         ":" + metricsPort,
		Handler:      metricsMux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
	}
	go func() {
		logger.Info("metrics server listening", "port", metricsPort)
		if err := metricsServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("metrics server error", "error", err)
		}
	}()

	// ── 4. Transport & backend pool ─────────────────────────────────────────
	pool := &ServerPool{}
	transport := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,
		DialContext: (&net.Dialer{
			Timeout:   5 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		TLSHandshakeTimeout: 10 * time.Second,
	}

	// ── 5. Redis discovery loop (ticker, context-aware) ─────────────────────
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-rootCtx.Done():
				return
			case <-ticker.C:
				endpoints, err := rdb.SMembers(rootCtx, "gopher_backends").Result()
				if err != nil {
					logger.Warn("Redis SMembers failed", "error", err)
					continue
				}
				if len(endpoints) > 0 {
					updateServerPool(pool, endpoints, transport)
				}
			}
		}
	}()

	// ── 6. Health-check loop (ticker, context-aware) ────────────────────────
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-rootCtx.Done():
				return
			case <-ticker.C:
				pool.HealthCheck(rootCtx)
			}
		}
	}()

	// ── 7. Proxy handler ────────────────────────────────────────────────────
	proxyPort := getEnv("PROXY_PORT", "8080")
	ipLimiter := newIPRateLimiter(rate.Every(500*time.Millisecond), 5)

	lbHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		peer := pool.GetNextPeer()
		if peer == nil {
			opsDropped.Inc()
			http.Error(w, "No healthy backends available", http.StatusServiceUnavailable)
			return
		}
		start := time.Now()
		opsProcessed.Inc()
		peer.ReverseProxy.ServeHTTP(w, r)
		requestDuration.WithLabelValues(peer.URL.Host).Observe(time.Since(start).Seconds())
	})

	server := &http.Server{
		Addr:         ":" + proxyPort,
		Handler:      loggingMiddleware(ipLimiter.limitMiddleware(lbHandler)),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// ── 8. Graceful shutdown ────────────────────────────────────────────────
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	go func() {
		logger.Info("GopherProxy listening", "port", proxyPort)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("proxy server failed", "error", err)
			os.Exit(1)
		}
	}()

	<-stop
	logger.Info("shutdown signal received, draining connections…")
	rootCancel()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("graceful proxy shutdown failed", "error", err)
	}
	if err := metricsServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("graceful metrics shutdown failed", "error", err)
	}
	_ = rdb.Close()
	logger.Info("GopherProxy stopped cleanly")
}
