// Package observability собирает служебный обработчик: пробы и метрики.
//
// Он висит на отдельном порту и в OpenAPI-спеке не описан: это контракт
// со средой исполнения (Kubernetes, Prometheus), а не с фронтом, и наружу
// его публиковать не нужно.
package observability

import (
	"context"
	"net/http"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Readiness сообщает, готов ли процесс обслуживать запросы.
type Readiness interface {
	Ping(ctx context.Context) error
}

// NewAdminHandler разводит два зонда, которые часто путают:
//
//   - /healthz — процесс жив. Не ходит в базу: если сюда добавить проверку
//     зависимостей, кратковременная недоступность Postgres приведёт к тому,
//     что Kubernetes перезапустит совершенно здоровые поды и сделает хуже.
//   - /readyz  — процесс может обслуживать запросы прямо сейчас. Вот здесь
//     проверка базы уместна: под просто выводится из балансировки
//     до восстановления.
func NewAdminHandler(db Readiness) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		if err := db.Ping(r.Context()); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("database unavailable"))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	mux.Handle("GET /metrics", promhttp.Handler())

	return mux
}
