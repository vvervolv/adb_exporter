package http

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/vvervolv/adb_exporter/internal/metrics"
)

type fakeHealth struct {
	ready   bool
	healthy bool
}

func (f *fakeHealth) Ready() bool   { return f.ready }
func (f *fakeHealth) Healthy() bool { return f.healthy }

func doGet(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestHealthEndpoint(t *testing.T) {
	fh := &fakeHealth{}
	srv := NewServer(":0", prometheus.NewRegistry(), fh)

	if rec := doGet(t, srv.Handler, "/health"); rec.Code != http.StatusInternalServerError {
		t.Errorf("unhealthy /health = %d, want 500", rec.Code)
	}

	fh.healthy = true
	if rec := doGet(t, srv.Handler, "/health"); rec.Code != http.StatusOK {
		t.Errorf("healthy /health = %d, want 200", rec.Code)
	}
}

func TestReadyEndpoint(t *testing.T) {
	fh := &fakeHealth{}
	srv := NewServer(":0", prometheus.NewRegistry(), fh)

	if rec := doGet(t, srv.Handler, "/ready"); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("not-ready /ready = %d, want 503", rec.Code)
	}

	fh.ready = true
	if rec := doGet(t, srv.Handler, "/ready"); rec.Code != http.StatusOK {
		t.Errorf("ready /ready = %d, want 200", rec.Code)
	}
}

func TestMetricsEndpoint(t *testing.T) {
	reg := metrics.New()
	srv := NewServer(":0", reg.Gatherer(), &fakeHealth{})

	rec := doGet(t, srv.Handler, "/metrics")
	if rec.Code != http.StatusOK {
		t.Fatalf("/metrics = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "android_farm_") {
		t.Errorf("/metrics body missing android_farm_ metrics:\n%s", rec.Body.String())
	}
}
