package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

// ── ServerPool tests ──────────────────────────────────────────────────────────

func TestGetNextPeer_EmptyPool(t *testing.T) {
	pool := &ServerPool{}
	if got := pool.GetNextPeer(); got != nil {
		t.Fatalf("expected nil from empty pool, got %v", got)
	}
}

func TestGetNextPeer_AllDead(t *testing.T) {
	pool := &ServerPool{}
	for _, raw := range []string{"http://dead1:9001", "http://dead2:9002"} {
		u, _ := url.Parse(raw)
		pool.backends = append(pool.backends, &Backend{URL: u, Alive: false})
	}
	if got := pool.GetNextPeer(); got != nil {
		t.Fatalf("expected nil when all backends are dead, got %v", got.URL)
	}
}

func TestGetNextPeer_RoundRobin(t *testing.T) {
	pool := &ServerPool{}
	urls := []string{"http://b1:9001", "http://b2:9002", "http://b3:9003"}
	for _, raw := range urls {
		u, _ := url.Parse(raw)
		pool.backends = append(pool.backends, &Backend{URL: u, Alive: true})
	}

	seen := map[string]int{}
	for i := 0; i < 9; i++ {
		peer := pool.GetNextPeer()
		if peer == nil {
			t.Fatal("unexpected nil from healthy pool")
		}
		seen[peer.URL.Host]++
	}
	// Every backend should be hit exactly 3 times across 9 requests.
	for _, h := range []string{"b1:9001", "b2:9002", "b3:9003"} {
		if seen[h] != 3 {
			t.Errorf("backend %s hit %d times, want 3", h, seen[h])
		}
	}
}

func TestGetNextPeer_SkipsDeadBackend(t *testing.T) {
	pool := &ServerPool{}
	urls := []string{"http://alive:9001", "http://dead:9002"}
	alive := []bool{true, false}
	for i, raw := range urls {
		u, _ := url.Parse(raw)
		pool.backends = append(pool.backends, &Backend{URL: u, Alive: alive[i]})
	}

	for i := 0; i < 5; i++ {
		peer := pool.GetNextPeer()
		if peer == nil {
			t.Fatal("expected a live backend, got nil")
		}
		if peer.URL.Host == "dead:9002" {
			t.Fatal("should never route to a dead backend")
		}
	}
}

// ── Backend alive/dead toggle tests ──────────────────────────────────────────

func TestBackend_SetAndIsAlive(t *testing.T) {
	u, _ := url.Parse("http://test:9001")
	b := &Backend{URL: u, Alive: true}

	b.SetAlive(false)
	if b.IsAlive() {
		t.Error("expected backend to be dead after SetAlive(false)")
	}
	b.SetAlive(true)
	if !b.IsAlive() {
		t.Error("expected backend to be alive after SetAlive(true)")
	}
}

// ── updateServerPool tests ────────────────────────────────────────────────────

func TestUpdateServerPool_AddNew(t *testing.T) {
	pool := &ServerPool{}
	transport := &http.Transport{}
	updateServerPool(pool, []string{"http://localhost:9001"}, transport)

	pool.mux.RLock()
	defer pool.mux.RUnlock()
	if len(pool.backends) != 1 {
		t.Fatalf("expected 1 backend, got %d", len(pool.backends))
	}
	if pool.backends[0].URL.Host != "localhost:9001" {
		t.Errorf("unexpected host: %s", pool.backends[0].URL.Host)
	}
}

func TestUpdateServerPool_NoDuplicates(t *testing.T) {
	pool := &ServerPool{}
	transport := &http.Transport{}
	updateServerPool(pool, []string{"http://localhost:9001"}, transport)
	updateServerPool(pool, []string{"http://localhost:9001"}, transport) // same URL again

	pool.mux.RLock()
	defer pool.mux.RUnlock()
	if len(pool.backends) != 1 {
		t.Fatalf("expected 1 backend (no duplicate), got %d", len(pool.backends))
	}
}

func TestUpdateServerPool_InvalidURL(t *testing.T) {
	pool := &ServerPool{}
	transport := &http.Transport{}
	// "://bad url" is unparseable — should be skipped, not panic.
	updateServerPool(pool, []string{"://bad url"}, transport)

	pool.mux.RLock()
	defer pool.mux.RUnlock()
	if len(pool.backends) != 0 {
		t.Fatalf("expected 0 backends after invalid URL, got %d", len(pool.backends))
	}
}

// ── Per-IP rate limiter tests ─────────────────────────────────────────────────

func TestIPRateLimiter_AllowsBurst(t *testing.T) {
	il := newIPRateLimiter(rate.Every(time.Second), 3) // burst of 3
	for i := 0; i < 3; i++ {
		if !il.getLimiter("1.2.3.4").Allow() {
			t.Fatalf("request %d should be allowed within burst", i+1)
		}
	}
}

func TestIPRateLimiter_BlocksAfterBurst(t *testing.T) {
	il := newIPRateLimiter(rate.Every(time.Hour), 2) // very slow refill
	il.getLimiter("1.2.3.4").Allow()
	il.getLimiter("1.2.3.4").Allow()
	if il.getLimiter("1.2.3.4").Allow() {
		t.Error("3rd request should be blocked after burst of 2 is exhausted")
	}
}

func TestIPRateLimiter_IsolatesIPs(t *testing.T) {
	il := newIPRateLimiter(rate.Every(time.Hour), 1) // burst of 1
	// Exhaust IP A
	il.getLimiter("10.0.0.1").Allow()
	// IP B should still have its own fresh bucket
	if !il.getLimiter("10.0.0.2").Allow() {
		t.Error("a different IP should not be affected by another IP's rate limit")
	}
}

// ── Middleware integration tests ──────────────────────────────────────────────

func TestLoggingMiddleware_PassesThrough(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})
	handler := loggingMiddleware(inner)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusTeapot {
		t.Errorf("expected status 418, got %d", rr.Code)
	}
}

func TestLimitMiddleware_Allows(t *testing.T) {
	il := newIPRateLimiter(rate.Every(time.Millisecond), 10)
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := il.limitMiddleware(inner)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "1.2.3.4:5678"
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

func TestLimitMiddleware_Blocks(t *testing.T) {
	il := newIPRateLimiter(rate.Every(time.Hour), 1) // burst=1, refill very slow
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := il.limitMiddleware(inner)

	// First request consumes the burst token.
	req1 := httptest.NewRequest(http.MethodGet, "/", nil)
	req1.RemoteAddr = "5.5.5.5:1234"
	httptest.NewRecorder() // discard
	handler.ServeHTTP(httptest.NewRecorder(), req1)

	// Second request from same IP should be blocked.
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.RemoteAddr = "5.5.5.5:1234"
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req2)

	if rr.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429 Too Many Requests, got %d", rr.Code)
	}
}

// ── getEnv tests ──────────────────────────────────────────────────────────────

func TestGetEnv_UsesDefault(t *testing.T) {
	val := getEnv("THIS_VAR_DOES_NOT_EXIST_12345", "mydefault")
	if val != "mydefault" {
		t.Errorf("expected 'mydefault', got %q", val)
	}
}

func TestGetEnv_UsesEnvVar(t *testing.T) {
	t.Setenv("TEST_GOPHER_VAR", "fromenv")
	val := getEnv("TEST_GOPHER_VAR", "default")
	if val != "fromenv" {
		t.Errorf("expected 'fromenv', got %q", val)
	}
}

// ── Atomic counter sanity check ───────────────────────────────────────────────

func TestServerPool_AtomicConcurrency(t *testing.T) {
	pool := &ServerPool{}
	for i := 0; i < 5; i++ {
		u, _ := url.Parse("http://backend:9000")
		pool.backends = append(pool.backends, &Backend{URL: u, Alive: true})
	}

	// Fire 100 concurrent goroutines — should never panic or race.
	done := make(chan struct{})
	for i := 0; i < 100; i++ {
		go func() {
			pool.GetNextPeer()
			done <- struct{}{}
		}()
	}
	for i := 0; i < 100; i++ {
		<-done
	}
	if pool.current != 100 {
		// atomic counter should be exactly 100
		val := atomic.LoadUint64(&pool.current)
		if val != 100 {
			t.Errorf("expected counter=100 after 100 calls, got %d", val)
		}
	}
}
