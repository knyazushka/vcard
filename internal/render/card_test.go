package render

import (
	"bytes"
	"flag"
	"image"
	"image/color"
	"image/png"

	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tdewolff/canvas"

	"github.com/knyazushka/vcard/internal/domain"
)

// -update перерисовывает эталоны. Запускать осознанно и глазами смотреть
// на результат: тест сравнивает байты, но «правильно» решает человек.
var update = flag.Bool("update", false, "перезаписать эталонные изображения")

func testRenderer(t *testing.T) *Renderer {
	t.Helper()

	r, err := New()
	if err != nil {
		t.Fatalf("создать рендерер: %v", err)
	}
	return r
}

func gradientImage(w, h int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.SetRGBA(x, y, color.RGBA{
				R: uint8(40 + x*120/max(w, 1)),
				G: uint8(90 + y*90/max(h, 1)),
				B: 150,
				A: 255,
			})
		}
	}
	return img
}

func checkerImage(w, h int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			if (x/20+y/20)%2 == 0 {
				img.SetRGBA(x, y, color.RGBA{110, 86, 207, 255})
			} else {
				img.SetRGBA(x, y, color.RGBA{240, 90, 120, 255})
			}
		}
	}
	return img
}

func sampleCards() map[string]Card {
	return map[string]Card{
		"full": {
			FullName:    "Лаура Хоффманн",
			Position:    "Руководитель отдела продаж",
			Tags:        []string{"Golang", "DevOps", "Объехала 7 континентов"},
			Phone:       "+491752607358",
			Telegram:    "laura_h",
			WhatsApp:    "+491752607358",
			CompanyName: "Acme GmbH",
			Avatar:      PrepareAvatar(gradientImage(400, 500), &domain.Crop{X: 50, Y: 40, Size: 300}),
			Logo:        PrepareLogo(checkerImage(300, 60)),
		},
		"minimal": {
			FullName:    "Иван Петров",
			CompanyName: "Очень Длинное Название Компании ООО",
		},
		"overflow": {
			FullName:    "Константин Константинопольский-Заднепровский",
			Position:    "Заместитель генерального директора по стратегическому развитию",
			Tags:        []string{"Стратегия", "Развитие бизнеса", "Международные проекты"},
			Phone:       "+7 (999) 123-45-67",
			Telegram:    "konstantin_konstantinopolsky",
			CompanyName: "Рога и Копыта",
			Avatar:      PrepareAvatar(gradientImage(400, 500), nil),
		},
	}
}

// Эталонные картинки — главная защита макета: они ловят то, что не выразишь
// проверкой полей, — съехавший блок, пропавший логотип, изменившийся отступ.
// Их имеет смысл держать именно потому, что отрисовка детерминирована.
func TestGoldenImages(t *testing.T) {
	r := testRenderer(t)

	for name, card := range sampleCards() {
		got, err := r.PNG(card)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}

		path := filepath.Join("testdata", "preview_"+name+".png")

		if *update {
			if err := os.WriteFile(path, got, 0o600); err != nil {
				t.Fatal(err)
			}
			t.Logf("%s: эталон обновлён (%d байт)", name, len(got))
			continue
		}

		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s: нет эталона (перезапустите с -update): %v", name, err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s: изображение разошлось с эталоном (%d байт против %d). "+
				"Если макет менялся намеренно — обновите эталоны флагом -update "+
				"и не забудьте поднять render.Version",
				name, len(got), len(want))
		}
	}
}

// Отрисовка обязана быть детерминированной: на этом стоит и кэш по хэшу,
// и сравнение с эталоном. Недетерминизм здесь означал бы, что карточка
// перерисовывается и перезаписывается на каждом запросе.
func TestRenderIsDeterministic(t *testing.T) {
	r := testRenderer(t)
	card := sampleCards()["full"]

	first, err := r.PNG(card)
	if err != nil {
		t.Fatal(err)
	}
	second, err := r.PNG(card)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(first, second) {
		t.Error("две отрисовки одних данных дали разные байты")
	}
}

func TestPNGIsValidAndSized(t *testing.T) {
	r := testRenderer(t)

	data, err := r.PNG(sampleCards()["full"])
	if err != nil {
		t.Fatal(err)
	}

	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("получился невалидный PNG: %v", err)
	}

	wantW := int(cardWidth * pngScale)
	wantH := int(cardHeight * pngScale)
	if img.Bounds().Dx() != wantW || img.Bounds().Dy() != wantH {
		t.Errorf("размер %dx%d, ждали %dx%d",
			img.Bounds().Dx(), img.Bounds().Dy(), wantW, wantH)
	}
}

// SVG должен быть самодостаточным: страница вставляет его в DOM, и подгрузка
// шрифта извне означала бы, что кириллица иногда рисуется чем попало.
func TestSVGIsSelfContained(t *testing.T) {
	r := testRenderer(t)

	data, err := r.SVG(sampleCards()["full"])
	if err != nil {
		t.Fatal(err)
	}
	out := string(data)

	if !strings.HasPrefix(strings.TrimSpace(out), "<svg") {
		t.Fatal("ответ не начинается с <svg")
	}
	if !strings.Contains(out, "@font-face") {
		t.Error("шрифт не вкомпилирован")
	}
	if !strings.Contains(out, "Лаура Хоффманн") {
		t.Error("текст не попал в SVG — значит, он превращён в кривые и не выделяется")
	}

	// Подмножество глифов: со всем шрифтом файл весит сотни килобайт.
	if len(data) > 60*1024 {
		t.Errorf("SVG весит %d КБ — похоже, шрифт вкомпилирован целиком", len(data)/1024)
	}
}

// Длинные значения обязаны обрезаться, а не вылезать за карточку.
func TestTruncate(t *testing.T) {
	r := testRenderer(t)
	face := r.face(14, colorPrimary, canvas.FontRegular)

	long := strings.Repeat("Длинная должность ", 20)
	got := truncate(face, long, 200)

	if face.TextWidth(got) > 200 {
		t.Errorf("обрезанная строка шире предела: %.1f", face.TextWidth(got))
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("нет многоточия: %q", got)
	}
	// Обрезка по рунам, а не по байтам: иначе кириллица превращается в мусор.
	if !utf8Valid(got) {
		t.Errorf("строка порезана посреди символа: %q", got)
	}

	short := "Дизайнер"
	if truncate(face, short, 200) != short {
		t.Error("короткая строка не должна меняться")
	}
}

func utf8Valid(s string) bool {
	for _, r := range s {
		if r == '�' {
			return false
		}
	}
	return true
}
