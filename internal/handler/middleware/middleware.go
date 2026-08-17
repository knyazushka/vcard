// Package middleware содержит сквозные обёртки HTTP-обработчика.
package middleware

import (
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	apihttp "github.com/knyazushka/vcard/internal/handler/http"
)

const headerRequestID = "X-Request-Id"

// RequestID присваивает запросу идентификатор и возвращает его клиенту.
//
// Значение из входящего заголовка принимается только от доверенного прокси
// и в любом случае обрезается: иначе клиент управляет содержимым наших логов
// и может подмешать туда что угодно, включая переводы строк.
func RequestID(trustIncoming bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := ""
			if trustIncoming {
				id = sanitizeID(r.Header.Get(headerRequestID))
			}
			if id == "" {
				id = uuid.NewString()
			}

			w.Header().Set(headerRequestID, id)
			next.ServeHTTP(w, r.WithContext(apihttp.WithRequestID(r.Context(), id)))
		})
	}
}

func sanitizeID(s string) string {
	if len(s) > 64 {
		s = s[:64]
	}
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		default:
			return -1
		}
	}, s)
}

// ClientInfo складывает в контекст сведения для списка сессий.
//
// IP берётся из адреса соединения; X-Forwarded-For учитывается только
// за доверенным прокси — иначе клиент подставляет туда что угодно,
// и список устройств пользователя наполняется вымыслом.
func ClientInfo(trustProxy bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := clientIP(r, trustProxy)

			ua := r.UserAgent()
			if len(ua) > 255 {
				ua = ua[:255]
			}

			ctx := apihttp.WithClientInfo(r.Context(), apihttp.ClientInfo{UserAgent: ua, IP: ip})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func clientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			// Первый адрес в цепочке — исходный клиент.
			if first, _, found := strings.Cut(xff, ","); found || first != "" {
				if ip := net.ParseIP(strings.TrimSpace(first)); ip != nil {
					return ip.String()
				}
			}
		}
	}

	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return ""
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.String()
	}
	return ""
}

// Recover не даёт панике в одном обработчике уронить процесс целиком.
func Recover(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					log.ErrorContext(r.Context(), "panic recovered",
						"panic", rec,
						"method", r.Method,
						"path", r.URL.Path,
						"request_id", apihttp.RequestIDFromContext(r.Context()),
					)
					w.WriteHeader(http.StatusInternalServerError)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// AccessLog пишет по строке на запрос в stdout.
//
// Путь логируется в сыром виде почти везде, но не для маршрутов приёма
// приглашения: там в пути стоит одноразовый токен, и запись его в журнал
// доступа равносильна хранению учётных данных открытым текстом.
func AccessLog(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

			next.ServeHTTP(rec, r)

			log.InfoContext(r.Context(), "request",
				"method", r.Method,
				"path", redactPath(r.URL.Path),
				"status", rec.status,
				"bytes", rec.written,
				"duration_ms", time.Since(start).Milliseconds(),
				"request_id", apihttp.RequestIDFromContext(r.Context()),
			)
		})
	}
}

func redactPath(p string) string {
	const prefix = "/api/v1/invitations/"
	if !strings.HasPrefix(p, prefix) {
		return p
	}
	rest := strings.TrimPrefix(p, prefix)
	if rest == "" {
		return p
	}
	token, tail, found := strings.Cut(rest, "/")
	_ = token
	if found {
		return prefix + "{token}/" + tail
	}
	return prefix + "{token}"
}

type statusRecorder struct {
	http.ResponseWriter
	status  int
	written int
	wrote   bool
}

func (r *statusRecorder) WriteHeader(code int) {
	if r.wrote {
		return
	}
	r.wrote = true
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if !r.wrote {
		r.WriteHeader(http.StatusOK)
	}
	n, err := r.ResponseWriter.Write(b)
	r.written += n
	return n, err
}

// Chain применяет обёртки в порядке перечисления: первая — самая внешняя.
func Chain(h http.Handler, mws ...func(http.Handler) http.Handler) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}
