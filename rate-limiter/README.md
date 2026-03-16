# Sliding Window Counter Rate Limiter (Go + Redis)

A production-grade HTTP rate limiter using the **sliding window counter** algorithm, implemented in Go with Redis as the distributed state store.

## Architecture

```
                    ┌─────────────────────────────────────────────┐
                    │              HTTP Server (:8080)             │
                    │                                             │
   Client ──────►  │  ┌─────────────────────────────────────┐    │
   Request         │  │     Rate Limiter Middleware          │    │
                   │  │                                     │    │
                   │  │  1. Extract client IP                │    │
                   │  │  2. Call Allow(ctx, clientIP)        │    │
                   │  │  3. If allowed → next handler        │    │
                   │  │     If denied  → 429 + Retry-After   │    │
                   │  └──────────────┬──────────────────────┘    │
                   │                 │                            │
                   │                 │  Lua script (atomic)       │
                   │                 ▼                            │
                   │          ┌─────────────┐                    │
                   │          │    Redis     │                    │
                   │          │             │                    │
                   │          │  prev_key: 7│  ← previous window │
                   │          │  curr_key: 3│  ← current window  │
                   │          └─────────────┘                    │
                   │                                             │
                   │  ┌─────────────────────────────────────┐    │
                   │  │     /api/resource Handler            │    │
                   │  │     (your actual business logic)     │    │
                   │  └─────────────────────────────────────┘    │
                   └─────────────────────────────────────────────┘
```

## How the Sliding Window Counter Works

### The Problem with Fixed Windows

A fixed window counter (e.g., "10 req/sec") resets at exact boundaries. A client can send 10 requests at 0.9s and 10 more at 1.1s — that's 20 requests in 0.2 seconds, double the intended rate.

### The Sliding Window Solution

Instead of a hard reset, we **blend** the previous and current windows:

```
Time ──────────────────────────────────────────►

    |◄── Previous Window ──►|◄── Current Window ──►|
    |   count_prev = 7      |   count_curr = 3     |
    |                       |   ▲                   |
    |                       |   │ we are here       |
    |                       |   │ (300ms in)        |

    overlap = (1000 - 300) / 1000 = 0.7

    weighted_count = (7 × 0.7) + 3 = 7.9

    If limit = 10 → 7.9 < 10 → ALLOWED
```

This eliminates the boundary spike problem while using only **O(1) storage per client per window** (vs. O(n) for sliding window log which stores every timestamp).

### Why Redis + Lua?

The check-and-increment must be **atomic**. Without atomicity:

```
Thread A: reads count = 9  (limit = 10)
Thread B: reads count = 9  (limit = 10)
Thread A: "9 < 10, allowed!" → increments to 10
Thread B: "9 < 10, allowed!" → increments to 11  ← RACE CONDITION
```

Redis executes Lua scripts atomically — no interleaving. This handles concurrency across **multiple server instances** in a distributed deployment.

## Project Structure

```
rate-limiter/
├── ratelimiter/
│   ├── limiter.go        # Core algorithm (Lua script + Allow method)
│   └── middleware.go     # HTTP middleware (extracts IP, sets headers)
├── cmd/
│   ├── server/main.go    # HTTP server with rate-limited endpoint
│   └── loadtest/main.go  # Concurrent load test simulator
├── docker-compose.yml    # Redis + server
├── Dockerfile
└── README.md
```

## Quick Start

### Option 1: Docker Compose (easiest)

```bash
docker compose up -d
# Server is at http://localhost:8080

# Run the load test
go run ./cmd/loadtest
```

### Option 2: Local (requires Redis running)

```bash
# Terminal 1: Start Redis
redis-server

# Terminal 2: Start the server
go run ./cmd/server

# Terminal 3: Run load test
go run ./cmd/loadtest
```

### Manual Testing with curl

```bash
# Send a single request
curl -i http://localhost:8080/api/resource

# Rapid-fire 15 requests (with limit=10, some will get 429)
for i in $(seq 1 15); do
  echo "Request $i: $(curl -s -o /dev/null -w '%{http_code}' http://localhost:8080/api/resource)"
done
```

## Configuration

| Env Variable      | Default        | Description                    |
|-------------------|----------------|--------------------------------|
| `REDIS_ADDR`      | localhost:6379 | Redis connection address       |
| `RATE_LIMIT`      | 10             | Max requests per window        |
| `WINDOW_SIZE_SEC` | 1              | Window duration in seconds     |
| `PORT`            | 8080           | HTTP server port               |

Load test config:

| Env Variable      | Default                        | Description              |
|-------------------|--------------------------------|--------------------------|
| `SERVER_URL`      | http://localhost:8080           | Target server            |
| `TOTAL_REQUESTS`  | 50                             | Total requests to fire   |
| `CONCURRENCY`     | 20                             | Simultaneous goroutines  |

## Response Headers

Every response includes:

```
X-RateLimit-Limit: 10          # max requests per window
X-RateLimit-Remaining: 7       # estimated remaining quota
X-RateLimit-Weighted: 3.40     # current weighted count
```

On 429 responses, additionally:

```
Retry-After: 0.7               # seconds until window resets
```

## HLD Interview Talking Points

This implementation maps directly to common system design questions:

1. **Why sliding window counter over other algorithms?**
   - Fixed window: boundary spike problem
   - Sliding window log: O(n) memory per client (stores every timestamp)
   - Token bucket: great for burst tolerance but harder to reason about
   - Sliding window counter: O(1) memory, smooth rate enforcement, simple mental model

2. **How does it handle distributed concurrency?**
   - Redis Lua scripts execute atomically (no MULTI/EXEC needed)
   - Single Redis instance = single source of truth
   - go-redis client is goroutine-safe with built-in connection pooling

3. **What happens if Redis goes down?**
   - Design choice: fail open (allow all) vs fail closed (deny all)
   - This implementation fails open — see middleware.go comments

4. **How would you scale this?**
   - Redis Cluster for sharding by key
   - Read replicas won't help (need atomic read-write)
   - Local in-memory cache + periodic sync for approximate limiting at extreme scale