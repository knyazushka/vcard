package render

import (
	"image"
	"image/color"

	xdraw "golang.org/x/image/draw"

	"github.com/knyazushka/vcard/internal/domain"
)

// avatarPixels — сторона подготовленного аватара в пикселях.
//
// Вдвое больше макетного размера, чтобы на PNG удвоенной плотности
// фотография не мылилась.
const avatarPixels = 256

// PrepareAvatar вырезает область кадрирования, приводит её к квадрату
// и закрашивает всё за пределами круга фоном карточки.
//
// Круг делается здесь, а не при выводе: у canvas клип только прямоугольный,
// а главное — маска, вшитая в пиксели, одинаково ведёт себя и в PNG,
// и в SVG. Фон непрозрачный намеренно: на прозрачных углах PNG-карточка
// получила бы чёрные квадраты вокруг лица.
func PrepareAvatar(src image.Image, crop *domain.Crop) image.Image {
	if src == nil {
		return nil
	}

	src = applyCrop(src, crop)

	dst := image.NewRGBA(image.Rect(0, 0, avatarPixels, avatarPixels))

	// CatmullRom, а не NearestNeighbor: фотографию уменьшают в разы,
	// и дешёвая интерполяция даёт рваные края и муар на волосах.
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), xdraw.Over, nil)

	applyCircleMask(dst, colorBackground)
	return dst
}

// applyCrop вырезает заданную пользователем область.
//
// Область приходит в координатах исходного файла и могла быть посчитана
// для другой картинки — например, если фотографию заменили, а кроп остался.
// Поэтому она приводится к границам изображения, а не принимается на веру.
func applyCrop(src image.Image, crop *domain.Crop) image.Image {
	bounds := src.Bounds()

	rect := squareCenter(bounds)
	if crop != nil && crop.Size > 0 {
		requested := image.Rect(
			bounds.Min.X+crop.X,
			bounds.Min.Y+crop.Y,
			bounds.Min.X+crop.X+crop.Size,
			bounds.Min.Y+crop.Y+crop.Size,
		).Intersect(bounds)

		// Пустое пересечение означает, что кроп целиком за пределами
		// картинки: берём центр, а не отдаём пустоту.
		if !requested.Empty() {
			rect = requested
		}
	}

	if sub, ok := src.(interface {
		SubImage(image.Rectangle) image.Image
	}); ok {
		return sub.SubImage(rect)
	}

	out := image.NewRGBA(image.Rect(0, 0, rect.Dx(), rect.Dy()))
	xdraw.Draw(out, out.Bounds(), src, rect.Min, xdraw.Src)
	return out
}

// squareCenter — наибольший квадрат по центру изображения.
// Используется, когда кроп не задан: обрезать по центру привычнее,
// чем растягивать портрет в квадрат.
func squareCenter(b image.Rectangle) image.Rectangle {
	side := min(b.Dx(), b.Dy())
	offX := (b.Dx() - side) / 2
	offY := (b.Dy() - side) / 2

	return image.Rect(
		b.Min.X+offX, b.Min.Y+offY,
		b.Min.X+offX+side, b.Min.Y+offY+side,
	)
}

// applyCircleMask закрашивает углы фоном.
//
// Край сглаживается по доле пикселя, попавшей внутрь круга: без этого
// окружность на растре выходит зазубренной, и это первое, что бросается
// в глаза на готовой карточке.
func applyCircleMask(img *image.RGBA, bg color.RGBA) {
	b := img.Bounds()
	cx := float64(b.Dx()) / 2
	cy := float64(b.Dy()) / 2
	radius := cx

	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			dx := float64(x-b.Min.X) + 0.5 - cx
			dy := float64(y-b.Min.Y) + 0.5 - cy
			dist := dx*dx + dy*dy

			switch {
			case dist <= (radius-1)*(radius-1):
				continue
			case dist >= (radius+1)*(radius+1):
				img.SetRGBA(x, y, bg)
			default:
				// Переходная полоса шириной в два пикселя: доля фона
				// растёт линейно от внутреннего края к внешнему.
				d := sqrt(dist)
				t := (d - (radius - 1)) / 2
				img.SetRGBA(x, y, blend(img.RGBAAt(x, y), bg, t))
			}
		}
	}
}

func blend(fg, bg color.RGBA, t float64) color.RGBA {
	if t < 0 {
		t = 0
	}
	if t > 1 {
		t = 1
	}
	mix := func(a, b uint8) uint8 {
		return uint8(float64(a)*(1-t) + float64(b)*t)
	}
	return color.RGBA{
		R: mix(fg.R, bg.R),
		G: mix(fg.G, bg.G),
		B: mix(fg.B, bg.B),
		A: 255,
	}
}

// sqrt без math: вызывается на кольце шириной в два пикселя, но всё же
// миллионы раз за карточку — держим его тривиальным и без аллокаций.
func sqrt(x float64) float64 {
	if x <= 0 {
		return 0
	}
	guess := x
	for range 8 {
		guess = 0.5 * (guess + x/guess)
	}
	return guess
}

// PrepareLogo приводит логотип к непрозрачному фону карточки.
//
// Логотипы часто приходят PNG с альфой; на белой карточке это не заметно,
// но при выводе в PNG без учёта фона полупрозрачные края темнеют.
func PrepareLogo(src image.Image) image.Image {
	if src == nil {
		return nil
	}

	b := src.Bounds()
	out := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))

	xdraw.Draw(out, out.Bounds(), &image.Uniform{C: colorBackground}, image.Point{}, xdraw.Src)
	xdraw.Draw(out, out.Bounds(), src, b.Min, xdraw.Over)

	return out
}
