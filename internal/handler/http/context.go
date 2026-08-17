package http

import (
	"context"

	"github.com/google/uuid"
)

type ctxKey int

const (
	requestIDKey ctxKey = iota
	principalKey
	refreshTokenKey
	clientInfoKey
)

// Principal — то, что удалось установить о вызывающем по access-токену.
// Ни роли, ни компании здесь нет: они резолвятся по месту, из базы.
type Principal struct {
	UserID    uuid.UUID
	SessionID uuid.UUID
}

// ClientInfo — сведения о клиенте для списка сессий.
type ClientInfo struct {
	UserAgent string
	IP        string
}

// WithRequestID кладёт идентификатор запроса в контекст.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey, id)
}

// RequestIDFromContext достаёт идентификатор запроса; пустая строка, если его нет.
func RequestIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

// WithPrincipal кладёт установленного вызывающего в контекст.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey, p)
}

// PrincipalFromContext возвращает вызывающего. Второе значение — признак
// того, что запрос вообще прошёл аутентификацию: для операций
// с необязательной аутентификацией это разные ветки, а не «нулевой UUID».
func PrincipalFromContext(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey).(Principal)
	return p, ok
}

// WithRefreshToken кладёт сырой refresh-токен из cookie.
//
// Security-обработчик намеренно не ходит с ним в базу: его дело — достать
// значение, а решение о судьбе сессии принимает сценарий, который заодно
// умеет распознавать повторное использование.
func WithRefreshToken(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, refreshTokenKey, token)
}

// RefreshTokenFromContext достаёт сырой refresh-токен из контекста.
func RefreshTokenFromContext(ctx context.Context) (string, bool) {
	t, ok := ctx.Value(refreshTokenKey).(string)
	return t, ok && t != ""
}

// WithClientInfo кладёт сведения о клиенте в контекст.
func WithClientInfo(ctx context.Context, ci ClientInfo) context.Context {
	return context.WithValue(ctx, clientInfoKey, ci)
}

// ClientInfoFromContext достаёт сведения о клиенте; нулевое значение, если их нет.
func ClientInfoFromContext(ctx context.Context) ClientInfo {
	ci, _ := ctx.Value(clientInfoKey).(ClientInfo)
	return ci
}
