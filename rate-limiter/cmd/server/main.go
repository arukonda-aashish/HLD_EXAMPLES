package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	ratelimiter "github.com/arukonda-aashish/rate-limiter/ratelimiter"

	"github.com/redis/go-redis/v9"
)

func main() {

	redisAddr := getEnv("REDIS_ADDR", "localhost:6379")
	rateLimit := getEnvInt("RATE_LIMIT", 10)
	windowSec := getEnvInt("WINDOW_SIZE_SEC", 1)
	port := getEnv("PORT", "8080")

	fmt.Println("╔══════════════════════════════════════════════════╗")
	fmt.Println("║   Sliding Window Counter Rate Limiter (Go+Redis) ║")
	fmt.Println("╠══════════════════════════════════════════════════╣")
	fmt.Printf("║  Redis:       %s\n", redisAddr)
	fmt.Printf("║  Rate Limit:  %d requests / %d second(s)\n", rateLimit, windowSec)
	fmt.Printf("║  Port:        %s\n", port)
	fmt.Println("╚══════════════════════════════════════════════════╝")

	rdb := redis.NewClient(&redis.Options{
		Addr:         redisAddr,
		PoolSize:     50,
		MinIdleConns: 10,
		ReadTimeout:  2 * time.Second,
		WriteTimeout: 2 * time.Second,
	})

	ctx := context.Background()

	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Fatalf("Redis connection failed: %v", err)
	}

	limiter := ratelimiter.NewSlidingWindowCounter(
		rdb,
		rateLimit,
		time.Duration(windowSec)*time.Second,
	)

	mux := http.NewServeMux()

	mux.HandleFunc("/api/resource", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":    "success",
			"message":   "Here's ur data",
			"timestamp": time.Now().Format(time.RFC3339Nano),
		})
	})

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"stauts": "ok"})
	})

	handler := ratelimiter.Middleware(limiter)(mux)

	server := &http.Server{
		Addr:         ":" + port,
		Handler:      handler,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	log.Printf("Server starting on :%s", port)
	if err := server.ListenAndServe(); err != nil {
		log.Fatalf("server failed: %v", err)
	}

}
func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}
