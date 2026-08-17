package middleware

import (
	"net/http"
	"strings"
)

// ConditionalGet превращает повторный запрос неизменившегося ресурса в 304.
//
// Обработчики выставляют ETag, но сами по себе они на If-None-Match
// не реагируют, а спека обещает 304 — без этого клиент каждый раз качает
// одно и то же тело, и весь смысл ETag теряется.
//
// Реализовано перехватом WriteHeader, а не буферизацией ответа: тело
// формируется как обычно, и если ETag совпал, оно просто не пишется
// в соединение. Так экономится трафик, но не работа сервера — зато
// не приходится держать ответ в памяти целиком.
func ConditionalGet() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				next.ServeHTTP(w, r)
				return
			}

			inm := r.Header.Get("If-None-Match")
			if inm == "" {
				next.ServeHTTP(w, r)
				return
			}

			next.ServeHTTP(&conditionalWriter{ResponseWriter: w, ifNoneMatch: inm}, r)
		})
	}
}

type conditionalWriter struct {
	http.ResponseWriter
	ifNoneMatch string
	notModified bool
	wrote       bool
}

func (w *conditionalWriter) WriteHeader(code int) {
	if w.wrote {
		return
	}
	w.wrote = true

	// Условный запрос осмыслен только для успешного ответа: 304 на ошибку
	// сказал бы клиенту, что у него уже есть актуальная версия ресурса,
	// которого нет.
	if code == http.StatusOK && etagMatches(w.Header().Get("ETag"), w.ifNoneMatch) {
		w.notModified = true

		// Тела не будет — заголовки, описывающие его, обязаны уйти.
		w.Header().Del("Content-Type")
		w.Header().Del("Content-Length")
		w.ResponseWriter.WriteHeader(http.StatusNotModified)
		return
	}

	w.ResponseWriter.WriteHeader(code)
}

func (w *conditionalWriter) Write(b []byte) (int, error) {
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}
	if w.notModified {
		// Делаем вид, что записали: обработчику не за что тут падать.
		return len(b), nil
	}
	return w.ResponseWriter.Write(b)
}

// etagMatches сравнивает ETag со списком из If-None-Match.
//
// Сравнение слабое, как и предписано для условных GET: префикс W/
// игнорируется с обеих сторон — он говорит лишь о том, что представления
// эквивалентны семантически, а не побайтово.
func etagMatches(etag, ifNoneMatch string) bool {
	if etag == "" {
		return false
	}
	if strings.TrimSpace(ifNoneMatch) == "*" {
		return true
	}

	want := trimWeak(etag)
	for _, candidate := range strings.Split(ifNoneMatch, ",") {
		if trimWeak(strings.TrimSpace(candidate)) == want {
			return true
		}
	}
	return false
}

func trimWeak(etag string) string {
	return strings.TrimPrefix(strings.TrimSpace(etag), "W/")
}
