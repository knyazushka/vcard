// Package http реализует сгенерированный из спеки интерфейс сервера.
//
// Здесь живёт только транспорт: разбор уже выполнил ogen, бизнес-правила
// лежат в сценариях, а этот слой переводит одно в другое и обратно.
package http

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/knyazushka/vcard/internal/gen/openapi"
	"github.com/knyazushka/vcard/internal/service/auth"
	cardsvc "github.com/knyazushka/vcard/internal/service/card"
	"github.com/knyazushka/vcard/internal/service/company"
	invitesvc "github.com/knyazushka/vcard/internal/service/invitation"
	profilesvc "github.com/knyazushka/vcard/internal/service/profile"
	"github.com/knyazushka/vcard/internal/storage"
)

// Limits — потолки размера загружаемых файлов.
type Limits struct {
	Avatar int64
	Logo   int64
}

// API реализует openapi.Handler.
//
// Встроенный UnimplementedHandler отвечает 501 на всё, что ещё не написано,
// — благодаря ему сервис поднимается и маршрутизируется целиком, до того как
// готовы все операции. Обратная сторона: пропущенный метод перестаёт быть
// ошибкой компиляции, поэтому встраивание убирается, как только реализованы
// все операции, — иначе теряется главное свойство contract-first.
type API struct {
	openapi.UnimplementedHandler

	auth        *auth.Service
	companies   *company.Service
	invitations *invitesvc.Service
	profiles    *profilesvc.Service
	cards       *cardsvc.Service
	files       storage.BlobStore
	limits      Limits
	log         *slog.Logger

	// Флаг Secure у refresh-cookie, приходит из конфига.
	secureCookies bool

	now func() time.Time
}

// Deps — зависимости обработчика.
type Deps struct {
	Auth        *auth.Service
	Companies   *company.Service
	Invitations *invitesvc.Service
	Profiles    *profilesvc.Service
	Cards       *cardsvc.Service
	Files       storage.BlobStore
	Limits      Limits
	Log         *slog.Logger

	SecureCookies bool
}

// NewAPI собирает обработчик со всеми его зависимостями.
func NewAPI(d Deps) *API {
	return &API{
		auth:          d.Auth,
		companies:     d.Companies,
		invitations:   d.Invitations,
		profiles:      d.Profiles,
		cards:         d.Cards,
		files:         d.Files,
		limits:        d.Limits,
		log:           d.Log,
		secureCookies: d.SecureCookies,
		now:           time.Now,
	}
}

// errUnauthenticated возвращается, когда обработчик вызван без установленного
// вызывающего. В норме до этого не доходит — операцию закрывает схема
// безопасности, — но молча полагаться на это нельзя.
var errUnauthenticated = errors.New("caller is not authenticated")

// NewError превращает всё, что не было отображено в конкретный ответ,
// в единый типизированный вид. Без него незамапленная ошибка уходит клиенту
// голым 500 без тела, и фронт не отличает её от сетевого сбоя.
//
// Сюда же ogen направляет отказы аутентификации — не в ErrorHandler, как
// можно ожидать. Поэтому статус берётся из mapError: иначе запрос без токена
// отвечает пятисоткой, то есть «сервис сломался» вместо «войдите».
//
// Наружу отдаётся только код: текст ошибки может содержать имена таблиц,
// фрагменты запросов и значения полей. Подробности остаются в логе,
// связь между ними — по requestId.
func (a *API) NewError(ctx context.Context, err error) *openapi.UnexpectedStatusCode {
	status, body := mapError(ctx, err)

	if status == http.StatusInternalServerError {
		a.log.ErrorContext(ctx, "unhandled error",
			"error", err, "request_id", RequestIDFromContext(ctx))
	}

	return &openapi.UnexpectedStatusCode{StatusCode: status, Response: body}
}
