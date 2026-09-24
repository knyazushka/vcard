package middleware

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
)

const frontOrigin = "https://vc.example.com"

// reached отмечает, дошёл ли запрос до обработчика.
func corsHandler(reached *bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		*reached = true
		w.WriteHeader(http.StatusOK)
	})
}

func doCORS(t *testing.T, allowed []string, method, origin string, preflight bool) (*httptest.ResponseRecorder, bool) {
	t.Helper()

	r := httptest.NewRequestWithContext(t.Context(), method, "/api/v1/auth/refresh", nil)
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	if preflight {
		r.Header.Set("Access-Control-Request-Method", http.MethodPost)
	}

	var reached bool
	rec := httptest.NewRecorder()
	CORS(allowed)(corsHandler(&reached)).ServeHTTP(rec, r)
	return rec, reached
}

func TestCORSAllowedOrigin(t *testing.T) {
	rec, reached := doCORS(t, []string{frontOrigin}, http.MethodPost, frontOrigin, false)

	if !reached {
		t.Fatal("запрос не дошёл до обработчика")
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != frontOrigin {
		t.Errorf("Allow-Origin = %q, ждали %q", got, frontOrigin)
	}
	// Без этого браузер не отправит refresh-cookie и не отдаст ответ.
	if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Errorf("Allow-Credentials = %q", got)
	}
	if got := rec.Header().Get("Access-Control-Expose-Headers"); got == "" {
		t.Error("X-Request-Id не открыт странице")
	}
	if !slices.Contains(rec.Header().Values("Vary"), "Origin") {
		t.Error("нет Vary: Origin")
	}
}

func TestCORSPreflight(t *testing.T) {
	rec, reached := doCORS(t, []string{frontOrigin}, http.MethodOptions, frontOrigin, true)

	if reached {
		t.Error("preflight ушёл в обработчик API")
	}
	if rec.Code != http.StatusNoContent {
		t.Errorf("код %d, ждали 204", rec.Code)
	}
	for _, name := range []string{
		"Access-Control-Allow-Origin",
		"Access-Control-Allow-Methods",
		"Access-Control-Allow-Headers",
		"Access-Control-Max-Age",
	} {
		if rec.Header().Get(name) == "" {
			t.Errorf("нет заголовка %s", name)
		}
	}
}

// Чужой origin не получает разрешений — ни на запрос, ни на preflight.
// Отражение любого Origin с Allow-Credentials отдало бы refresh-cookie
// посетителя любому сайту.
func TestCORSForeignOrigin(t *testing.T) {
	cases := map[string]string{
		"чужой сайт":          "https://evil.example.org",
		"тот же хост по http": "http://vc.example.com",
		"похожий поддомен":    "https://vc.example.com.evil.org",
		"null из песочницы":   "null",
		"со слэшем в конце":   frontOrigin + "/",
	}

	for name, origin := range cases {
		for _, preflight := range []bool{false, true} {
			method := http.MethodPost
			if preflight {
				method = http.MethodOptions
			}
			rec, reached := doCORS(t, []string{frontOrigin}, method, origin, preflight)

			if !reached {
				t.Errorf("%s (preflight=%v): запрос не дошёл до обработчика", name, preflight)
			}
			if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
				t.Errorf("%s (preflight=%v): Allow-Origin = %q", name, preflight, got)
			}
			if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "" {
				t.Errorf("%s (preflight=%v): Allow-Credentials = %q", name, preflight, got)
			}
		}
	}
}

func TestCORSWithoutOrigin(t *testing.T) {
	rec, reached := doCORS(t, []string{frontOrigin}, http.MethodGet, "", false)

	if !reached {
		t.Fatal("запрос не дошёл до обработчика")
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Allow-Origin = %q без Origin в запросе", got)
	}
	// Vary нужен и здесь: этот ответ может осесть в кэше и уйти фронту.
	if !slices.Contains(rec.Header().Values("Vary"), "Origin") {
		t.Error("нет Vary: Origin")
	}
}

func TestCORSDisabled(t *testing.T) {
	rec, reached := doCORS(t, nil, http.MethodOptions, frontOrigin, true)

	if !reached {
		t.Error("с пустым списком обёртка должна пропускать всё как есть")
	}
	if len(rec.Header()) != 0 {
		t.Errorf("с пустым списком появились заголовки: %v", rec.Header())
	}
}
