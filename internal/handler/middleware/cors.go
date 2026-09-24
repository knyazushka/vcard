package middleware

import (
	"net/http"
	"strings"
)

// Что разрешается странице фронта. Списки явные, а не отражение того,
// что попросил браузер: иначе набор допустимых заголовков определял бы
// клиент, а не мы.
var (
	corsAllowMethods = strings.Join([]string{
		http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete,
	}, ", ")
	corsAllowHeaders = strings.Join([]string{"Authorization", "Content-Type", "If-None-Match"}, ", ")
	// X-Request-Id — чтобы фронт мог показать его в сообщении об ошибке,
	// Content-Disposition — имя файла при скачивании карточки через fetch.
	corsExposeHeaders = strings.Join([]string{"X-Request-Id", "Content-Disposition"}, ", ")
)

// corsMaxAge — сколько браузер помнит ответ на preflight. Больше двух
// часов Chromium всё равно не хранит.
const corsMaxAge = "7200"

// CORS разрешает страницам фронта читать ответы API.
//
// Фронт и API живут на разных поддоменах, а для браузера это разные
// origin: без этих заголовков он не отдаст странице ни одного ответа.
//
// Origin сверяется с точным списком. Отражать любой пришедший Origin
// нельзя: вместе с Allow-Credentials это позволило бы любому сайту
// обменять refresh-cookie посетителя на access-токен.
//
// Пустой список выключает обёртку целиком: без фронта на другом origin
// ни заголовки, ни Vary не нужны.
func CORS(allowed []string) func(http.Handler) http.Handler {
	origins := make(map[string]struct{}, len(allowed))
	for _, o := range allowed {
		origins[o] = struct{}{}
	}

	return func(next http.Handler) http.Handler {
		if len(origins) == 0 {
			return next
		}

		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			// Ответ зависит от Origin, даже если заголовков CORS в нём нет.
			// Иначе кэш по дороге отдаст фронту копию, сохранённую для
			// запроса без Origin, и браузер её отвергнет.
			h.Add("Vary", "Origin")

			origin := r.Header.Get("Origin")
			if _, ok := origins[origin]; !ok {
				// Чужой origin получает обычный ответ без разрешений —
				// браузер сам не покажет его странице. Отказывать явно
				// незачем: запрос без браузера CORS не касается вовсе.
				next.ServeHTTP(w, r)
				return
			}

			h.Set("Access-Control-Allow-Origin", origin)
			h.Set("Access-Control-Allow-Credentials", "true")

			if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
				h.Add("Vary", "Access-Control-Request-Method")
				h.Add("Vary", "Access-Control-Request-Headers")
				h.Set("Access-Control-Allow-Methods", corsAllowMethods)
				h.Set("Access-Control-Allow-Headers", corsAllowHeaders)
				h.Set("Access-Control-Max-Age", corsMaxAge)
				w.WriteHeader(http.StatusNoContent)
				return
			}

			h.Set("Access-Control-Expose-Headers", corsExposeHeaders)
			next.ServeHTTP(w, r)
		})
	}
}
