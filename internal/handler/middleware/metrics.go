package middleware

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	httpRequests = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "vcard",
		Subsystem: "http",
		Name:      "requests_total",
		Help:      "Handled requests by operation and response code.",
	}, []string{"operation", "code"})

	httpDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "vcard",
		Subsystem: "http",
		Name:      "request_duration_seconds",
		Help:      "Time from receiving a request to the end of the handler.",
		Buckets:   prometheus.DefBuckets,
	}, []string{"operation"})

	httpInFlight = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: "vcard",
		Subsystem: "http",
		Name:      "requests_in_flight",
		Help:      "Requests being handled right now.",
	})
)

// Metrics считает запросы, их длительность и коды ответов по операциям.
//
// Стоит снаружи Recover: паника в обработчике превращается там в 500,
// и эта пятисотка должна попасть в метрики, а не пропасть вместе
// с раскруткой стека. Отказы ограничителя и 304 учитываются так же —
// с тем кодом, который ушёл клиенту.
func Metrics() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			httpInFlight.Inc()
			defer httpInFlight.Dec()

			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r)

			op := OperationFromContext(r.Context())
			httpRequests.WithLabelValues(op, strconv.Itoa(rec.status)).Inc()
			httpDuration.WithLabelValues(op).Observe(time.Since(start).Seconds())
		})
	}
}
