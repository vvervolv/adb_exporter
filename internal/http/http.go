// Package http serves the exporter endpoints. Handlers only read pre-computed
// state (metrics registry, poller health) and NEVER invoke adb (SPEC §HTTP).
package http

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Health exposes the poller's liveness/readiness state.
type Health interface {
	// Ready reports whether the first successful poll has completed.
	Ready() bool
	// Healthy reports whether the last successful poll is recent enough.
	Healthy() bool
}

// NewServer wires the handlers onto a mux and returns an *http.Server.
func NewServer(addr string, gatherer prometheus.Gatherer, health Health) *http.Server {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(gatherer, promhttp.HandlerOpts{}))
	mux.HandleFunc("/health", healthHandler(health))
	mux.HandleFunc("/ready", readyHandler(health))
	return &http.Server{Addr: addr, Handler: mux}
}

func healthHandler(h Health) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if h.Healthy() {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok\n"))
			return
		}
		http.Error(w, "unhealthy: no recent successful poll", http.StatusInternalServerError)
	}
}

func readyHandler(h Health) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if h.Ready() {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ready\n"))
			return
		}
		http.Error(w, "not ready: no successful poll yet", http.StatusServiceUnavailable)
	}
}
