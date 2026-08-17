// Package render рисует карточку визитки.
//
// Карточка собирается кодом, а не снимается с HTML-страницы браузером:
// макет фиксированный и простой, а headless-браузер стоил бы сотен мегабайт
// памяти, секунды на карточку и отдельного процесса со своим жизненным
// циклом. Здесь это миллисекунды и детерминированный результат, который
// можно сравнивать с эталоном в тестах.
//
// PNG и SVG выходят из ОДНОГО набора команд рисования: расходиться им
// физически нечем.
package render

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"strings"

	"github.com/tdewolff/canvas"
	"github.com/tdewolff/canvas/renderers/rasterizer"
	svgrenderer "github.com/tdewolff/canvas/renderers/svg"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"
)

// Version меняется при любой правке макета.
//
// Она входит в хэш, которым именуется файл карточки, поэтому правка
// раскладки разом обесценивает все ранее отрисованные картинки — без
// ручной чистки хранилища и без «почему у половины сотрудников старый
// дизайн».
const Version = "1"

// Размеры макета в логических единицах. PNG растрируется с двукратным
// увеличением, поэтому единица здесь — это пиксель при обычной плотности.
const (
	cardWidth  = 520.0
	cardHeight = 190.0

	padding       = 24.0
	avatarSize    = 118.0
	avatarLeft    = 26.0
	contentLeft   = avatarLeft + avatarSize + 26.0
	logoMaxHeight = 26.0
	logoMaxWidth  = 150.0

	pngScale = 2.0

	// Размер шрифта в canvas задаётся в пунктах, а единица нашего макета —
	// пиксель при обычной плотности. Коэффициент переводит одно в другое,
	// чтобы в раскладке можно было писать привычные кегли.
	ptPerUnit = 72.0 / 25.4
)

var (
	colorBackground = color.RGBA{255, 255, 255, 255}
	colorBorder     = color.RGBA{228, 231, 236, 255}
	colorPrimary    = color.RGBA{26, 26, 26, 255}
	colorAccent     = color.RGBA{110, 86, 207, 255}
	colorMuted      = color.RGBA{110, 116, 128, 255}
	colorFaint      = color.RGBA{140, 146, 158, 255}
	colorAvatarBg   = color.RGBA{236, 238, 242, 255}
)

// Card — данные, из которых рисуется карточка.
//
// Готовая структура, а не domain.Profile: рендерер не должен знать
// ни про статусы, ни про права доступа, ни про то, откуда взялись картинки.
type Card struct {
	FullName string
	Position string
	Tags     []string

	Phone    string
	Telegram string
	WhatsApp string

	CompanyName string

	Avatar image.Image
	Logo   image.Image
}

// Renderer рисует карточки.
//
// Шрифты загружаются один раз при создании: разбор TTF стоит заметно
// дороже самой отрисовки, и делать его на каждый запрос — значит платить
// за него на каждой карточке.
type Renderer struct {
	family *canvas.FontFamily
}

// New создаёт рендерер и загружает шрифты.
//
// Шрифты берутся из модуля, а не из файла на диске: иначе в образе
// появляется ещё один путь, за наличием которого надо следить, и карточка
// ломается не при сборке, а при первом запросе — квадратами вместо букв.
// Семейство Go покрывает кириллицу, латиницу и знаки препинания,
// которые встречаются в именах и должностях.
func New() (*Renderer, error) {
	family := canvas.NewFontFamily("card")

	if err := family.LoadFont(goregular.TTF, 0, canvas.FontRegular); err != nil {
		return nil, fmt.Errorf("load regular font: %w", err)
	}
	if err := family.LoadFont(gobold.TTF, 0, canvas.FontBold); err != nil {
		return nil, fmt.Errorf("load bold font: %w", err)
	}

	return &Renderer{family: family}, nil
}

// PNG рисует карточку и кодирует её в PNG.
//
// PNG, а не JPEG: на карточке текст, логотип и плоские заливки — JPEG
// оставил бы звон вокруг букв.
func (r *Renderer) PNG(card Card) ([]byte, error) {
	c := r.draw(card)

	img := rasterizer.Draw(c, canvas.DPMM(pngScale), canvas.DefaultColorSpace)

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, fmt.Errorf("encode png: %w", err)
	}
	return buf.Bytes(), nil
}

// SVG рисует ту же карточку в вектор.
//
// Нужен фронту для живого предпросмотра: страница вставляет его прямо
// в DOM и не дублирует вёрстку карточки у себя. Шрифт вкомпилируется
// подмножеством — только использованные глифы, иначе один файл весил бы
// сотни килобайт вместо десятка.
func (r *Renderer) SVG(card Card) ([]byte, error) {
	c := r.draw(card)

	var buf bytes.Buffer
	out := svgrenderer.New(&buf, c.W, c.H, &svgrenderer.Options{
		EmbedFonts:  true,
		SubsetFonts: true,
		SizeUnits:   "px",
	})
	c.RenderTo(out)

	if err := out.Close(); err != nil {
		return nil, fmt.Errorf("encode svg: %w", err)
	}
	return buf.Bytes(), nil
}

// draw выполняет саму раскладку.
//
// Начало координат у canvas в левом нижнем углу, поэтому все вертикальные
// величины отсчитываются от cardHeight — так текст описывается сверху вниз,
// как он и читается.
func (r *Renderer) draw(card Card) *canvas.Canvas {
	c := canvas.New(cardWidth, cardHeight)
	ctx := canvas.NewContext(c)

	r.drawBackground(ctx)
	r.drawAvatar(ctx, card.Avatar)

	y := cardHeight - padding

	if card.Logo != nil {
		y = r.drawLogo(ctx, card.Logo, y)
	} else if card.CompanyName != "" {
		// Без логотипа его место занимает название компании — иначе
		// карточка выглядит обрезанной сверху.
		face := r.face(15, colorPrimary, canvas.FontBold)
		y -= face.Metrics().Ascent
		ctx.DrawText(contentLeft, y, canvas.NewTextLine(face, card.CompanyName, canvas.Left))
		y -= face.Metrics().Descent
	}

	y -= 14
	y = r.drawNameAndPosition(ctx, card, y)

	if len(card.Tags) > 0 {
		y -= 12
		y = r.drawTags(ctx, card.Tags, y)
	}

	y -= 14
	r.drawContacts(ctx, card, y)

	return c
}

func (r *Renderer) drawBackground(ctx *canvas.Context) {
	ctx.SetFillColor(colorBackground)
	ctx.SetStrokeColor(colorBorder)
	ctx.SetStrokeWidth(1)

	ctx.DrawPath(0, 0, canvas.RoundedRectangle(cardWidth, cardHeight, 14))
}

// drawAvatar рисует фотографию, обрезанную в круг.
//
// Круг делается здесь, а не в самом файле: PNG-карточка кладётся
// на непрозрачный фон, и прозрачные углы в исходнике дали бы чёрные
// квадраты вокруг лица.
func (r *Renderer) drawAvatar(ctx *canvas.Context, img image.Image) {
	cx := avatarLeft + avatarSize/2
	cy := cardHeight / 2

	if img == nil {
		ctx.SetFillColor(colorAvatarBg)
		ctx.SetStrokeColor(canvas.Transparent)
		ctx.DrawPath(cx, cy, canvas.Circle(avatarSize/2))
		return
	}

	bounds := img.Bounds()
	side := float64(min(bounds.Dx(), bounds.Dy()))
	if side == 0 {
		return
	}
	// Изображение уже квадратное и с вшитой круглой маской — PrepareAvatar
	// сделал это заранее, поэтому здесь остаётся только масштаб.
	resolution := canvas.Resolution(side / avatarSize)
	ctx.DrawImage(avatarLeft, cy-avatarSize/2, img, resolution)
}

func (r *Renderer) drawLogo(ctx *canvas.Context, img image.Image, y float64) float64 {
	bounds := img.Bounds()
	w, h := float64(bounds.Dx()), float64(bounds.Dy())
	if w == 0 || h == 0 {
		return y
	}

	// Логотипы приходят любых пропорций: вписываем в рамку, не искажая.
	scale := min(logoMaxHeight/h, logoMaxWidth/w)
	drawH := h * scale

	ctx.Push()
	defer ctx.Pop()

	ctx.DrawImage(contentLeft, y-drawH, img, canvas.Resolution(1/scale))
	return y - drawH
}

func (r *Renderer) drawNameAndPosition(ctx *canvas.Context, card Card, y float64) float64 {
	nameFace := r.face(20, colorPrimary, canvas.FontBold)
	posFace := r.face(14, colorAccent, canvas.FontRegular)

	available := cardWidth - contentLeft - padding

	name := card.FullName
	if name == "" {
		name = "—"
	}

	// Имя важнее должности: под должность отдаём то, что осталось,
	// а имя урезаем только если оно само не помещается целиком.
	nameWidth := nameFace.TextWidth(name)
	if nameWidth > available {
		name = truncate(nameFace, name, available)
		nameWidth = nameFace.TextWidth(name)
	}

	y -= nameFace.Metrics().Ascent
	ctx.DrawText(contentLeft, y, canvas.NewTextLine(nameFace, name, canvas.Left))

	if card.Position != "" {
		sepFace := r.face(14, colorFaint, canvas.FontRegular)
		sep := " | "
		rest := available - nameWidth - sepFace.TextWidth(sep)

		if rest > 30 {
			ctx.DrawText(contentLeft+nameWidth, y, canvas.NewTextLine(sepFace, sep, canvas.Left))
			ctx.DrawText(contentLeft+nameWidth+sepFace.TextWidth(sep), y,
				canvas.NewTextLine(posFace, truncate(posFace, card.Position, rest), canvas.Left))
		} else {
			// Не поместилась рядом — переносим на свою строку, а не
			// обрезаем до трёх букв.
			y -= nameFace.Metrics().Descent + posFace.Metrics().Ascent + 6
			ctx.DrawText(contentLeft, y,
				canvas.NewTextLine(posFace, truncate(posFace, card.Position, available), canvas.Left))
		}
	}

	return y - nameFace.Metrics().Descent
}

func (r *Renderer) drawTags(ctx *canvas.Context, tags []string, y float64) float64 {
	face := r.face(12, colorMuted, canvas.FontRegular)
	available := cardWidth - contentLeft - padding

	// Разделитель с одиночными пробелами: SVG схлопывает подряд идущие
	// пробелы, и раскладка с двойными разъезжалась бы между PNG и вектором.
	line := strings.Join(tags, " · ")
	if face.TextWidth(line) > available {
		line = truncate(face, line, available)
	}

	y -= face.Metrics().Ascent
	ctx.DrawText(contentLeft, y, canvas.NewTextLine(face, line, canvas.Left))
	return y - face.Metrics().Descent
}

func (r *Renderer) drawContacts(ctx *canvas.Context, card Card, y float64) {
	labelFace := r.face(11, colorPrimary, canvas.FontBold)
	valueFace := r.face(11, colorMuted, canvas.FontRegular)
	available := cardWidth - contentLeft - padding

	type row struct{ label, value string }

	rows := make([]row, 0, 3)
	if card.Phone != "" {
		rows = append(rows, row{"Телефон:", card.Phone})
	}
	if card.Telegram != "" {
		rows = append(rows, row{"Telegram:", "@" + card.Telegram})
	}
	if card.WhatsApp != "" {
		rows = append(rows, row{"WhatsApp:", card.WhatsApp})
	}

	for _, item := range rows {
		y -= labelFace.Metrics().Ascent
		ctx.DrawText(contentLeft, y, canvas.NewTextLine(labelFace, item.label, canvas.Left))

		offset := labelFace.TextWidth(item.label + " ")
		ctx.DrawText(contentLeft+offset, y,
			canvas.NewTextLine(valueFace, truncate(valueFace, item.value, available-offset), canvas.Left))

		y -= labelFace.Metrics().Descent + 4
	}
}

func (r *Renderer) face(size float64, col color.Color, style canvas.FontStyle) *canvas.FontFace {
	return r.family.Face(size*ptPerUnit, col, style, canvas.FontNormal)
}

// truncate обрезает строку по ширине, добавляя многоточие.
//
// Обрезается по рунам, а не по байтам: срез посреди двухбайтной буквы
// превратил бы кириллицу в мусор.
func truncate(face *canvas.FontFace, s string, maxWidth float64) string {
	if maxWidth <= 0 {
		return ""
	}
	if face.TextWidth(s) <= maxWidth {
		return s
	}

	const ellipsis = "…"
	runes := []rune(s)

	for i := len(runes) - 1; i > 0; i-- {
		candidate := strings.TrimRight(string(runes[:i]), " ") + ellipsis
		if face.TextWidth(candidate) <= maxWidth {
			return candidate
		}
	}
	return ellipsis
}
