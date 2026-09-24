package middleware

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestMetricsCountsByOperationAndCode(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	status := http.StatusTooManyRequests
	h := Chain(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			if status == http.StatusInternalServerError {
				panic("boom")
			}
			w.WriteHeader(status)
		}),
		Operation(func(*http.Request) string { return "metricsTestOp" }),
		Metrics(),
		Recover(log),
	)

	serve := func() {
		r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/x", nil)
		h.ServeHTTP(httptest.NewRecorder(), r)
	}

	serve()
	if got := testutil.ToFloat64(httpRequests.WithLabelValues("metricsTestOp", "429")); got != 1 {
		t.Errorf("429: счётчик %v, ждали 1", got)
	}

	// Паника превращается в 500 внутри Recover, и метрики должны
	// увидеть именно этот код, а не потерять запрос.
	status = http.StatusInternalServerError
	serve()
	if got := testutil.ToFloat64(httpRequests.WithLabelValues("metricsTestOp", "500")); got != 1 {
		t.Errorf("500 от паники: счётчик %v, ждали 1", got)
	}

	if got := testutil.ToFloat64(httpInFlight); got != 0 {
		t.Errorf("запросов в работе после завершения: %v", got)
	}
}
