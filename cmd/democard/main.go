// Command democard рисует карточку-образец для главной страницы фронта.
//
// Главная показывает, как выглядит карточка, не обращаясь к API: образец
// рисуется тем же рендерером, что и настоящие карточки, и лежит во фронте
// готовым SVG. Данные вымышленные, вместо фотографии — нарисованный
// силуэт: чужое лицо на главной — вопрос согласия, а пустой серый круг
// читается как поломка.
//
// SVG пишется в stdout. Перерисовать после правки макета (render.Version) —
// из репозитория фронта:
//
//	npm run card:demo
package main

import (
	"fmt"
	"image"
	"image/color"
	"os"

	"github.com/knyazushka/vcard/internal/render"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "democard:", err)
		os.Exit(1)
	}
}

func run() error {
	r, err := render.New()
	if err != nil {
		return err
	}

	svg, err := r.SVG(render.Card{
		// Имя и должность короткие: они делят одну строку, и длинная
		// должность ушла бы в многоточие — не лучший вид для образца.
		FullName: "Анна Орлова",
		Position: "Аккаунт-менеджер",
		Tags:     []string{"B2B-продажи", "Переговоры", "CRM"},
		// Заведомо несуществующие контакты: картинку увидит каждый
		// посетитель главной, и настоящий номер получил бы звонки.
		Phone:       "+7 (900) 000-00-00",
		Telegram:    "vcard_demo",
		WhatsApp:    "+7 (900) 000-00-00",
		CompanyName: "Северный ветер",
		Avatar:      render.PrepareAvatar(silhouette(512), nil),
	})
	if err != nil {
		return err
	}

	_, err = os.Stdout.Write(svg)
	return err
}

// silhouette рисует условный портрет: голова и плечи на мягком фоне
// в оттенке акцента карточки. Круглую маску наложит PrepareAvatar.
func silhouette(side int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, side, side))
	s := float64(side)

	top := color.RGBA{237, 233, 252, 255}
	bottom := color.RGBA{216, 207, 249, 255}
	figure := color.RGBA{157, 138, 228, 255}

	// Голова — круг, плечи — верхняя половина эллипса, уходящего за край.
	headX, headY, headR := 0.5*s, 0.40*s, 0.18*s
	bodyX, bodyY, bodyRX, bodyRY := 0.5*s, 1.02*s, 0.37*s, 0.36*s

	for y := range side {
		t := float64(y) / s
		bg := color.RGBA{
			R: lerp(top.R, bottom.R, t),
			G: lerp(top.G, bottom.G, t),
			B: lerp(top.B, bottom.B, t),
			A: 255,
		}
		for x := range side {
			fx, fy := float64(x)+0.5, float64(y)+0.5

			hx, hy := fx-headX, fy-headY
			bx, by := (fx-bodyX)/bodyRX, (fy-bodyY)/bodyRY

			if hx*hx+hy*hy <= headR*headR || bx*bx+by*by <= 1 {
				img.SetRGBA(x, y, figure)
			} else {
				img.SetRGBA(x, y, bg)
			}
		}
	}
	return img
}

func lerp(a, b uint8, t float64) uint8 {
	return uint8(float64(a) + (float64(b)-float64(a))*t)
}
