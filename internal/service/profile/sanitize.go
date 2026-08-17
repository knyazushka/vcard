package profile

import (
	"html"
	"strings"

	"github.com/microcosm-cc/bluemonday"
)

// Sanitizer очищает HTML из редактора «Обо мне».
//
// Очистка происходит НА ЗАПИСИ, и в базе лежит уже безопасный вариант.
// Если бы чистили на чтении, обновление правил молча меняло бы вид давно
// сохранённых страниц, а любое место, забывшее вызвать очистку, отдавало бы
// сырой ввод. На выдаче фильтр применяется повторно — как страховка.
type Sanitizer struct {
	policy *bluemonday.Policy
	plain  *bluemonday.Policy
}

// NewSanitizer собирает политику очистки.
func NewSanitizer() *Sanitizer {
	p := bluemonday.NewPolicy()

	// Ровно тот allowlist, что описан в спеке. Ссылки в него не входят
	// намеренно: «Обо мне» — это описание человека, а возможность вставить
	// произвольный href превращает каждую визитку в площадку для рассылки.
	p.AllowElements("p", "br", "b", "strong", "i", "em", "u", "h2", "h3", "ul", "ol", "li")

	// Quill размечает выравнивание и отступы классами ql-*. Разрешаем
	// только их: произвольный class открывает дорогу к стилям страницы.
	p.AllowAttrs("class").Matching(qlClassPattern).OnElements("p", "li", "ul", "ol")

	return &Sanitizer{policy: p, plain: bluemonday.StrictPolicy()}
}

// Sanitize возвращает очищенный HTML и его версию без разметки.
//
// Обе считаются вместе, чтобы они не могли разойтись: текстовая нужна
// для og:description и поиска, и она обязана соответствовать тому, что
// человек видит на странице.
func (s *Sanitizer) Sanitize(raw string) (safeHTML, plainText string) {
	safeHTML = strings.TrimSpace(s.policy.Sanitize(raw))

	// StrictPolicy вырезает теги, но оставляет сущности вида &amp;
	// закодированными — для текстовой версии их нужно раскодировать.
	plainText = strings.TrimSpace(html.UnescapeString(s.plain.Sanitize(safeHTML)))
	plainText = strings.Join(strings.Fields(plainText), " ")

	return safeHTML, plainText
}
