// Command checkout-fixture is a disposable sandbox workload for the primary
// memory-limit scenario. It contains no production data or credentials.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"
)

const defaultAllocationMiB = 384

type fixture struct {
	once         sync.Once
	held         []byte
	mu           sync.Mutex
	requestCount int
	durationSum  float64
	buckets      [6]int
}

var bounds = [...]float64{0.01, 0.025, 0.05, 0.1, 0.25, 0.5}

func (f *fixture) checkout(allocationMiB int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		if r.Method != http.MethodGet && r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		f.once.Do(func() {
			// Touch each page and retain it so the container's memory limit,
			// rather than virtual allocation alone, determines the outcome.
			f.held = make([]byte, allocationMiB*1024*1024)
			for i := 0; i < len(f.held); i += 4096 {
				f.held[i] = 1
			}
		})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"checkout":"ok"}`))
		elapsed := time.Since(started).Seconds()
		f.mu.Lock()
		f.requestCount++
		f.durationSum += elapsed
		for i, bound := range bounds {
			if elapsed <= bound {
				f.buckets[i]++
			}
		}
		f.mu.Unlock()
	}
}

func (f *fixture) metrics(w http.ResponseWriter, _ *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	_, _ = fmt.Fprintf(w, "# TYPE checkout_requests_total counter\ncheckout_requests_total{status=\"200\"} %d\n", f.requestCount)
	_, _ = fmt.Fprintln(w, "# TYPE checkout_request_duration_seconds histogram")
	for i, bound := range bounds {
		_, _ = fmt.Fprintf(w, "checkout_request_duration_seconds_bucket{le=\"%g\"} %d\n", bound, f.buckets[i])
	}
	_, _ = fmt.Fprintf(w, "checkout_request_duration_seconds_bucket{le=\"+Inf\"} %d\ncheckout_request_duration_seconds_sum %g\ncheckout_request_duration_seconds_count %d\n", f.requestCount, f.durationSum, f.requestCount)
}

func main() {
	allocationMiB := defaultAllocationMiB
	if value := os.Getenv("CHECKOUT_FIXTURE_ALLOCATION_MIB"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 300 || parsed > 768 {
			slog.Error("invalid CHECKOUT_FIXTURE_ALLOCATION_MIB")
			os.Exit(1)
		}
		allocationMiB = parsed
	}
	f := &fixture{}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mux.HandleFunc("/checkout", f.checkout(allocationMiB))
	mux.HandleFunc("/metrics", f.metrics)
	server := &http.Server{Addr: ":8080", Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		slog.Error("fixture server failed", "error", err)
		os.Exit(1)
	}
}
