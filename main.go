package main

import (
	"context"
	"log"
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

var ctx = context.Background()

// --- METRICS ---
var (
	opsProcessed = promauto.NewCounter(prometheus.CounterOpts{
		Name: "gopherproxy_processed_requests_total",
		Help: "The total number of processed requests",
	})
	activeBackends = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "gopherproxy_active_backends",
		Help: "The number of backends currently marked as ALIVE",
	})
)

// --- RATE LIMITER ---
var limiter = rate.NewLimiter(rate.Every(time.Second/2), 5)

func limitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !limiter.Allow() {
			http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
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

func (s *ServerPool) AddBackend(b *Backend) {
	s.mux.Lock()
	defer s.mux.Unlock()
	s.backends = append(s.backends, b)
}

func (s *ServerPool) GetNextPeer() *Backend {
	s.mux.RLock()
	defer s.mux.RUnlock()

	l := uint64(len(s.backends))
	if l == 0 {
		return nil // SAFETY: No backends, no panic
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

func (s *ServerPool) HealthCheck() {
	s.mux.RLock()
	backends := s.backends
	s.mux.RUnlock()

	aliveCount := 0
	for _, b := range backends {
		alive := isBackendAlive(b.URL)
		b.SetAlive(alive)
		if alive {
			aliveCount++
		}
	}
	activeBackends.Set(float64(aliveCount))
}

func isBackendAlive(u *url.URL) bool {
	conn, err := net.DialTimeout("tcp", u.Host, 2*time.Second)
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
			u, _ := url.Parse(addr)
			proxy := httputil.NewSingleHostReverseProxy(u)
			proxy.Transport = transport
			s.backends = append(s.backends, &Backend{URL: u, Alive: true, ReverseProxy: proxy})
			log.Printf("[DISCOVERY] Registered: %s", addr)
		}
	}
}

func main() {
	// 1. Redis Connection
	redisAddr := os.Getenv("REDIS_URL")
	if redisAddr == "" {
		redisAddr = "localhost:16379"
	}
	rdb := redis.NewClient(&redis.Options{Addr: redisAddr})

	// 2. Metrics Server (Port 2112)
	go func() {
		http.Handle("/metrics", promhttp.Handler())
		http.ListenAndServe(":2112", nil)
	}()

	// 3. Initialize Shared Pool
	pool := &ServerPool{}
	transport := &http.Transport{MaxIdleConns: 100}

	// 4. Redis Discovery Loop
	go func() {
		for {
			endpoints, _ := rdb.SMembers(ctx, "gopher_backends").Result()
			if len(endpoints) > 0 {
				updateServerPool(pool, endpoints, transport)
			}
			time.Sleep(5 * time.Second)
		}
	}()

	// 5. Health Check Loop
	go func() {
		for {
			pool.HealthCheck()
			time.Sleep(10 * time.Second)
		}
	}()

	// 6. Proxy Handler
	lbHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		peer := pool.GetNextPeer()
		if peer != nil {
			opsProcessed.Inc() // Increment counter
			peer.ReverseProxy.ServeHTTP(w, r)
			return
		}
		http.Error(w, "No healthy backends available", http.StatusServiceUnavailable)
	})

	server := &http.Server{
		Addr:    ":8080",
		Handler: limitMiddleware(lbHandler),
	}

	// 7. Start & Graceful Shutdown
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	go func() {
		log.Println("GopherProxy running on :8080...")
		server.ListenAndServe()
	}()

	<-stop
	log.Println("Shutting down...")
	server.Shutdown(ctx)
}
