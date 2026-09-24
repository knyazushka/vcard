package middleware

import (
	"math"
	"net/http"
	"net/netip"
	"strconv"
	"sync"
	"time"

	apihttp "github.com/knyazushka/vcard/internal/handler/http"
)

// RateLimitOptions — лимиты запросов с одного адреса в минуту.
// Ноль выключает соответствующий лимит.
type RateLimitOptions struct {
	// PerMinute — общий лимит на всё API.
	PerMinute int
	// StrictPerMinute — лимит для каждой операции из Strict вместо общего.
	StrictPerMinute int
	// Strict — операции, для которых общего лимита мало.
	Strict []string
}

// RateLimit ограничивает частоту запросов с одного адреса клиента.
//
// Корзины живут в памяти процесса: при нескольких репликах лимит
// фактически умножается на их число. Для одной реплики это точный
// лимит, а общее хранилище ради него — ещё один backing service.
//
// Адрес берётся из ClientInfo, поэтому обёртка стоит после неё:
// X-Forwarded-For учитывается только за доверенным прокси, и подменой
// заголовка лимит не обойти. /files не ограничивается: файлы неизменны
// и кэшируются браузером навсегда, а страница со списком сотрудников
// тянет десятки аватарок за раз и съела бы весь лимит одной загрузкой.
func RateLimit(o RateLimitOptions) func(http.Handler) http.Handler {
	general := newLimiter(o.PerMinute, time.Now)
	strict := newLimiter(o.StrictPerMinute, time.Now)

	strictOps := make(map[string]struct{}, len(o.Strict))
	for _, op := range o.Strict {
		strictOps[op] = struct{}{}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			op := OperationFromContext(r.Context())
			if op == OperationFiles {
				next.ServeHTTP(w, r)
				return
			}

			lim, key := general, clientKey(apihttp.ClientInfoFromContext(r.Context()).IP)
			if _, ok := strictOps[op]; ok {
				// Строгий лимит — у каждой операции свой: администратор,
				// разославший десяток приглашений, не должен заодно
				// лишаться возможности войти.
				lim, key = strict, op+" "+key
			}
			if lim == nil {
				next.ServeHTTP(w, r)
				return
			}

			if ok, wait := lim.allow(key); !ok {
				w.Header().Set("Retry-After", strconv.Itoa(retryAfterSeconds(wait)))
				apihttp.WriteError(w, r, http.StatusTooManyRequests, "rate_limited", "too many requests")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// clientKey — ключ корзины для адреса клиента. IPv6 группируется по /64:
// столько адресов выдаётся одному абоненту, и лимит на отдельный адрес
// обходился бы сменой последних цифр.
func clientKey(ip string) string {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return ip
	}
	addr = addr.Unmap()
	if addr.Is4() {
		return addr.String()
	}
	prefix, err := addr.Prefix(64)
	if err != nil {
		return addr.String()
	}
	return prefix.String()
}

// retryAfterSeconds округляет вверх: клиент, повторивший ровно через
// округлённое вниз время, снова получил бы отказ.
func retryAfterSeconds(wait time.Duration) int {
	return max(1, int(math.Ceil(wait.Seconds())))
}

// limiter — token bucket на каждый ключ. В корзине до perMinute жетонов,
// запрос забирает один, а возвращаются они равномерно — perMinute штук
// за минуту. Всплеск в пределах лимита проходит целиком, а не режется
// на стыке минут, как у фиксированного окна.
type limiter struct {
	mu        sync.Mutex
	capacity  float64
	perSecond float64
	buckets   map[string]*bucket
	lastSweep time.Time
	now       func() time.Time
}

type bucket struct {
	tokens float64
	last   time.Time
}

// newLimiter возвращает nil для нулевого лимита: nil означает
// «не ограничивать».
func newLimiter(perMinute int, now func() time.Time) *limiter {
	if perMinute <= 0 {
		return nil
	}
	return &limiter{
		capacity:  float64(perMinute),
		perSecond: float64(perMinute) / 60,
		buckets:   make(map[string]*bucket),
		lastSweep: now(),
		now:       now,
	}
}

// allow забирает жетон для ключа. При отказе возвращает, через сколько
// появится следующий.
func (l *limiter) allow(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	l.sweep(now)

	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{tokens: l.capacity, last: now}
		l.buckets[key] = b
	}

	b.tokens = min(l.capacity, b.tokens+now.Sub(b.last).Seconds()*l.perSecond)
	b.last = now

	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	return false, time.Duration((1 - b.tokens) / l.perSecond * float64(time.Second))
}

// sweep раз в «время полного наполнения» выбрасывает корзины, которые
// успели наполниться до краёв. Такая корзина ничем не отличается
// от новой, а хранить её — значит расти в памяти с каждым адресом,
// заглянувшим однажды.
func (l *limiter) sweep(now time.Time) {
	refill := time.Duration(l.capacity / l.perSecond * float64(time.Second))
	if now.Sub(l.lastSweep) < refill {
		return
	}
	l.lastSweep = now

	for key, b := range l.buckets {
		if now.Sub(b.last) >= refill {
			delete(l.buckets, key)
		}
	}
}
