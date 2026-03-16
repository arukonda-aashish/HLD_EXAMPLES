package ratelimiter

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

func Middleware(limiter *SlidingWindowCounter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

			clientIP := extractClientIP(r)

			result, err := limiter.Allow(r.Context(), clientIP)

			if err != nil {
				http.Error(w, "Interna Server Error", http.StatusInternalServerError)
				return
			}

			w.Header().Set("X-RateLimit-Limit", strconv.Itoa(result.Limit))
			w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(result.Remaining))
			w.Header().Set("X-RateLimit-Weighted", fmt.Sprintf("%.2f", result.WeightedCount))

			if !result.Allowed {
				retryAfterSec := float64(result.RetryAfterMs) / 1000.0
				w.Header().Set("Retry-After", fmt.Sprintf("%.1f", retryAfterSec))
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusTooManyRequests)

				json.NewEncoder(w).Encode(map[string]interface{}{
					"error":          "rate limit exceeded",
					"limit":          result.Limit,
					"weighted_count": result.WeightedCount,
					"retry_after_ms": result.RetryAfterMs,
				})
				return

			}
			next.ServeHTTP(w, r)
		})
	}
}

func extractClientIP(r *http.Request) string {

	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		return strings.TrimSpace(parts[0])
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return xri
	}
	ip := r.RemoteAddr
	if idx := strings.LastIndex(ip, ":"); idx != -1 {
		ip = ip[:idx]
	}
	return ip
}
