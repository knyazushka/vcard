package profile

import (
	"strings"
	"testing"
)

// Опасная разметка обязана исчезать: «Обо мне» приходит из редактора
// в браузере, то есть полностью под контролем пользователя.
func TestSanitizeStripsDangerousMarkup(t *testing.T) {
	s := NewSanitizer()

	cases := map[string]string{
		"скрипт":             `<p>ok</p><script>alert(1)</script>`,
		"обработчик":         `<img src=x onerror=alert(1)>`,
		"iframe":             `<iframe src="http://evil"></iframe>`,
		"ссылка":             `<a href="http://evil">click</a>`,
		"стиль":              `<p style="position:fixed;top:0">ok</p>`,
		"произвольный класс": `<p class="site-header">ok</p>`,
		"javascript-ссылка":  `<a href="javascript:alert(1)">x</a>`,
	}

	for name, raw := range cases {
		safe, _ := s.Sanitize(raw)

		for _, bad := range []string{"<script", "onerror", "<iframe", "href", "style=", "site-header", "javascript:"} {
			if strings.Contains(safe, bad) {
				t.Errorf("%s: в результате остался %q — %q", name, bad, safe)
			}
		}
	}
}

// Разрешённая разметка обязана сохраняться, иначе редактор бесполезен.
func TestSanitizeKeepsAllowedMarkup(t *testing.T) {
	s := NewSanitizer()

	raw := `<p>Обо <b>мне</b> и <em>работе</em></p><h2>Опыт</h2><ul><li>раз</li><li>два</li></ul><br>`
	safe, _ := s.Sanitize(raw)

	for _, want := range []string{"<p>", "<b>", "<em>", "<h2>", "<ul>", "<li>", "<br>"} {
		if !strings.Contains(safe, want) {
			t.Errorf("потерян разрешённый тег %q: %q", want, safe)
		}
	}
	if !strings.Contains(safe, "Обо") {
		t.Error("потерян текст")
	}
}

// Классы редактора Quill нужны для выравнивания и отступов — их пропускаем,
// но только их.
func TestSanitizeKeepsQuillClasses(t *testing.T) {
	s := NewSanitizer()

	safe, _ := s.Sanitize(`<p class="ql-align-center">по центру</p>`)
	if !strings.Contains(safe, "ql-align-center") {
		t.Errorf("класс редактора вырезан: %q", safe)
	}
}

// Текстовая версия нужна для og:description и поиска: она обязана быть
// без разметки и без HTML-сущностей.
func TestSanitizePlainText(t *testing.T) {
	s := NewSanitizer()

	_, plain := s.Sanitize(`<p>Иван   &amp;  Ко</p><h2>Опыт</h2><script>alert(1)</script>`)

	if strings.Contains(plain, "<") || strings.Contains(plain, "&amp;") {
		t.Errorf("текстовая версия содержит разметку или сущности: %q", plain)
	}
	if strings.Contains(plain, "alert") {
		t.Errorf("в текстовую версию попало содержимое скрипта: %q", plain)
	}
	if !strings.Contains(plain, "Иван & Ко") {
		t.Errorf("текст искажён: %q", plain)
	}
	if strings.Contains(plain, "  ") {
		t.Errorf("пробелы не схлопнуты: %q", plain)
	}
}

// Повторная очистка не должна ничего менять: результат уже безопасен,
// а страховочный проход на выдаче применяется к нему же.
func TestSanitizeIsIdempotent(t *testing.T) {
	s := NewSanitizer()

	once, _ := s.Sanitize(`<p>Обо <b>мне</b><script>x</script></p>`)
	twice, _ := s.Sanitize(once)

	if once != twice {
		t.Errorf("повторная очистка изменила результат:\n  %q\n  %q", once, twice)
	}
}
