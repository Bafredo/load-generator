package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/time/rate"
)

// Stats to track requests
type Stats struct {
	totalRequests int64
	successCount  int64
	errorCount    int64
	timeoutErrors int64
	networkErrors int64
}

// HTTPClient wraps a configured HTTP client with shared transport
type HTTPClient struct {
	client  *http.Client
	limiter *rate.Limiter
}

// NewHTTPClient creates a properly configured HTTP client for high concurrency
func NewHTTPClient(requestsPerSecond int) *HTTPClient {
	// Configure transport for high concurrency
	transport := &http.Transport{
		MaxIdleConns:        500,              // Increased from default 100
		MaxIdleConnsPerHost: 100,              // Increased from default 2
		MaxConnsPerHost:     200,              // Limit concurrent connections per host
		IdleConnTimeout:     90 * time.Second, // How long idle connections stay open
		
		// Explicit timeout settings for better diagnostics
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 15 * time.Second, // Time to wait for response headers
		ExpectContinueTimeout: 1 * time.Second,
		
		// Connection settings
		DisableKeepAlives:     false, // Enable connection reuse
		DisableCompression:    false,
		ForceAttemptHTTP2:     true,
	}

	client := &http.Client{
		Transport: transport,
		Timeout:   30 * time.Second, // Overall request timeout
	}

	// Rate limiter to prevent resource exhaustion
	limiter := rate.NewLimiter(rate.Limit(requestsPerSecond), requestsPerSecond/10) // burst = rps/10

	return &HTTPClient{
		client:  client,
		limiter: limiter,
	}
}

// makeRequest performs an HTTP GET request with proper context and rate limiting
func (hc *HTTPClient) makeRequest(ctx context.Context, url string, stats *Stats, requestID int64, hostname string) {
	// Wait for rate limiter
	if err := hc.limiter.Wait(ctx); err != nil {
		atomic.AddInt64(&stats.errorCount, 1)
		return // Context cancelled
	}

	// Create request with context for proper cancellation
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		atomic.AddInt64(&stats.errorCount, 1)
		fmt.Printf("[%s] Request %d: Failed to create request - %v\n", hostname, requestID, err)
		return
	}

	// Add user agent to identify our load test
	req.Header.Set("User-Agent", fmt.Sprintf("LoadGenerator/%s", hostname))

	start := time.Now()
	resp, err := hc.client.Do(req)
	duration := time.Since(start)

	atomic.AddInt64(&stats.totalRequests, 1)

	if err != nil {
		atomic.AddInt64(&stats.errorCount, 1)
		
		// Categorize error types for better diagnostics
		if ctx.Err() == context.DeadlineExceeded {
			atomic.AddInt64(&stats.timeoutErrors, 1)
		} else {
			atomic.AddInt64(&stats.networkErrors, 1)
		}
		
		// Only log errors occasionally to prevent spam
		if requestID%50 == 0 {
			fmt.Printf("[%s] Request %d: Error - %v (took %v)\n", hostname, requestID, err, duration)
		}
		return
	}
	defer resp.Body.Close()

	atomic.AddInt64(&stats.successCount, 1)
	
	// Only log successful requests occasionally to reduce noise
	if requestID%200 == 0 {
		fmt.Printf("[%s] Request %d: Status %d - %s (took %v)\n", 
			hostname, requestID, resp.StatusCode, resp.Status, duration)
	}
}

// worker continuously makes requests until context is cancelled
func worker(ctx context.Context, url string, hc *HTTPClient, stats *Stats, workerID int, requestCounter *int64, delay time.Duration, hostname string) {
	for {
		select {
		case <-ctx.Done():
			fmt.Printf("[%s] Worker %d stopping...\n", hostname, workerID)
			return
		default:
			requestID := atomic.AddInt64(requestCounter, 1)
			hc.makeRequest(ctx, url, stats, requestID, hostname)
			
			// Sleep to prevent tight spinning
			if delay > 0 {
				select {
				case <-ctx.Done():
					return
				case <-time.After(delay):
				}
			}
		}
	}
}

// printStats periodically prints statistics
func printStats(ctx context.Context, stats *Stats, interval time.Duration, hostname string) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	var lastTotal int64
	lastTime := time.Now()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			now := time.Now()
			total := atomic.LoadInt64(&stats.totalRequests)
			success := atomic.LoadInt64(&stats.successCount)
			errors := atomic.LoadInt64(&stats.errorCount)
			timeouts := atomic.LoadInt64(&stats.timeoutErrors)
			networkErrs := atomic.LoadInt64(&stats.networkErrors)
			
			// Calculate requests per second
			deltaRequests := total - lastTotal
			deltaTime := now.Sub(lastTime).Seconds()
			rps := float64(deltaRequests) / deltaTime
			
			fmt.Printf("\n[%s] === STATS ===\n", hostname)
			fmt.Printf("[%s] Total Requests: %d\n", hostname, total)
			fmt.Printf("[%s] Successful: %d\n", hostname, success)
			fmt.Printf("[%s] Errors: %d (Timeouts: %d, Network: %d)\n", hostname, errors, timeouts, networkErrs)
			fmt.Printf("[%s] RPS: %.1f\n", hostname, rps)
			if total > 0 {
				successRate := float64(success) / float64(total) * 100
				fmt.Printf("[%s] Success Rate: %.2f%%\n", hostname, successRate)
			}
			fmt.Printf("[%s] =============\n\n", hostname)
			
			lastTotal = total
			lastTime = now
		}
	}
}

// Helper functions for environment variables
func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intVal, err := strconv.Atoi(value); err == nil {
			return intVal
		}
	}
	return defaultValue
}

func getEnvDuration(key string, defaultValue time.Duration) time.Duration {
	if value := os.Getenv(key); value != "" {
		if duration, err := time.ParseDuration(value); err == nil {
			return duration
		}
	}
	return defaultValue
}

func main() {
	// Get configuration from environment variables or use defaults
	domain := getEnv("TARGET_URL", "https://pinetsawp.net/piswap.html")
	numWorkers := getEnvInt("NUM_WORKERS", 400)
	requestDelay := getEnvDuration("REQUEST_DELAY", 10*time.Millisecond)
	statsInterval := getEnvDuration("STATS_INTERVAL", 5*time.Second)
	maxRPS := getEnvInt("MAX_RPS", 1000) // Rate limit: requests per second
	
	// Get hostname for logging
	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "unknown"
	}

	fmt.Printf("[%s] Starting %d concurrent workers making continuous requests to: %s\n", hostname, numWorkers, domain)
	fmt.Printf("[%s] Request delay: %v, Stats interval: %v, Max RPS: %d\n", hostname, requestDelay, statsInterval, maxRPS)
	fmt.Println("Press Ctrl+C to stop gracefully...")
	fmt.Println("Stats will be printed every interval.\n")

	// Create context for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	
	// Handle interrupt signals for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	
	var wg sync.WaitGroup
	stats := &Stats{}
	var requestCounter int64

	// Create shared HTTP client with proper configuration
	httpClient := NewHTTPClient(maxRPS)

	// Start stats printer
	go printStats(ctx, stats, statsInterval, hostname)

	// Start all worker goroutines
	for i := 1; i <= numWorkers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			worker(ctx, domain, httpClient, stats, workerID, &requestCounter, requestDelay, hostname)
		}(i)
	}

	// Wait for interrupt signal
	<-sigChan
	fmt.Println("\nShutdown signal received. Stopping all workers...")
	
	// Cancel context to stop all workers
	cancel()
	
	// Wait for all workers to finish
	wg.Wait()
	
	// Print final stats
	total := atomic.LoadInt64(&stats.totalRequests)
	success := atomic.LoadInt64(&stats.successCount)
	errors := atomic.LoadInt64(&stats.errorCount)
	timeouts := atomic.LoadInt64(&stats.timeoutErrors)
	networkErrs := atomic.LoadInt64(&stats.networkErrors)
	
	fmt.Printf("\n[%s] === FINAL STATS ===\n", hostname)
	fmt.Printf("[%s] Total Requests: %d\n", hostname, total)
	fmt.Printf("[%s] Successful: %d\n", hostname, success)
	fmt.Printf("[%s] Errors: %d (Timeouts: %d, Network: %d)\n", hostname, errors, timeouts, networkErrs)
	if total > 0 {
		successRate := float64(success) / float64(total) * 100
		fmt.Printf("[%s] Success Rate: %.2f%%\n", hostname, successRate)
	}
	fmt.Printf("[%s] ==================\n", hostname)
	
	fmt.Println("All workers stopped. Goodbye!")
}