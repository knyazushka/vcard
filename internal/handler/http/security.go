package http

import (
	"context"

	"github.com/knyazushka/vcard/internal/gen/openapi"
	"github.com/knyazushka/vcard/internal/service/auth"
)

// Security реализует openapi.SecurityHandler.
//
// Обе схемы обходятся без обращения к базе. Для access-токена это принцип:
// проверка сводится к одной подписи, поэтому аутентификация не добавляет
// запроса к Postgres на каждый вызов API. Для refresh — разделение
// обязанностей: здесь достаётся значение, а судьбу сессии решает сценарий,
// который умеет распознавать повторное использование токена.
type Security struct {
	tokens *auth.TokenIssuer
}

// NewSecurity создаёт обработчик схем безопасности.
func NewSecurity(tokens *auth.TokenIssuer) *Security {
	return &Security{tokens: tokens}
}

// HandleBearerAuth проверяет access-токен и кладёт вызывающего в контекст.
func (s *Security) HandleBearerAuth(
	ctx context.Context, _ openapi.OperationName, t openapi.BearerAuth,
) (context.Context, error) {
	claims, err := s.tokens.ParseAccess(t.GetToken())
	if err != nil {
		return ctx, err
	}

	userID, err := claims.UserID()
	if err != nil {
		return ctx, err
	}

	return WithPrincipal(ctx, Principal{
		UserID:    userID,
		SessionID: claims.SessionID,
	}), nil
}

// HandleRefreshCookie достаёт refresh-токен из cookie, не обращаясь к базе.
func (s *Security) HandleRefreshCookie(
	ctx context.Context, _ openapi.OperationName, t openapi.RefreshCookie,
) (context.Context, error) {
	token := t.GetAPIKey()
	if token == "" {
		return ctx, auth.ErrInvalidToken
	}
	return WithRefreshToken(ctx, token), nil
}
