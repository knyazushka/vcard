// Package avatar готовит обрезанный аватар для публичной страницы.
//
// Кроп хранится метаданными, а исходник остаётся целым — это нужно, чтобы
// кадрирование можно было переиграть. Но публичной странице исходник
// не годится: карточка PNG рисуется с кропом, и, показывая рядом исходник
// по центру, мы получили бы одного человека с двумя разными лицами.
//
// Поэтому здесь тот же приём, что у карточек: имя файла — хэш от всего,
// что влияет на результат, отрисовка ленивая, найденный файл заведомо
// актуален. Устареть, оставшись под тем же именем, он не может.
package avatar

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	"image/png"

	// Форматы, которые принимаются при загрузке. Импортируются ради
	// побочного эффекта — регистрации декодеров.
	_ "image/jpeg"

	_ "golang.org/x/image/webp"

	"github.com/knyazushka/vcard/internal/domain"
	"github.com/knyazushka/vcard/internal/render"
	"github.com/knyazushka/vcard/internal/storage"
)

// version меняется, когда меняется способ подготовки картинки.
// Правка обесценивает все ранее нарезанные файлы разом: их имена
// перестают совпадать, и следующий запрос нарежет заново.
const version = "1"

// Service нарезает аватары и хранит результат.
type Service struct {
	files storage.BlobStore
}

// NewService создаёт сервис аватаров.
func NewService(files storage.BlobStore) *Service {
	return &Service{files: files}
}

// CroppedKey отдаёт ключ обрезанного аватара, нарезая его при первом обращении.
//
// Пустой ключ на входе даёт пустой на выходе: профиль без фотографии —
// обычное дело, и заставлять вызывающего проверять это самому незачем.
//
// Ошибка нарезки не возвращается наверх намеренно: страница визитки
// без фотографии лучше, чем пятисотка из-за того, что файл побился.
// Вызывающий получит исходный ключ — то же, что было до этой правки.
func (s *Service) CroppedKey(ctx context.Context, p domain.Profile) string {
	if p.AvatarKey == "" {
		return ""
	}
	// Без кропа резать нечего: исходник и есть результат, а лишний файл
	// на диске ничем не лучше.
	if p.Crop == nil {
		return p.AvatarKey
	}

	key := fmt.Sprintf("avatars/%s/%s-web.png", p.ID, s.fingerprint(p))

	if s.exists(ctx, key) {
		return key
	}

	src := s.load(ctx, p.AvatarKey)
	if src == nil {
		return p.AvatarKey
	}

	out := render.CropAvatar(src, p.Crop)
	if out == nil {
		return p.AvatarKey
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, out); err != nil {
		return p.AvatarKey
	}
	if err := s.files.Put(ctx, key, bytes.NewReader(buf.Bytes()), "image/png"); err != nil {
		return p.AvatarKey
	}

	return key
}

func (s *Service) exists(ctx context.Context, key string) bool {
	rc, err := s.files.Get(ctx, key)
	if err != nil {
		return false
	}
	_ = rc.Close()
	return true
}

func (s *Service) load(ctx context.Context, key string) image.Image {
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

// fingerprint — отпечаток всего, что влияет на нарезанную картинку.
func (s *Service) fingerprint(p domain.Profile) string {
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "%s\x00%s\x00%d:%d:%d",
		version, p.AvatarKey, p.Crop.X, p.Crop.Y, p.Crop.Size)
	return hex.EncodeToString(h.Sum(nil)[:16])
}
