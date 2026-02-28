package main

import (
	"context"
	"log"
	"net"
	"os"
	"time"

	"github.com/redis/go-redis/v9"
)

var ctx = context.Background()

func main() {
	redisAddr := os.Getenv("REDIS_URL")
	if redisAddr == "" {
		redisAddr = "localhost:16379"
	}
	rdb := redis.NewClient(&redis.Options{Addr: redisAddr})

	_, err := rdb.Ping(ctx).Result()
	if err != nil {
		log.Fatalf("Redis connection failed: %v", err)
	}
	log.Println("Connected to Redis at", redisAddr)

	// Ports we want to monitor on the host machine
	targets := []string{
		"host.docker.internal:8081",
		"host.docker.internal:8082",
		"host.docker.internal:8083",
	}

	log.Println("Sentinel started. Monitoring backends...")

	for {
		for _, target := range targets {
			url := "http://" + target

			// Try to connect to the TCP port
			conn, err := net.DialTimeout("tcp", target, 1*time.Second)

			if err != nil {
				// Port is DOWN -> Remove from Redis
				rdb.SRem(ctx, "gopher_backends", url)
			} else {
				// Port is UP -> Add to Redis
				rdb.SAdd(ctx, "gopher_backends", url)
				conn.Close()
			}
		}
		time.Sleep(2 * time.Second)
	}

}
