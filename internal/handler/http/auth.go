package http

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/google/uuid"

	"github.com/knyazushka/vcard/internal/domain"
	"github.com/knyazushka/vcard/internal/gen/openapi"
	"github.com/knyazushka/vcard/internal/service/auth"
)

// refreshCookieName и refreshCookiePath должны совпадать с тем, что описано
// в спеке: клиент про cookie ничего не знает, но браузер сверяет путь.
const (
	refreshCookieName = "vcard_refresh"
	refreshCookiePath = "/api/v1/auth"
)

// Register реализует операцию register.
func (a *API) Register(ctx context.Context, req *openapi.RegisterRequest) (openapi.RegisterRes, error) {
	tokens, err := a.auth.Register(ctx, string(req.Email), string(req.Password), clientInfo(ctx))
	switch {
	case errors.Is(err, domain.ErrEmailTaken):
		return &openapi.RegisterConflict{
			Code:      "email_taken",
			Message:   "email is already registered",
			RequestId: a.reqID(ctx),
		}, nil
	case err != nil:
		return nil, err
	}

	return &openapi.AccessTokenHeaders{
		SetCookie: openapi.NewOptString(a.refreshCookie(tokens.Refresh, tokens.RefreshTTL)),
		Response:  accessToken(tokens),
	}, nil
}

// Login реализует операцию login.
func (a *API) Login(ctx context.Context, req *openapi.LoginRequest) (openapi.LoginRes, error) {
	tokens, err := a.auth.Login(ctx, string(req.Email), req.Password, clientInfo(ctx))
	switch {
	case errors.Is(err, domain.ErrInvalidCredentials):
		// Один и тот же ответ на «нет такого адреса» и «неверный пароль».
		return &openapi.LoginUnauthorized{
			Code:      "unauthenticated",
			Message:   "invalid email or password",
			RequestId: a.reqID(ctx),
		}, nil
	case err != nil:
		return nil, err
	}

	return &openapi.AccessTokenHeaders{
		SetCookie: openapi.NewOptString(a.refreshCookie(tokens.Refresh, tokens.RefreshTTL)),
		Response:  accessToken(tokens),
	}, nil
}

// RefreshToken реализует операцию refreshToken.
func (a *API) RefreshToken(ctx context.Context) (openapi.RefreshTokenRes, error) {
	token, ok := RefreshTokenFromContext(ctx)
	if !ok {
		return a.refreshRejected(ctx), nil
	}

	tokens, err := a.auth.Refresh(ctx, token, clientInfo(ctx))
	switch {
	case errors.Is(err, domain.ErrSessionInvalid):
		// Cookie стирается вместе с отказом: держать в браузере заведомо
		// мёртвый токен незачем, а при обнаружении повторного использования
		// это ещё и выкидывает вора из сессии.
		return a.refreshRejected(ctx), nil
	case err != nil:
		return nil, err
	}

	return &openapi.AccessTokenHeaders{
		SetCookie: openapi.NewOptString(a.refreshCookie(tokens.Refresh, tokens.RefreshTTL)),
		Response:  accessToken(tokens),
	}, nil
}

// Logout реализует операцию logout.
func (a *API) Logout(ctx context.Context) (openapi.LogoutRes, error) {
	// Выйти можно и по access-токену, и по одной лишь cookie: если access
	// уже истёк, кнопка «выйти» всё равно должна работать.
	if p, ok := PrincipalFromContext(ctx); ok {
		if err := a.auth.Logout(ctx, p.SessionID); err != nil {
			return nil, err
		}
	} else if token, ok := RefreshTokenFromContext(ctx); ok {
		if err := a.auth.LogoutByRefreshToken(ctx, token); err != nil {
			return nil, err
		}
	}

	return &openapi.LogoutNoContent{
		SetCookie: openapi.NewOptString(a.clearRefreshCookie()),
	}, nil
}

// LogoutAll реализует операцию logoutAll.
func (a *API) LogoutAll(ctx context.Context) (openapi.LogoutAllRes, error) {
	p, ok := PrincipalFromContext(ctx)
	if !ok {
		return nil, auth.ErrInvalidToken
	}

	if err := a.auth.LogoutAll(ctx, p.UserID); err != nil {
		return nil, err
	}

	return &openapi.LogoutAllNoContent{
		SetCookie: openapi.NewOptString(a.clearRefreshCookie()),
	}, nil
}

// GetMe реализует операцию getMe.
func (a *API) GetMe(ctx context.Context) (openapi.GetMeRes, error) {
	p, ok := PrincipalFromContext(ctx)
	if !ok {
		return nil, auth.ErrInvalidToken
	}

	me, err := a.auth.Me(ctx, p.UserID)
	if err != nil {
		return nil, err
	}

	memberships := make([]openapi.Membership, 0, len(me.Memberships))
	for _, m := range me.Memberships {
		memberships = append(memberships, openapi.Membership{
			CompanyId:      openapi.UUID(m.CompanyID),
			CompanyName:    m.CompanyName,
			CompanyLogoUrl: optURL(a.files.URL(m.CompanyLogoKey)),
			Role:           openapi.Role(m.Role),
			ProfileId:      optUUID(m.ProfileID),
		})
	}

	return &openapi.Me{
		User: openapi.User{
			ID:            openapi.UUID(me.User.ID),
			Email:         openapi.Email(me.User.Email),
			EmailVerified: openapi.NewOptBool(me.User.EmailVerified),
			CreatedAt:     openapi.Timestamp(me.User.CreatedAt),
		},
		Memberships: memberships,
	}, nil
}

// --- вспомогательное ---------------------------------------------------------

func accessToken(t auth.Tokens) openapi.AccessToken {
	return openapi.AccessToken{
		AccessToken: t.Access,
		TokenType:   openapi.AccessTokenTokenTypeBearer,
		ExpiresIn:   int32(t.AccessTTL.Seconds()),
	}
}

// refreshCookie собирает Set-Cookie.
//
// HttpOnly — чтобы XSS не добрался до токена; Secure — чтобы он не ушёл
// по http; Path сужен до /auth, поэтому на остальные запросы cookie
// не отправляется вовсе. SameSite=Lax достаточно: refresh обменивается
// только методом POST, а Lax запрещает межсайтовые POST-запросы.
func (a *API) refreshCookie(token string, ttl time.Duration) string {
	c := &http.Cookie{ //nolint:gosec // Secure задаётся конфигом, см. HTTP.CookieSecure
		Name:     refreshCookieName,
		Value:    token,
		Path:     refreshCookiePath,
		HttpOnly: true,
		Secure:   a.secureCookies,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(ttl.Seconds()),
	}
	return c.String()
}

func (a *API) clearRefreshCookie() string {
	c := &http.Cookie{ //nolint:gosec // Secure задаётся конфигом, см. HTTP.CookieSecure
		Name:     refreshCookieName,
		Value:    "",
		Path:     refreshCookiePath,
		HttpOnly: true,
		Secure:   a.secureCookies,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	}
	return c.String()
}

func (a *API) refreshRejected(ctx context.Context) *openapi.ErrorHeaders {
	return &openapi.ErrorHeaders{
		SetCookie: openapi.NewOptString(a.clearRefreshCookie()),
		Response: openapi.Error{
			Code:      "unauthenticated",
			Message:   "refresh token is invalid",
			RequestId: a.reqID(ctx),
		},
	}
}

func (a *API) reqID(ctx context.Context) openapi.OptString {
	return openapi.NewOptString(RequestIDFromContext(ctx))
}

func clientInfo(ctx context.Context) auth.ClientInfo {
	ci := ClientInfoFromContext(ctx)
	return auth.ClientInfo{UserAgent: ci.UserAgent, IP: ci.IP}
}

func optUUID(id uuid.UUID) openapi.OptNilUUID {
	if id == uuid.Nil {
		return openapi.OptNilUUID{Set: true, Null: true}
	}
	return openapi.NewOptNilUUID(id)
}

// optURL превращает адрес в опциональное поле ответа. Пустая строка и любой
// неразбираемый адрес дают явный null: отдать клиенту битую ссылку хуже,
// чем честно сказать, что картинки нет.
func optURL(raw string) openapi.OptNilURLNullable {
	if raw == "" {
		return openapi.OptNilURLNullable{Set: true, Null: true}
	}
	u, err := url.Parse(raw)
	if err != nil {
		return openapi.OptNilURLNullable{Set: true, Null: true}
	}
	return openapi.NewOptNilURLNullable(openapi.URLNullable(*u))
}
