// Package localfs хранит файлы на локальном диске.
package localfs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Store хранит объекты в каталоге на диске.
type Store struct {
	root    string
	baseURL string
}

// New создаёт хранилище, при необходимости заводя корневой каталог.
func New(root, baseURL string) (*Store, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve storage root: %w", err)
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return nil, fmt.Errorf("create storage root: %w", err)
	}
	return &Store{root: abs, baseURL: strings.TrimRight(baseURL, "/")}, nil
}

var errBadKey = errors.New("invalid object key")

// resolve переводит ключ в путь на диске.
//
// Ключи приходят из базы, но попадают туда из пользовательского ввода,
// поэтому проверка обязательна: "../../etc/passwd" в ключе — это чтение
// и запись где угодно на диске. Сначала чистим ключ, потом сверяем,
// что итоговый путь не вышел за корень.
func (s *Store) resolve(key string) (string, error) {
	if key == "" {
		return "", errBadKey
	}
	clean := path.Clean("/" + key)
	full := filepath.Join(s.root, filepath.FromSlash(clean))

	if !strings.HasPrefix(full, s.root+string(filepath.Separator)) {
		return "", errBadKey
	}
	return full, nil
}

// Put кладёт объект по ключу, перезаписывая существующий.
func (s *Store) Put(_ context.Context, key string, r io.Reader, _ string) error {
	full, err := s.resolve(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return fmt.Errorf("create object dir: %w", err)
	}

	// Пишем во временный файл и переименовываем: иначе оборванная загрузка
	// оставляет по постоянному адресу битую картинку, и клиенты получают
	// её из кэша ещё долго.
	tmp, err := os.CreateTemp(filepath.Dir(full), ".upload-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}()

	if _, err := io.Copy(tmp, r); err != nil {
		return fmt.Errorf("write object: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close object: %w", err)
	}
	if err := os.Rename(tmpName, full); err != nil {
		return fmt.Errorf("commit object: %w", err)
	}
	return nil
}

// Get открывает объект на чтение.
func (s *Store) Get(_ context.Context, key string) (io.ReadCloser, error) {
	full, err := s.resolve(key)
	if err != nil {
		return nil, err
	}
	// Путь собран resolve: ключ очищен от "..", а результат проверен
	// на принадлежность корню хранилища. Открывать здесь больше нечего.
	f, err := os.Open(full) //nolint:gosec // путь валидирован в resolve
	if err != nil {
		return nil, fmt.Errorf("open object: %w", err)
	}
	return f, nil
}

// Delete удаляет объект; отсутствие объекта ошибкой не считается.
func (s *Store) Delete(_ context.Context, key string) error {
	full, err := s.resolve(key)
	if err != nil {
		return err
	}
	if err := os.Remove(full); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("delete object: %w", err)
	}
	return nil
}

// URL собирает публичный адрес объекта.
func (s *Store) URL(key string) string {
	if key == "" {
		return ""
	}
	return s.baseURL + "/" + strings.TrimPrefix(path.Clean("/"+key), "/")
}
