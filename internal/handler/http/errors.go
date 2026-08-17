package http

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-faster/jx"
	ht "github.com/ogen-go/ogen/http"
	"github.com/ogen-go/ogen/ogenerrors"

	"github.com/knyazushka/vcard/internal/domain"
	"github.com/knyazushka/vcard/internal/gen/openapi"
	"github.com/knyazushka/vcard/internal/service/auth"
)

// NewErrorHandler отображает ошибки уровня транспорта в тот же формат Error,
// что и всё остальное API.
//
// Без него ogen отвечает на любую свою ошибку пятисоткой: запрос без токена
// становится «внутренней ошибкой сервера», и фронт не может отличить
// «нужно войти» от «сервис сломался». Тело при этом тоже своё, не такое,
// как у прикладных ошибок, — клиенту приходится разбирать два формата.
func NewErrorHandler(log *slog.Logger) ogenerrors.ErrorHandler {
	return func(ctx context.Context, w http.ResponseWriter, r *http.Request, err error) {
		status, body := mapError(ctx, err)

		// Только настоящая пятисотка. 501 тоже больше 500, но «операция
		// ещё не написана» — это состояние разработки, а не инцидент,
		// и засорять им ошибки нельзя: перестанешь их читать.
		if status == http.StatusInternalServerError {
			log.ErrorContext(ctx, "request failed",
				"error", err,
				"path", r.URL.Path,
				"request_id", RequestIDFromContext(ctx),
			)
		}

		// Кодируется тем же сериализатором, что и ответы ogen. Через
		// encoding/json форма получилась бы другой (например, nil-срез
		// превращается в "fields":null), и клиент видел бы два разных
		// представления одного и того же типа Error.
		enc := jx.GetEncoder()
		defer jx.PutEncoder(enc)
		body.Encode(enc)

		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(status)
		_, _ = w.Write(enc.Bytes())
	}
}

func mapError(ctx context.Context, err error) (int, openapi.Error) {
	reqID := openapi.NewOptString(RequestIDFromContext(ctx))

	// Операции, до которых ещё не дошли руки: их отдаёт встроенный
	// UnimplementedHandler. Отвечать на них пятисоткой нельзя — фронт
	// принял бы «ещё не написано» за сбой и полез бы разбираться.
	if errors.Is(err, ht.ErrNotImplemented) {
		return http.StatusNotImplemented, openapi.Error{
			Code:      "not_implemented",
			Message:   "operation is not implemented yet",
			RequestId: reqID,
		}
	}

	// Отказ аутентификации: битый или истёкший access-токен приходит сюда
	// же, минуя ogen-обёртку, когда его отвергает security-обработчик.
	if errors.Is(err, auth.ErrInvalidToken) || errors.Is(err, domain.ErrSessionInvalid) {
		return http.StatusUnauthorized, openapi.Error{
			Code:      "unauthenticated",
			Message:   "authentication required",
			RequestId: reqID,
		}
	}

	if errors.Is(err, domain.ErrInvalidInput) {
		return http.StatusBadRequest, openapi.Error{
			Code:      "validation_failed",
			Message:   "request contains an invalid value",
			RequestId: reqID,
		}
	}

	if errors.Is(err, domain.ErrForbidden) {
		return http.StatusForbidden, openapi.Error{
			Code:      "forbidden",
			Message:   "insufficient permissions",
			RequestId: reqID,
		}
	}

	if errors.Is(err, domain.ErrNotFound) {
		return http.StatusNotFound, openapi.Error{
			Code:      "not_found",
			Message:   "resource not found",
			RequestId: reqID,
		}
	}

	var secErr *ogenerrors.SecurityError
	if errors.As(err, &secErr) {
		// Наружу — только «войдите». Причина отказа (нет заголовка, битая
		// подпись, отозванная сессия) остаётся в логе: по разнице ответов
		// перебирают состояние чужих токенов.
		return http.StatusUnauthorized, openapi.Error{
			Code:      "unauthenticated",
			Message:   "authentication required",
			RequestId: reqID,
		}
	}

	var paramsErr *ogenerrors.DecodeParamsError
	if errors.As(err, &paramsErr) {
		return http.StatusBadRequest, openapi.Error{
			Code:      "validation_failed",
			Message:   "invalid request parameters",
			RequestId: reqID,
		}
	}

	var reqErr *ogenerrors.DecodeRequestError
	if errors.As(err, &reqErr) {
		return http.StatusBadRequest, openapi.Error{
			Code:      "validation_failed",
			Message:   "invalid request body",
			RequestId: reqID,
		}
	}

	var bodyErr *ogenerrors.DecodeBodyError
	if errors.As(err, &bodyErr) {
		return http.StatusBadRequest, openapi.Error{
			Code:      "validation_failed",
			Message:   "invalid request body",
			RequestId: reqID,
		}
	}

	return http.StatusInternalServerError, openapi.Error{
		Code:      "internal_error",
		Message:   "internal server error",
		RequestId: reqID,
	}
}
