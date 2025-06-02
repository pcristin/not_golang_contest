// cmd/megaload/main.go
package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

type Metrics struct {
	// Request counts
	requestsSent      int64
	requestsCompleted int64

	// Response categories
	success201      int64 // Created (got checkout code)
	clientErrors4xx int64 // 400-499 (bad request, sold out, etc)
	serverErrors5xx int64 // 500+ (server failures)
	networkErrors   int64 // Timeouts, connection refused, etc

	// Specific errors we care about
	soldOut409    int64 // Stock sold out
	userLimit429  int64 // User hit 10 item limit
	badRequest400 int64 // Missing parameters, etc
}

func (m *Metrics) recordResponse(statusCode int) {
	atomic.AddInt64(&m.requestsCompleted, 1)

	switch statusCode {
	case 201:
		atomic.AddInt64(&m.success201, 1)
	case 400:
		atomic.AddInt64(&m.badRequest400, 1)
		atomic.AddInt64(&m.clientErrors4xx, 1)
	case 409:
		atomic.AddInt64(&m.soldOut409, 1)
		atomic.AddInt64(&m.clientErrors4xx, 1)
	case 429:
		atomic.AddInt64(&m.userLimit429, 1)
		atomic.AddInt64(&m.clientErrors4xx, 1)
	default:
		if statusCode >= 500 {
			atomic.AddInt64(&m.serverErrors5xx, 1)
		} else if statusCode >= 400 {
			atomic.AddInt64(&m.clientErrors4xx, 1)
		}
	}
}

func (m *Metrics) recordNetworkError() {
	atomic.AddInt64(&m.requestsCompleted, 1)
	atomic.AddInt64(&m.networkErrors, 1)
}

func (m *Metrics) printProgress(userNum int, totalUsers int) {
	sent := atomic.LoadInt64(&m.requestsSent)
	completed := atomic.LoadInt64(&m.requestsCompleted)
	success := atomic.LoadInt64(&m.success201)
	inFlight := sent - completed

	fmt.Printf("Progress: %d/%d | Sent: %d | Completed: %d | In-flight: %d | Success: %d\n",
		userNum, totalUsers, sent, completed, inFlight, success)
}

func (m *Metrics) printFinal(duration time.Duration) {
	sent := atomic.LoadInt64(&m.requestsSent)
	completed := atomic.LoadInt64(&m.requestsCompleted)

	fmt.Printf("\n=== FINAL RESULTS ===\n")
	fmt.Printf("Duration: %v\n", duration)
	fmt.Printf("Requests sent: %d\n", sent)
	fmt.Printf("Requests completed: %d (%.2f%%)\n", completed, float64(completed)/float64(sent)*100)
	fmt.Printf("Requests lost: %d\n", sent-completed)

	fmt.Printf("\n--- Success ---\n")
	fmt.Printf("201 Created (got code): %d\n", atomic.LoadInt64(&m.success201))

	fmt.Printf("\n--- Expected Rejections (4xx) ---\n")
	fmt.Printf("409 Conflict (sold out): %d\n", atomic.LoadInt64(&m.soldOut409))
	fmt.Printf("429 Too Many (user limit): %d\n", atomic.LoadInt64(&m.userLimit429))
	fmt.Printf("400 Bad Request: %d\n", atomic.LoadInt64(&m.badRequest400))
	fmt.Printf("Other 4xx errors: %d\n",
		atomic.LoadInt64(&m.clientErrors4xx)-
			atomic.LoadInt64(&m.soldOut409)-
			atomic.LoadInt64(&m.userLimit429)-
			atomic.LoadInt64(&m.badRequest400))

	fmt.Printf("\n--- Server Issues ---\n")
	fmt.Printf("5xx Server Errors: %d\n", atomic.LoadInt64(&m.serverErrors5xx))
	fmt.Printf("Network Errors: %d\n", atomic.LoadInt64(&m.networkErrors))

	fmt.Printf("\n--- Performance ---\n")
	fmt.Printf("Overall rate: %.2f req/s\n", float64(sent)/duration.Seconds())
	fmt.Printf("Completed rate: %.2f req/s\n", float64(completed)/duration.Seconds())
	fmt.Printf("Success rate: %.2f req/s\n", float64(atomic.LoadInt64(&m.success201))/duration.Seconds())
}

func main() {
	var (
		totalUsers = 1000000
		concurrent = 2000 // Increased concurrency
		metrics    Metrics
	)

	// More aggressive HTTP client settings
	client := &http.Client{
		Timeout: 30 * time.Second, // Increased timeout
		Transport: &http.Transport{
			MaxIdleConns:        concurrent * 2,
			MaxIdleConnsPerHost: concurrent,
			MaxConnsPerHost:     concurrent,
			IdleConnTimeout:     90 * time.Second,
		},
	}

	fmt.Printf("Starting load test: %d users, %d concurrent\n", totalUsers, concurrent)
	start := time.Now()

	var wg sync.WaitGroup
	sem := make(chan struct{}, concurrent)

	// Progress printer goroutine
	progressDone := make(chan bool)
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				metrics.printProgress(int(atomic.LoadInt64(&metrics.requestsSent)), totalUsers)
			case <-progressDone:
				return
			}
		}
	}()

	// Send requests
	for i := 0; i < totalUsers; i++ {
		wg.Add(1)
		sem <- struct{}{}
		atomic.AddInt64(&metrics.requestsSent, 1)

		go func(userNum int) {
			defer wg.Done()
			defer func() { <-sem }()

			userID := fmt.Sprintf("mega_user_%d", userNum)
			url := fmt.Sprintf("http://localhost:8080/checkout?user_id=%s&id=%d", userID, userNum%100000+1)

			resp, err := client.Post(url, "", nil)
			if err != nil {
				metrics.recordNetworkError()
				return
			}
			defer resp.Body.Close()

			// Read response body for debugging
			var result map[string]interface{}
			json.NewDecoder(resp.Body).Decode(&result)

			metrics.recordResponse(resp.StatusCode)
		}(i)

		// Remove artificial delays - let the semaphore handle concurrency
	}

	wg.Wait()
	close(progressDone)
	duration := time.Since(start)

	metrics.printFinal(duration)

	// Additional insights
	fmt.Printf("\n=== INSIGHTS ===\n")
	if metrics.serverErrors5xx > 0 {
		fmt.Printf("⚠️  Server errors detected! The server struggled under load.\n")
	}
	if metrics.networkErrors > int64(float64(metrics.requestsSent)*0.01) {
		fmt.Printf("⚠️  High network error rate (>1%%). Server might be dropping connections.\n")
	}
	if metrics.success201 < 10000 && metrics.soldOut409 == 0 {
		fmt.Printf("⚠️  Less than 10k items sold but no 'sold out' responses. Possible issue with stock tracking.\n")
	}

	lostRequests := metrics.requestsSent - metrics.requestsCompleted
	if lostRequests > 0 {
		fmt.Printf("⚠️  %d requests never completed. Possible timeout or connection issues.\n", lostRequests)
	}
}
