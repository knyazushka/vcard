// Package domain содержит сущности и правила, не зависящие ни от транспорта,
// ни от хранилища.
package domain

import "errors"

// Общие ошибки предметной области. Слой транспорта отображает их в коды
// ответов, слой хранилища — возвращает; ни тот ни другой не выдумывает
// собственных словарей.
var (
	ErrNotFound  = errors.New("not found")
	ErrConflict  = errors.New("conflict")
	ErrForbidden = errors.New("forbidden")

	// ErrInvalidInput — запрос синтаксически разобран, но значение не годится
	// (например, строка не в UTF-8). Ошибка клиента, а не сервера.
	ErrInvalidInput = errors.New("invalid input")

	// ErrInvalidCredentials намеренно один на «нет такого пользователя»
	// и «неверный пароль». Различать их в ответе — значит превратить форму
	// входа в перебиратель зарегистрированных адресов.
	ErrInvalidCredentials = errors.New("invalid credentials")

	// ErrSessionInvalid — refresh-токен неизвестен, истёк, отозван
	// или предъявлен повторно. Наружу все четыре случая выглядят одинаково.
	ErrSessionInvalid = errors.New("session is invalid")

	ErrEmailTaken = errors.New("email is already taken")

	// ErrLastAdmin — операция оставила бы компанию без администратора.
	// Проверку делает база: два одновременных понижения по отдельности
	// выглядят допустимыми, а вместе лишают компанию управления.
	ErrLastAdmin = errors.New("company would be left without an admin")

	ErrSlugTaken    = errors.New("slug is already taken")
	ErrSlugReserved = errors.New("slug is reserved")

	// ErrProfileIncomplete — профиль не готов к публикации: нет имени
	// или тегов меньше трёх.
	ErrProfileIncomplete = errors.New("profile is not ready to be published")
	// ErrTooManyTags — тегов больше допустимого.
	ErrTooManyTags = errors.New("too many tags")
	// ErrPositionAmbiguous — должность задана и ссылкой, и текстом сразу.
	ErrPositionAmbiguous = errors.New("position is set both by reference and by text")
	// ErrCropOutOfBounds — область кадрирования выходит за границы файла.
	ErrCropOutOfBounds = errors.New("crop is out of image bounds")

	// ErrAlreadyMember — приглашённый уже состоит в компании. Не ошибка
	// пользователя: приём такого приглашения просто идемпотентен.
	ErrAlreadyMember = errors.New("user is already a member")

	ErrInvitationNotFound = errors.New("invitation is not usable")
	// ErrEmailMismatch — приглашение выписано на другой адрес. Токена
	// самого по себе для вступления недостаточно: письма пересылают.
	ErrEmailMismatch = errors.New("invitation was issued for another email")
	// ErrPasswordRequired — учётки с этим адресом ещё нет, нужен пароль.
	ErrPasswordRequired = errors.New("password is required to register")
	// ErrAuthRequired — учётка есть, нужен вход, а не регистрация.
	ErrAuthRequired = errors.New("authentication is required to accept")
)
