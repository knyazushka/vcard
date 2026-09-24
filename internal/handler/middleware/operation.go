package middleware

import (
	"context"
	"net/http"
)

// Имена для запросов, у которых нет операции в спеке.
const (
	// OperationFiles — раздача загруженных файлов.
	OperationFiles = "files"
	// OperationPreflight — preflight-запрос браузера перед CORS-запросом.
	OperationPreflight = "preflight"
	// OperationUnknown — путь, которого нет ни в спеке, ни среди служебных:
	// чаще всего сканеры, перебирающие /wp-admin и .env.
	OperationUnknown = "unknown"
)

type operationKey struct{}

// Operation определяет, к какой операции из спеки относится запрос,
// и кладёт её имя в контекст.
//
// По имени операции ограничитель выбирает лимит, а метрики — метку.
// Путь в метку не годится: в нём UUID, слаги и токены приглашений,
// и число временных рядов росло бы с каждым профилем.
func Operation(resolve func(*http.Request) string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := context.WithValue(r.Context(), operationKey{}, resolve(r))
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// OperationFromContext возвращает имя операции; OperationUnknown, если
// оно не определялось.
func OperationFromContext(ctx context.Context) string {
	if op, ok := ctx.Value(operationKey{}).(string); ok && op != "" {
		return op
	}
	return OperationUnknown
}
