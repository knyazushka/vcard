package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func handlerWithETag(etag string, status int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if etag != "" {
			w.Header().Set("ETag", etag)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"payload":"body"}`))
	})
}

func do(t *testing.T, h http.Handler, ifNoneMatch, method string) *httptest.ResponseRecorder {
	t.Helper()

	r := httptest.NewRequestWithContext(t.Context(), method, "/public/employee/laura", nil)
	if ifNoneMatch != "" {
		r.Header.Set("If-None-Match", ifNoneMatch)
	}
	rec := httptest.NewRecorder()
	ConditionalGet()(h).ServeHTTP(rec, r)
	return rec
}

func TestConditionalGetMatching(t *testing.T) {
	h := handlerWithETag(`W/"PUBLISHED-42"`, http.StatusOK)

	cases := map[string]string{
		"точное совпадение": `W/"PUBLISHED-42"`,
		"слабое сравнение":  `"PUBLISHED-42"`,
		"список":            `W/"other", W/"PUBLISHED-42"`,
		"звёздочка":         `*`,
		"пробелы в списке":  `  W/"PUBLISHED-42"  `,
	}

	for name, inm := range cases {
		rec := do(t, h, inm, http.MethodGet)

		if rec.Code != http.StatusNotModified {
			t.Errorf("%s: получили %d, ждали 304", name, rec.Code)
		}
		if rec.Body.Len() != 0 {
			t.Errorf("%s: тело не пустое (%d байт)", name, rec.Body.Len())
		}
		// Заголовки, описывающие тело, при 304 не отправляются.
		if rec.Header().Get("Content-Type") != "" {
			t.Errorf("%s: остался Content-Type", name)
		}
	}
}

func TestConditionalGetMismatch(t *testing.T) {
	h := handlerWithETag(`W/"PUBLISHED-42"`, http.StatusOK)

	for name, inm := range map[string]string{
		"устаревший ETag": `W/"PUBLISHED-1"`,
		"чужой ETag":      `"whatever"`,
		"без заголовка":   "",
	} {
		rec := do(t, h, inm, http.MethodGet)
		if rec.Code != http.StatusOK {
			t.Errorf("%s: получили %d, ждали 200", name, rec.Code)
		}
		if rec.Body.Len() == 0 {
			t.Errorf("%s: тело пустое", name)
		}
	}
}

// 304 на ошибку сказал бы клиенту, что у него есть актуальная версия
// ресурса, которого нет.
func TestConditionalGetIgnoresNonOK(t *testing.T) {
	h := handlerWithETag(`W/"x"`, http.StatusNotFound)

	rec := do(t, h, `W/"x"`, http.MethodGet)
	if rec.Code != http.StatusNotFound {
		t.Errorf("получили %d, ждали 404", rec.Code)
	}
}

// Условный запрос определён только для безопасных методов: PATCH
// с If-None-Match не должен молча превращаться в «ничего не делать».
func TestConditionalGetIgnoresUnsafeMethods(t *testing.T) {
	h := handlerWithETag(`W/"x"`, http.StatusOK)

	rec := do(t, h, `W/"x"`, http.MethodPatch)
	if rec.Code != http.StatusOK {
		t.Errorf("получили %d, ждали 200", rec.Code)
	}
}

func TestConditionalGetWithoutETag(t *testing.T) {
	h := handlerWithETag("", http.StatusOK)

	rec := do(t, h, `*`, http.MethodGet)
	// Звёздочка означает «если ресурс существует». Ответ без ETag
	// под это правило всё равно попадает.
	if rec.Code != http.StatusNotModified {
		t.Logf("ответ без ETag на '*' дал %d", rec.Code)
	}

	rec = do(t, h, `W/"x"`, http.MethodGet)
	if rec.Code != http.StatusOK {
		t.Errorf("ответ без ETag должен идти целиком, получили %d", rec.Code)
	}
}
