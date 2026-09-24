package middleware

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	apihttp "github.com/knyazushka/vcard/internal/handler/http"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newTestLimiter(perMinute int) (*limiter, *fakeClock) {
	clock := &fakeClock{t: time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)}
	return newLimiter(perMinute, clock.now), clock
}

// Лимит в минуту доступен сразу целиком: пользователь, открывший
// несколько вкладок подряд, не должен получать отказ на третьем запросе.
func TestLimiterBurstThenRefill(t *testing.T) {
	l, clock := newTestLimiter(60)

	for i := range 60 {
		if ok, _ := l.allow("1.2.3.4"); !ok {
			t.Fatalf("запрос %d из 60 отвергнут", i+1)
		}
	}

	ok, wait := l.allow("1.2.3.4")
	if ok {
		t.Fatal("61-й запрос прошёл")
	}
	// 60 в минуту — один жетон в секунду.
	if wait != time.Second {
		t.Errorf("ждать %v, ожидали 1s", wait)
	}

	clock.advance(time.Second)
	if ok, _ := l.allow("1.2.3.4"); !ok {
		t.Error("через секунду жетон не вернулся")
	}
	if ok, _ := l.allow("1.2.3.4"); ok {
		t.Error("вернулся лишний жетон")
	}
}

func TestLimiterKeysAreIndependent(t *testing.T) {
	l, _ := newTestLimiter(1)

	if ok, _ := l.allow("1.1.1.1"); !ok {
		t.Fatal("первый запрос отвергнут")
	}
	if ok, _ := l.allow("1.1.1.1"); ok {
		t.Fatal("лимит не сработал")
	}
	if ok, _ := l.allow("2.2.2.2"); !ok {
		t.Error("чужой лимит задел другой адрес")
	}
}

// Простой не копит жетоны сверх лимита: иначе после часа тишины
// можно было бы выпустить разом шестьдесят лимитов.
func TestLimiterCapacityIsCapped(t *testing.T) {
	l, clock := newTestLimiter(5)

	l.allow("k")
	clock.advance(time.Hour)

	passed := 0
	for range 10 {
		if ok, _ := l.allow("k"); ok {
			passed++
		}
	}
	if passed != 5 {
		t.Errorf("после простоя прошло %d, ожидали 5", passed)
	}
}

func TestLimiterSweepsFullBuckets(t *testing.T) {
	l, clock := newTestLimiter(10)

	l.allow("idle")
	clock.advance(30 * time.Second)
	l.allow("active")

	clock.advance(40 * time.Second) // idle молчит 70 с, active — 40 с
	l.allow("active")

	if _, ok := l.buckets["idle"]; ok {
		t.Error("наполнившаяся корзина не выброшена")
	}
	if _, ok := l.buckets["active"]; !ok {
		t.Error("выброшена корзина, которая ещё не наполнилась")
	}
}

func TestNewLimiterZeroDisables(t *testing.T) {
	if l, _ := newTestLimiter(0); l != nil {
		t.Error("нулевой лимит должен выключать ограничение")
	}
}

func TestClientKey(t *testing.T) {
	cases := map[string]string{
		"203.0.113.7":             "203.0.113.7",
		"::ffff:203.0.113.7":      "203.0.113.7",
		"2001:db8:1:2:aaaa::1":    "2001:db8:1:2::/64",
		"2001:db8:1:2:bbbb::9999": "2001:db8:1:2::/64",
		"":                        "",
	}
	for ip, want := range cases {
		if got := clientKey(ip); got != want {
			t.Errorf("%q: получили %q, ждали %q", ip, got, want)
		}
	}
}

func TestRetryAfterRoundsUp(t *testing.T) {
	cases := map[time.Duration]int{
		10 * time.Millisecond:   1,
		time.Second:             1,
		1500 * time.Millisecond: 2,
		6 * time.Second:         6,
	}
	for wait, want := range cases {
		if got := retryAfterSeconds(wait); got != want {
			t.Errorf("%v: получили %d, ждали %d", wait, got, want)
		}
	}
}

func doLimited(t *testing.T, h http.Handler, op, ip string) *httptest.ResponseRecorder {
	t.Helper()

	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/auth/login", nil)
	ctx := context.WithValue(r.Context(), operationKey{}, op)
	ctx = apihttp.WithClientInfo(ctx, apihttp.ClientInfo{IP: ip})
	ctx = apihttp.WithRequestID(ctx, "req-1")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r.WithContext(ctx))
	return rec
}

func TestRateLimitMiddleware(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	h := RateLimit(RateLimitOptions{PerMinute: 3, StrictPerMinute: 1, Strict: []string{"login"}})(ok)

	t.Run("строгая операция живёт по своему лимиту", func(t *testing.T) {
		if rec := doLimited(t, h, "login", "10.0.0.1"); rec.Code != http.StatusOK {
			t.Fatalf("первый вход: %d", rec.Code)
		}
		rec := doLimited(t, h, "login", "10.0.0.1")
		if rec.Code != http.StatusTooManyRequests {
			t.Fatalf("второй вход: %d, ждали 429", rec.Code)
		}
		if rec.Header().Get("Retry-After") == "" {
			t.Error("нет Retry-After")
		}

		// Тело — тот же Error, что и у остального API.
		var body struct{ Code, RequestID string }
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("тело не JSON: %v", err)
		}
		if body.Code != "rate_limited" || body.RequestID != "req-1" {
			t.Errorf("тело %s", rec.Body.String())
		}
	})

	t.Run("у каждой строгой операции свой лимит", func(t *testing.T) {
		h := RateLimit(RateLimitOptions{PerMinute: 100, StrictPerMinute: 1, Strict: []string{"login", "createInvitation"}})(ok)

		if rec := doLimited(t, h, "createInvitation", "10.0.0.9"); rec.Code != http.StatusOK {
			t.Fatalf("приглашение: %d", rec.Code)
		}
		if rec := doLimited(t, h, "login", "10.0.0.9"); rec.Code != http.StatusOK {
			t.Errorf("вход после приглашения: %d — лимиты операций смешались", rec.Code)
		}
	})

	t.Run("общий лимит не задет строгим", func(t *testing.T) {
		for i := range 3 {
			if rec := doLimited(t, h, "getOwnProfile", "10.0.0.1"); rec.Code != http.StatusOK {
				t.Fatalf("запрос %d: %d", i+1, rec.Code)
			}
		}
		if rec := doLimited(t, h, "getOwnProfile", "10.0.0.1"); rec.Code != http.StatusTooManyRequests {
			t.Errorf("четвёртый запрос: %d, ждали 429", rec.Code)
		}
	})

	t.Run("файлы не ограничиваются", func(t *testing.T) {
		for i := range 10 {
			if rec := doLimited(t, h, OperationFiles, "10.0.0.1"); rec.Code != http.StatusOK {
				t.Fatalf("файл %d: %d", i+1, rec.Code)
			}
		}
	})
}
