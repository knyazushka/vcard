// Package s3 хранит файлы в S3-совместимом объектном хранилище.
//
// Бакет может оставаться приватным: наружу файлы по-прежнему отдаёт
// /files, а STORAGE_BASE_URL указывает на API. Если бакет сделать
// публичным и направить STORAGE_BASE_URL прямо в него, ссылки поведут
// туда без правок в коде — адрес собирается из настройки, а в базе
// лежит только ключ.
package s3

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// Config — параметры подключения к бакету.
type Config struct {
	// Endpoint — адрес со схемой; схема определяет, нужен ли TLS.
	Endpoint  string
	Region    string
	Bucket    string
	AccessKey string
	SecretKey string
}

// Store хранит объекты в бакете.
type Store struct {
	client  *minio.Client
	bucket  string
	baseURL string
}

// bucketCheckTimeout ограничивает проверку бакета на старте: зависшее
// хранилище не должно подвешивать запуск процесса без конца.
const bucketCheckTimeout = 10 * time.Second

// New подключается к хранилищу и проверяет, что бакет существует.
//
// Проверка на старте, а не при первой загрузке: опечатка в имени бакета
// или в ключах иначе всплывёт только тогда, когда пользователь уже
// не смог сохранить аватар.
func New(ctx context.Context, cfg Config, baseURL string) (*Store, error) {
	endpoint, err := url.Parse(cfg.Endpoint)
	if err != nil {
		return nil, fmt.Errorf("parse endpoint: %w", err)
	}

	client, err := minio.New(endpoint.Host, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: endpoint.Scheme == "https",
		Region: cfg.Region,
	})
	if err != nil {
		return nil, fmt.Errorf("create s3 client: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, bucketCheckTimeout)
	defer cancel()

	exists, err := client.BucketExists(ctx, cfg.Bucket)
	if err != nil {
		return nil, fmt.Errorf("check bucket %q: %w", cfg.Bucket, err)
	}
	if !exists {
		return nil, fmt.Errorf("bucket %q does not exist", cfg.Bucket)
	}

	return &Store{
		client:  client,
		bucket:  cfg.Bucket,
		baseURL: strings.TrimRight(baseURL, "/"),
	}, nil
}

var errBadKey = errors.New("invalid object key")

// objectName очищает ключ так же, как локальное хранилище: ключ, который
// там не прошёл бы проверку, не должен проходить и здесь, иначе смена
// драйвера меняла бы поведение /files.
func objectName(key string) (string, error) {
	name := strings.TrimPrefix(path.Clean("/"+key), "/")
	if key == "" || name == "" {
		return "", errBadKey
	}
	return name, nil
}

// Put кладёт объект по ключу, перезаписывая существующий.
func (s *Store) Put(ctx context.Context, key string, r io.Reader, contentType string) error {
	name, err := objectName(key)
	if err != nil {
		return err
	}

	body, size, err := sized(r)
	if err != nil {
		return err
	}

	_, err = s.client.PutObject(ctx, s.bucket, name, body, size, minio.PutObjectOptions{
		ContentType: contentType,
		// Имя объекта содержит хэш содержимого, так что по этому адресу
		// оно уже не изменится. Заголовок нужен на случай, когда ссылки
		// ведут прямо в публичный бакет, минуя /files.
		CacheControl: "public, max-age=31536000, immutable",
	})
	if err != nil {
		return fmt.Errorf("put object: %w", err)
	}
	return nil
}

// sized узнаёт длину тела заранее. С неизвестной длиной клиент уходит
// в многочастную загрузку и выделяет буфер под одну часть, а это больше
// полугигабайта ради аватарки в сотню килобайт. Все файлы у нас ограничены
// единицами мегабайт, поэтому проще дочитать тело в память.
func sized(r io.Reader) (io.Reader, int64, error) {
	if l, ok := r.(interface{ Len() int }); ok {
		return r, int64(l.Len()), nil
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, 0, fmt.Errorf("read object: %w", err)
	}
	return bytes.NewReader(data), int64(len(data)), nil
}

// Get открывает объект на чтение.
func (s *Store) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	name, err := objectName(key)
	if err != nil {
		return nil, err
	}

	obj, err := s.client.GetObject(ctx, s.bucket, name, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("get object: %w", err)
	}

	// GetObject ленив: запрос уходит только на первом чтении или Stat.
	// Потребители узнают об отсутствии файла по ошибке Get, а не чтения,
	// поэтому спрашиваем сразу.
	if _, err := obj.Stat(); err != nil {
		_ = obj.Close()
		if minio.ToErrorResponse(err).Code == minio.NoSuchKey {
			return nil, fmt.Errorf("open object: %w", os.ErrNotExist)
		}
		return nil, fmt.Errorf("open object: %w", err)
	}
	return obj, nil
}

// Delete удаляет объект; отсутствие объекта ошибкой не считается —
// S3 и сам отвечает успехом на удаление несуществующего ключа.
func (s *Store) Delete(ctx context.Context, key string) error {
	name, err := objectName(key)
	if err != nil {
		return err
	}
	if err := s.client.RemoveObject(ctx, s.bucket, name, minio.RemoveObjectOptions{}); err != nil {
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
