package domain

import (
	"strings"
	"testing"
)

func TestSlugFromEmail(t *testing.T) {
	cases := map[string]string{
		"Laura.Hoffmann@Acme.com": "laura-hoffmann",
		"boss@acme.com":           "boss",
		"ivan+work@acme.com":      "ivan-work",
		"a_b.c@acme.com":          "a-b-c",
		"--weird--@acme.com":      "weird",
		// Кириллица отбрасывается: гадать про транслитерацию — верный способ
		// выдать человеку адрес, который он не узнает.
		"иван@acme.com": "",
	}

	for email, want := range cases {
		if got := SlugFromEmail(email); got != want {
			t.Errorf("%s: получили %q, ждали %q", email, got, want)
		}
	}
}

func TestNormalizeSlugShape(t *testing.T) {
	got := NormalizeSlug("  Hello,   World!  ")
	if got != "hello-world" {
		t.Errorf("получили %q, ждали %q", got, "hello-world")
	}

	long := NormalizeSlug(strings.Repeat("a", SlugMaxLen+50))
	if len(long) != SlugMaxLen {
		t.Errorf("длина %d, ждали %d", len(long), SlugMaxLen)
	}
	if strings.HasPrefix(long, "-") || strings.HasSuffix(long, "-") {
		t.Error("слаг не должен начинаться или заканчиваться дефисом")
	}
}

func TestSlugCandidates(t *testing.T) {
	got := SlugCandidates("boss", 3)
	want := []string{"boss", "boss-2", "boss-3"}

	if len(got) != len(want) {
		t.Fatalf("получили %d вариантов, ждали %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("вариант %d: получили %q, ждали %q", i, got[i], want[i])
		}
	}
}

// Пустая заготовка получается из адреса вида «иван@…» — вариант всё равно
// обязан быть допустимым слагом, иначе регистрация по приглашению падает.
func TestSlugCandidatesFallback(t *testing.T) {
	for _, base := range []string{"", "ab"} {
		for _, candidate := range SlugCandidates(base, 3) {
			if len(candidate) < SlugMinLen {
				t.Errorf("вариант %q короче минимума", candidate)
			}
		}
	}
}

// Суффикс не должен вытолкнуть слаг за предел длины.
func TestSlugCandidatesRespectMaxLen(t *testing.T) {
	base := strings.Repeat("a", SlugMaxLen)

	for _, candidate := range SlugCandidates(base, 5) {
		if len(candidate) > SlugMaxLen {
			t.Errorf("вариант длиной %d превышает предел %d", len(candidate), SlugMaxLen)
		}
	}
}
