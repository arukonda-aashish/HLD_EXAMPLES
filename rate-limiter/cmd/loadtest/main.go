package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

func main() {
	serverURL := getEnv("SERVER_URL", "http://localhost:8080")
	totalRequests := getEnvInt("TOTAL_REQUESTS", 50)
	concurrency := getEnvInt("CONCURRENCY", 20)
	endpoint := getEnv("ENDPOINT", "/api/resource")

	fmt.Println("╔══════════════════════════════════════════════════╗")
	fmt.Println("║          Rate Limiter Load Test                  ║")
	fmt.Println("╠══════════════════════════════════════════════════╣")
	fmt.Printf("║  Target:      %s%s\n", serverURL, endpoint)
	fmt.Printf("║  Requests:    %d total\n", totalRequests)
	fmt.Printf("║  Concurrency: %d goroutines\n", concurrency)
	fmt.Println("╚══════════════════════════════════════════════════╝")
	fmt.Println()

	var allowed atomic.Int64
	var denied atomic.Int64
	var errors atomic.Int64

	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup

	client := &http.Client{
		Timeout: 5 * time.Second,
	}

	type RequestResult struct {
		Index      int
		StatusCode int
		Remaining  string
		Weighted   string
		Duration   time.Duration
		Error      string
	}

	var mu sync.Mutex
	results := make([]RequestResult, totalRequests)

	start := time.Now()

	for i := 0; i < totalRequests; i++ {
		wg.Add(1)
		sem <- struct{}{}

		go func(idx int) {
			defer wg.Done()
			defer func() { <-sem }()

			reqStart := time.Now()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()

			req, err := http.NewRequestWithContext(ctx, "GET", serverURL+endpoint, nil)
			if err != nil {
				mu.Lock()
				results[idx] = RequestResult{Index: idx, Error: err.Error()}
				mu.Unlock()
				errors.Add(1)
				return
			}
			req.Header.Set("X-Forwarded-For", "192.168.1.100")

			resp, err := client.Do(req)
			duration := time.Since(reqStart)

			if err != nil {
				mu.Lock()
				results[idx] = RequestResult{Index: idx, Duration: duration, Error: err.Error()}
				mu.Unlock()
				errors.Add(1)
				return
			}
			io.ReadAll(resp.Body)
			resp.Body.Close()

			r := RequestResult{
				Index:      idx,
				StatusCode: resp.StatusCode,
				Remaining:  resp.Header.Get("X-RateLimit-Remaining"),
				Weighted:   resp.Header.Get("X-RateLimit-Weighted"),
				Duration:   duration,
			}

			if resp.StatusCode == http.StatusOK {
				allowed.Add(1)
			} else if resp.StatusCode == http.StatusTooManyRequests {
				denied.Add(1)
			}

			mu.Lock()
			results[idx] = r
			mu.Unlock()
		}(i)
	}

	wg.Wait()
	elapsed := time.Since(start)

	fmt.Println("┌──────┬────────┬───────────┬──────────┬──────────┐")
	fmt.Println("│  #   │ Status │ Remaining │ Weighted │ Latency  │")
	fmt.Println("├──────┼────────┼───────────┼──────────┼──────────┤")

	for _, r := range results {
		icon := "✅"
		status := fmt.Sprintf("%d", r.StatusCode)

		if r.StatusCode == 429 {
			icon = "🚫"
		} else if r.StatusCode == 0 || r.Error != "" {
			icon = "❌"
			status = "ERR"
		}

		remaining := r.Remaining
		if remaining == "" {
			remaining = "-"
		}
		weighted := r.Weighted
		if weighted == "" {
			weighted = "-"
		}

		latency := float64(r.Duration.Microseconds()) / 1000.0

		fmt.Printf("│ %s %2d │  %s  │  %7s  │  %6s  │ %6.1fms │\n",
			icon, r.Index,
			status,
			remaining,
			weighted,
			latency,
		)
	}

	fmt.Println("└──────┴────────┴───────────┴──────────┴──────────┘")
	fmt.Println()
	fmt.Println("═══════════════════ SUMMARY ═══════════════════════")
	fmt.Printf("  Total Requests:  %d\n", totalRequests)
	fmt.Printf("  ✅ Allowed (200): %d\n", allowed.Load())
	fmt.Printf("  🚫 Denied (429):  %d\n", denied.Load())
	fmt.Printf("  ❌ Errors:        %d\n", errors.Load())
	fmt.Printf("  Total Time:      %v\n", elapsed.Round(time.Millisecond))
	fmt.Printf("  Throughput:      %.0f req/s\n", float64(totalRequests)/elapsed.Seconds())
	fmt.Println("═══════════════════════════════════════════════════")

	fmt.Println()
	if denied.Load() > 0 {
		fmt.Println("✅ PASS: Rate limiter correctly rejected excess requests")
	} else {
		fmt.Println("⚠️  WARNING: No requests were denied — rate limit may be too high")
	}
	if errors.Load() == 0 {
		fmt.Println("✅ PASS: No errors — all requests handled cleanly")
	}

	fmt.Println()
	summary := map[string]interface{}{
		"total":      totalRequests,
		"allowed":    allowed.Load(),
		"denied":     denied.Load(),
		"errors":     errors.Load(),
		"elapsed_ms": elapsed.Milliseconds(),
		"rps":        float64(totalRequests) / elapsed.Seconds(),
	}
	b, _ := json.MarshalIndent(summary, "", "  ")
	fmt.Println("JSON Summary:")
	fmt.Println(string(b))
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
