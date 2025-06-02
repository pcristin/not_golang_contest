# ⚡ Flash Sale System

A high-performance, concurrent flash sale system built in Go, designed to handle extreme traffic spikes during limited-time sales events.

## 🏗️ Architecture & Design Principles

### Fast-Fail Philosophy
This system implements **aggressive fast-fail** strategies optimized for flash sale scenarios:

- **Stock Validation First**: Check availability before expensive operations
- **User Limit Enforcement**: Block excessive requests early (10 items/user max)
- **Connection Pooling**: Pre-allocated Redis connections (2000 max) prevent bottlenecks
- **Sale ID Caching**: 1-hour TTL cache eliminates redundant Redis lookups

### Core Process Flow
```
1. Request → Query Validation → Stock Check (Fast-Fail)
2. User Limit Check → Decrement Stock (Atomic)
3. Generate Checkout Code → Store in Redis (15min TTL)
4. Return Success/Failure Response
```

### Tech Stack Justification
4 core libraries:
| Dependency | Purpose | Size Impact |
|------------|---------|-------------|
| **Redis** | Atomic operations, session storage | Essential for concurrency |
| **PostgreSQL** | Sale metadata, user history | Business logic persistence |
| **Chi Router** | HTTP routing, middleware | Lightweight framework (~1MB) |
| **Redigo** | Redis connection pooling | Mature, stable, lightweight client |
| **lib/pq** | PostgreSQL driver | Raw SQL performance |

**Binary Size**: ~9.3MB (lean, no ORM overhead) - can be optimized with build flags

## 🧪 Testing Methodology

### Test Suite 1: Go Load Test (`cmd/megaload/main.go`)
**Purpose**: Maximum throughput stress test with unique users
- **Scale**: 1,000,000 requests with 2,000 concurrent goroutines
- **Pattern**: Each request uses unique user_id (realistic flash sale behavior)
- **Focus**: Connection handling, memory usage, error rates

### Test Suite 2: k6 Scenarios (`loadtest.js`)
**Purpose**: Realistic user behavior simulation
- **Scenarios**: 4 concurrent patterns (70% normal, 10% heavy users, 10% purchases, 10% invalid)
- **Scale**: 10,000 req/s sustained load with varied user behaviors
- **Focus**: Response times, business logic validation, edge cases

## 📊 Performance Results

### Go Load Test Results
```
Duration: 1m 12s
Requests: 1,000,000 (100% completion rate)
Throughput: 13,842 req/s
Success Rate: 10,000 items sold (100% stock depleted)
Error Rate: 0% server errors, 0% network timeouts
Expected Rejections: 990,000 "sold out" responses (correct behavior)
```

### k6 Multi-Scenario Results
```
Duration: 2m 20s
Throughput: 6,903 req/s sustained
Stock Management: 10,000/10,000 items sold (100% efficiency)
Response Times: P95: 464ms | P99: 1.22s
Business Logic: 838,732 proper "sold out" responses
Error Handling: 100% correct status codes (409, 429, 400)
```

## 🚀 Optimization Opportunities

**Current system handles 13k+ req/s on single instance.** Further optimizations available but avoided for clarity:

### Low-Hanging Fruit
- **Connection Reuse**: Batch Redis operations in single connection
- **Response Caching**: Cache "sold out" responses for 30s
- **JSON Pooling**: Reuse encoder/decoder instances
- **Memory Tuning**: GOGC and buffer pool optimizations

### Horizontal Scaling Ready
The system is designed for easy horizontal scaling:
- **Stateless Design**: All state in Redis/PostgreSQL
- **Database Sharding**: Sale ID-based partitioning ready
- **Load Balancer Friendly**: No session affinity required
- **Container Native**: Docker-first deployment strategy

## 🏃‍♂️ Quick Start

```bash
# Start infrastructure
make up

# Run local load test
go run cmd/megaload/main.go

# Run k6 scenarios
k6 run loadtest.js

# Monitor performance
docker-compose logs app | grep "items sold"
```

## 🎯 Production Deployment

Live demo: https://not-golang-contest.onrender.com

**Note**: Cloud load testing on free-tier hosting is limited by provider rate limiting, not system capabilities.

## 🔍 Technical Deep Dive

For architecture decisions, database schema, and Redis key patterns, explore the codebase structure:
- `internal/api/` - Handler implementations with business logic
- `internal/database/` - Optimized Redis client with connection pooling
- `cmd/server/` - Application bootstrap and dependency injection

## 🥚 Easter Egg

*Hint: Check what happens when you ***/purchase*** that one 1% lottery ticket*

---

**Built for scale. Tested under fire. Ready for Black Friday.** 🛒 