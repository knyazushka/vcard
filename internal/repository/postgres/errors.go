// Package postgres — реализация хранилищ поверх pgx.
//
// Слой переводит ошибки драйвера в доменные: выше по стеку никто не должен
// знать ни про коды SQLSTATE, ни про имена индексов.
package postgres

import (
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/knyazushka/vcard/internal/domain"
)

// Коды PostgreSQL, которые нас интересуют.
const (
	codeUniqueViolation = "23505"
	// Клиент прислал байты, не являющиеся корректным UTF-8. Это ошибка
	// запроса, а не сервера: отвечать на неё пятисоткой значит списывать
	// на себя чужую кодировку.
	codeInvalidEncoding     = "22021"
	codeForeignKeyViolation = "23503"
	codeCheckViolation      = "23514"
	// Собственные коды наших триггеров — см. миграцию 00008.
	// Текст сообщения разбирать нельзя: он меняется при первой же правке
	// формулировки, код — нет.
	codeLastAdmin    = "VC001"
	codeSlugReserved = "VC002"
)

// mapError переводит ошибку драйвера в доменную.
//
// Различить «занята почта» от «занят слаг» — оба приходят одним и тем же
// 23505 — помогает isConstraint: вызывающий проверяет конкретное ограничение
// до того, как отдать ошибку сюда.
func mapError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}

	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return err
	}

	switch pgErr.Code {
	case codeLastAdmin:
		return domain.ErrLastAdmin
	case codeSlugReserved:
		return domain.ErrSlugReserved
	case codeUniqueViolation:
		if pgErr.ConstraintName == "profiles_slug_key" {
			return domain.ErrSlugTaken
		}
		return domain.ErrConflict
	case codeForeignKeyViolation, codeCheckViolation:
		return domain.ErrConflict
	case codeInvalidEncoding:
		return domain.ErrInvalidInput
	default:
		return err
	}
}

// isConstraint сообщает, вызвана ли ошибка нарушением конкретного ограничения.
func isConstraint(err error, name string) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	return pgErr.ConstraintName == name
}
