package main

import (
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"
)

func main() {
	// 1. Define where the traffic should go (Our "Backend")
	// For now, let's assume a local service is running on port 8081
	targetURL := "http://localhost:8081"
	url, err := url.Parse(targetURL)
	if err != nil {
		log.Fatalf("Failed to parse target URL: %v", err)
	}

	// 2. Initialize the Reverse Proxy
	proxy := httputil.NewSingleHostReverseProxy(url)

	// 3. Customize the Proxy behavior (Production Standard)
	proxy.Transport = &http.Transport{
		MaxIdleConns:        100,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
	}

	// 4. The "Director" - This modifies the request before it reaches the backend
	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		// Best Practice: Add headers to let the backend know it's behind a proxy
		req.Header.Set("X-Forwarded-Host", req.Host)
		req.Header.Set("X-Origin-Proxy", "GopherProxy")
	}

	// 5. Define the entry point for our Proxy
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		log.Printf("[PROXY] Received request: %s %s", r.Method, r.URL.Path)
		proxy.ServeHTTP(w, r)
	})

	// 6. Start the server with a timeout (Security Practice)
	server := &http.Server{
		Addr:         ":8080",
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		Handler:      nil, // uses the default mux
	}

	log.Println("GopherProxy is running on :8080...")
	log.Printf("Forwarding traffic to: %s", targetURL)

	if err := server.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}
