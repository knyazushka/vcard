// Package card собирает карточку визитки и кэширует результат.
package card

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	"io"

	// Форматы, которые принимаются при загрузке. Импортируются ради
	// побочного эффекта — регистрации декодеров.
	_ "image/jpeg"
	_ "image/png"

	"github.com/google/uuid"
	_ "golang.org/x/image/webp"

	"github.com/knyazushka/vcard/internal/domain"
	"github.com/knyazushka/vcard/internal/render"
	"github.com/knyazushka/vcard/internal/storage"
)

// Repo — то, что нужно от хранилища профилей.
type Repo interface {
	SetCardKey(ctx context.Context, id uuid.UUID, key string) error
}

// Service рисует карточки и хранит их в объектном хранилище.
type Service struct {
	repo     Repo
	files    storage.BlobStore
	renderer *render.Renderer
}

// NewService создаёт сервис карточек.
func NewService(repo Repo, files storage.BlobStore, renderer *render.Renderer) *Service {
	return &Service{repo: repo, files: files, renderer: renderer}
}

// Format — во что рисуем.
type Format string

// Поддерживаемые форматы карточки.
const (
	FormatPNG Format = "png"
	FormatSVG Format = "svg"
)

// Result — готовая карточка.
type Result struct {
	Body []byte
	// Key — путь в хранилище; для PNG он же попадает в профиль
	// и превращается в постоянную ссылку.
	Key string
	// ETag построен на хэше содержимого, поэтому меняется ровно тогда,
	// когда меняется картинка.
	ETag string
}

// Render отдаёт карточку профиля, рисуя её при первом обращении.
//
// Синхронно, без очереди: отрисовка стоит миллисекунды, а не секунду,
// как стоил бы снимок страницы браузером. Очередь задач, статусы и опрос
// готовности здесь были бы механикой ради механики.
func (s *Service) Render(ctx context.Context, p domain.Profile, format Format) (Result, error) {
	hash := s.fingerprint(p)
	key := fmt.Sprintf("cards/%s/%s.%s", p.ID, hash, format)

	// Имя файла — хэш от данных профиля и версии макета, поэтому
	// найденный файл заведомо актуален: устареть, оставшись под тем же
	// именем, он не может.
	if body, err := s.read(ctx, key); err == nil {
		return Result{Body: body, Key: key, ETag: etag(hash, format)}, nil
	}

	card, err := s.build(ctx, p)
	if err != nil {
		return Result{}, err
	}

	var body []byte
	switch format {
	case FormatSVG:
		body, err = s.renderer.SVG(card)
	default:
		body, err = s.renderer.PNG(card)
	}
	if err != nil {
		return Result{}, err
	}

	if err := s.files.Put(ctx, key, bytes.NewReader(body), contentType(format)); err != nil {
		return Result{}, fmt.Errorf("store card: %w", err)
	}

	// Ссылка на PNG показывается в API и уходит в og:image, поэтому
	// ключ сохраняется в профиле. SVG — вспомогательный формат
	// для предпросмотра, его адрес нигде не публикуется.
	if format == FormatPNG && p.CardKey != key {
		if err := s.repo.SetCardKey(ctx, p.ID, key); err != nil {
			return Result{}, fmt.Errorf("save card key: %w", err)
		}
	}

	return Result{Body: body, Key: key, ETag: etag(hash, format)}, nil
}

func (s *Service) read(ctx context.Context, key string) ([]byte, error) {
	rc, err := s.files.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()

	return io.ReadAll(rc)
}

// build превращает профиль в данные для рисования и подтягивает картинки.
func (s *Service) build(ctx context.Context, p domain.Profile) (render.Card, error) {
	tags := make([]string, 0, len(p.Tags))
	for _, t := range p.Tags {
		tags = append(tags, t.Title)
	}

	card := render.Card{
		FullName:    p.FullName,
		Position:    p.Position.Title,
		Tags:        tags,
		Phone:       p.Contacts.Phone,
		Telegram:    p.Contacts.Telegram,
		WhatsApp:    p.Contacts.WhatsApp,
		CompanyName: p.Company.Name,
	}

	if img := s.loadImage(ctx, p.AvatarKey); img != nil {
		card.Avatar = render.PrepareAvatar(img, p.Crop)
	}
	if img := s.loadImage(ctx, p.Company.LogoKey); img != nil {
		card.Logo = render.PrepareLogo(img)
	}

	return card, nil
}

// loadImage читает и декодирует картинку из хранилища.
//
// Ошибка не прерывает отрисовку: карточка без логотипа лучше, чем пятисотка
// из-за того, что кто-то удалил файл руками. Дырка при этом видна глазом,
// а не спрятана в логах.
func (s *Service) loadImage(ctx context.Context, key string) image.Image {
	if key == "" {
		return nil
	}

	rc, err := s.files.Get(ctx, key)
	if err != nil {
		return nil
	}
	defer func() { _ = rc.Close() }()

	img, _, err := image.Decode(rc)
	if err != nil {
		return nil
	}
	return img
}

// fingerprint — отпечаток всего, что влияет на вид карточки.
//
// Версия макета входит в него первой: правка раскладки обесценивает
// все ранее нарисованные файлы разом, и ни один устаревший не переживёт
// выкатку под своим прежним именем.
func (s *Service) fingerprint(p domain.Profile) string {
	h := sha256.New()

	write := func(parts ...string) {
		for _, part := range parts {
			_, _ = h.Write([]byte(part))
			_, _ = h.Write([]byte{0})
		}
	}

	write(render.Version, p.FullName, p.Position.Title,
		p.Contacts.Phone, p.Contacts.Telegram, p.Contacts.WhatsApp,
		p.Company.Name, p.AvatarKey, p.Company.LogoKey)

	for _, t := range p.Tags {
		write(t.Title)
	}
	if p.Crop != nil {
		write(fmt.Sprintf("%d:%d:%d", p.Crop.X, p.Crop.Y, p.Crop.Size))
	}

	return hex.EncodeToString(h.Sum(nil)[:16])
}

func etag(hash string, format Format) string {
	return fmt.Sprintf(`W/"%s-%s"`, hash, format)
}

// ContentType отдаёт MIME-тип формата.
func ContentType(format Format) string { return contentType(format) }

func contentType(format Format) string {
	if format == FormatSVG {
		return "image/svg+xml"
	}
	return "image/png"
}
