// Package config читает конфигурацию из окружения.
//
// Только из окружения: никаких config.yaml с дефолтами, различающимися между
// стендами. Это единственное место в коде, которому позволено смотреть
// в os.Getenv — дальше по программе ходит уже разобранная структура.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/caarlos0/env/v11"
)

// Config — вся конфигурация приложения, разобранная из окружения.
type Config struct {
	Env      string `env:"APP_ENV"      envDefault:"development"`
	LogLevel string `env:"LOG_LEVEL"    envDefault:"info"`

	HTTP    HTTP
	DB      DB
	Auth    Auth
	Storage Storage
	Mail    Mail
	Public  Public
}

// HTTP — настройки обоих слушателей: публичного API и служебного порта.
type HTTP struct {
	Addr            string        `env:"HTTP_ADDR"             envDefault:":8080"`
	ReadTimeout     time.Duration `env:"HTTP_READ_TIMEOUT"     envDefault:"10s"`
	WriteTimeout    time.Duration `env:"HTTP_WRITE_TIMEOUT"    envDefault:"30s"`
	IdleTimeout     time.Duration `env:"HTTP_IDLE_TIMEOUT"     envDefault:"60s"`
	ShutdownTimeout time.Duration `env:"HTTP_SHUTDOWN_TIMEOUT" envDefault:"15s"`
	// Отдельный порт для проб и метрик: они не должны быть доступны
	// снаружи вместе с публичным API и не должны падать вместе с ним
	// под нагрузкой.
	AdminAddr string `env:"HTTP_ADMIN_ADDR" envDefault:":9090"`
	// Доверять ли X-Request-Id из входящего запроса. По умолчанию нет:
	// значение попадает в логи, и без доверенного прокси перед сервисом
	// им управляет кто угодно.
	TrustProxyHeaders bool `env:"TRUST_PROXY_HEADERS" envDefault:"false"`
	// Флаг Secure у refresh-cookie. По умолчанию поднят, в том числе
	// в разработке: http://localhost Chrome и Firefox считают доверенным
	// origin, и Secure-cookie там работает. Опускать приходится разве что
	// ради Safari — и это должно быть осознанным действием, а не молчаливым
	// следствием APP_ENV.
	CookieSecure bool `env:"COOKIE_SECURE" envDefault:"true"`
}

// DB — подключение к Postgres и размеры пула.
type DB struct {
	DSN             string        `env:"DATABASE_URL,required"`
	MaxConns        int32         `env:"DB_MAX_CONNS"         envDefault:"10"`
	MinConns        int32         `env:"DB_MIN_CONNS"         envDefault:"2"`
	MaxConnLifetime time.Duration `env:"DB_MAX_CONN_LIFETIME" envDefault:"1h"`
	ConnectTimeout  time.Duration `env:"DB_CONNECT_TIMEOUT"   envDefault:"5s"`
}

// Auth — параметры токенов и хеширования паролей.
type Auth struct {
	// Ключ подписи access-токенов. Обязателен и без дефолта: дефолтный
	// секрет — это отсутствие подписи, просто незаметное.
	JWTSecret      string        `env:"JWT_SECRET,required"`
	AccessTTL      time.Duration `env:"ACCESS_TOKEN_TTL"  envDefault:"15m"`
	RefreshTTL     time.Duration `env:"REFRESH_TOKEN_TTL" envDefault:"720h"`
	ArgonMemoryKiB uint32        `env:"ARGON_MEMORY_KIB"  envDefault:"65536"`
	ArgonTime      uint32        `env:"ARGON_TIME"        envDefault:"3"`
	ArgonThreads   uint8         `env:"ARGON_THREADS"     envDefault:"4"`
}

// Storage — где лежат загруженные файлы и каковы пределы их размера.
type Storage struct {
	// local — файлы на диске, s3 — объектное хранилище.
	// Локальный вариант делает процесс не stateless: вторую реплику с ним
	// поднимать нельзя, половина аватарок окажется не на том поде.
	// Осознанный долг первого этапа, изолированный интерфейсом BlobStore.
	Driver    string `env:"STORAGE_DRIVER" envDefault:"local"`
	LocalPath string `env:"STORAGE_LOCAL_PATH" envDefault:"./var/blobs"`
	// Базовый адрес, по которому отдаются загруженные файлы.
	BaseURL string `env:"STORAGE_BASE_URL" envDefault:"http://localhost:8080/files"`

	MaxAvatarBytes int64 `env:"MAX_AVATAR_BYTES" envDefault:"5242880"`
	MaxLogoBytes   int64 `env:"MAX_LOGO_BYTES"   envDefault:"2097152"`
}

// Mail — отправка почты и режим работы outbox-рассыльщика.
type Mail struct {
	SMTPAddr           string        `env:"SMTP_ADDR"  envDefault:"localhost:1025"`
	SMTPUser           string        `env:"SMTP_USER"`
	SMTPPass           string        `env:"SMTP_PASSWORD"`
	From               string        `env:"MAIL_FROM"  envDefault:"no-reply@vcard.local"`
	OutboxPollInterval time.Duration `env:"OUTBOX_POLL_INTERVAL" envDefault:"5s"`
	OutboxBatchSize    int           `env:"OUTBOX_BATCH_SIZE"    envDefault:"20"`
}

// Public — то, что видно снаружи: адрес фронта и срок жизни приглашений.
type Public struct {
	// Адрес фронта. Ссылки в письмах собираются ТОЛЬКО отсюда и никогда
	// из заголовка Host запроса: иначе подделанный Host уезжает в письмо
	// жертве вместе с вашим текстом и чужой ссылкой.
	AppURL string `env:"PUBLIC_APP_URL,required"`
	// Срок жизни приглашения.
	InvitationTTL time.Duration `env:"INVITATION_TTL" envDefault:"168h"`
}

// Load читает окружение и проверяет полученную конфигурацию.
func Load() (*Config, error) {
	var cfg Config
	if err := env.Parse(&cfg); err != nil {
		return nil, fmt.Errorf("parse env: %w", err)
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// validate ловит то, что тегами не выражается. Падать на старте с внятным
// сообщением дешевле, чем обнаружить кривой адрес в момент отправки письма.
func (c *Config) validate() error {
	var errs []error

	if _, err := url.ParseRequestURI(c.Public.AppURL); err != nil {
		errs = append(errs, fmt.Errorf("PUBLIC_APP_URL: %w", err))
	}
	if c.Storage.Driver != "local" && c.Storage.Driver != "s3" {
		errs = append(errs, fmt.Errorf("STORAGE_DRIVER: unknown driver %q", c.Storage.Driver))
	}
	if len(c.Auth.JWTSecret) < 32 {
		errs = append(errs, errors.New("JWT_SECRET: must be at least 32 bytes"))
	}
	if c.DB.MinConns > c.DB.MaxConns {
		errs = append(errs, errors.New("DB_MIN_CONNS must not exceed DB_MAX_CONNS"))
	}

	return errors.Join(errs...)
}

// IsProduction сообщает, работаем ли мы на боевом стенде.
func (c *Config) IsProduction() bool { return c.Env == "production" }
