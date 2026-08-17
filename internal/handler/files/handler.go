// Package files раздаёт загруженные файлы.
//
// В OpenAPI-спеке этого маршрута нет намеренно: это доставка статики,
// а не операция API — как /metrics или пробы. Фронт получает готовые
// адреса в ответах и просто подставляет их в <img>.
//
// Обслуживается через BlobStore, а не напрямую с диска: когда хранилище
// переедет в S3, ссылки станут указывать прямо туда, и маршрут просто
// перестанет использоваться — без правок в остальном коде.
package files

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path"
	"strings"

	"github.com/knyazushka/vcard/internal/storage"
)

// New собирает обработчик, раздающий содержимое хранилища под prefix.
func New(prefix string, store storage.BlobStore, log *slog.Logger) http.Handler {
	prefix = "/" + strings.Trim(prefix, "/") + "/"

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := strings.TrimPrefix(r.URL.Path, prefix)
		if key == "" || key == r.URL.Path {
			http.NotFound(w, r)
			return
		}

		body, err := store.Get(r.Context(), key)
		if err != nil {
			// Отсутствие файла и попытка выйти за корень хранилища дают
			// один и тот же ответ: по разнице между ними перебирают
			// содержимое диска.
			if !errors.Is(err, os.ErrNotExist) {
				log.DebugContext(r.Context(), "blob is not available", "key", key, "error", err)
			}
			http.NotFound(w, r)
			return
		}
		defer func() { _ = body.Close() }()

		if ct := contentType(key); ct != "" {
			w.Header().Set("Content-Type", ct)
		}
		// Имя файла содержит хэш содержимого, поэтому по этому адресу оно
		// уже не изменится — здесь вечный кэш не только безопасен,
		// но и обязателен: иначе аватар перезапрашивается на каждой
		// загрузке страницы.
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		// Браузер не должен угадывать тип по содержимому: подобранный
		// им text/html на пользовательском файле — это XSS.
		w.Header().Set("X-Content-Type-Options", "nosniff")

		if _, err := io.Copy(w, body); err != nil {
			log.WarnContext(r.Context(), "failed to stream blob", "key", key, "error", err)
		}
	})
}

// contentType определяется по расширению ключа, а не по содержимому:
// расширение проставили мы сами при загрузке, после проверки сигнатуры,
// и доверять ему здесь можно.
func contentType(key string) string {
	switch strings.ToLower(path.Ext(key)) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".webp":
		return "image/webp"
	case ".svg":
		return "image/svg+xml"
	default:
		return ""
	}
}
