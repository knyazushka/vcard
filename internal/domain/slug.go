package domain

import (
	"fmt"
	"strings"
	"unicode"
)

// Ограничения адреса визитки — те же, что в спеке.
const (
	SlugMinLen = 3
	SlugMaxLen = 64
)

// SlugFromEmail строит адрес визитки из части почты до «собаки».
//
// Пункт ТЗ: адрес выдаётся автоматически, но его можно поменять на любой
// свободный. Результат — только заготовка: уникальность проверяет база,
// а не эта функция.
func SlugFromEmail(email string) string {
	local, _, _ := strings.Cut(NormalizeEmail(email), "@")
	return NormalizeSlug(local)
}

// NormalizeSlug приводит строку к виду, допустимому в URL визитки.
//
// Всё, что не буква и не цифра, становится дефисом; повторы схлопываются;
// края обрезаются. Кириллица не транслитерируется, а отбрасывается: гадать
// про правила транслитерации — верный способ выдать человеку адрес,
// который он не узнает. Если после чистки ничего не осталось, вызывающий
// подставит запасной вариант.
func NormalizeSlug(s string) string {
	var b strings.Builder
	prevDash := true // не даём слагу начаться с дефиса

	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch {
		case r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r)):
			b.WriteRune(r)
			prevDash = false
		case !prevDash:
			b.WriteRune('-')
			prevDash = true
		}
	}

	out := strings.Trim(b.String(), "-")
	if len(out) > SlugMaxLen {
		out = strings.Trim(out[:SlugMaxLen], "-")
	}
	return out
}

// SlugCandidates перечисляет варианты адреса: сначала желаемый, затем
// с числовыми суффиксами.
//
// Нужен, потому что занятость проверяется вставкой с обработкой конфликта,
// а не предварительным SELECT: между проверкой и записью успевает вклиниться
// другая регистрация, и «свободный» адрес оказывается занят.
func SlugCandidates(base string, attempts int) []string {
	if base == "" {
		base = "user"
	}
	for len(base) < SlugMinLen {
		base += "0"
	}

	out := make([]string, 0, attempts)
	out = append(out, base)

	for i := 2; len(out) < attempts; i++ {
		suffix := fmt.Sprintf("-%d", i)
		trimmed := base
		if len(trimmed)+len(suffix) > SlugMaxLen {
			trimmed = strings.Trim(base[:SlugMaxLen-len(suffix)], "-")
		}
		out = append(out, trimmed+suffix)
	}
	return out
}
