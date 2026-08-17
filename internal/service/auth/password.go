package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Hasher считает и проверяет хэши паролей.
//
// argon2id, а не bcrypt: bcrypt молча обрезает вход на 72 байтах, и при
// разрешённых 128 символах часть пароля просто не участвовала бы в проверке —
// без единого признака в логах.
type Hasher struct {
	memoryKiB uint32
	time      uint32
	threads   uint8
}

const (
	saltLen = 16
	keyLen  = 32
)

// NewHasher создаёт хэшер с заданными параметрами argon2id.
func NewHasher(memoryKiB, timeCost uint32, threads uint8) *Hasher {
	return &Hasher{memoryKiB: memoryKiB, time: timeCost, threads: threads}
}

// Hash возвращает строку в стандартном формате PHC. Параметры хранятся
// внутри неё, поэтому ужесточение настроек не обесценивает старые хэши:
// они продолжают проверяться со своими значениями.
func (h *Hasher) Hash(password string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate salt: %w", err)
	}

	key := argon2.IDKey([]byte(password), salt, h.time, h.memoryKiB, h.threads, keyLen)

	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, h.memoryKiB, h.time, h.threads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

var errBadHash = errors.New("malformed password hash")

// Verify сравнивает пароль с сохранённым хэшем.
func (h *Hasher) Verify(password, encoded string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, errBadHash
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false, errBadHash
	}

	var memory, timeCost uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &timeCost, &threads); err != nil {
		return false, errBadHash
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, errBadHash
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false, errBadHash
	}
	// Длина берётся из строки хэша, то есть в конечном счёте из базы.
	// Без проверки её пришлось бы приводить к uint32 вслепую, а argon2
	// с абсурдной длиной ключа — это выделение памяти по чужому указанию.
	if len(want) < 16 || len(want) > 1024 {
		return false, errBadHash
	}

	got := argon2.IDKey([]byte(password), salt, timeCost, memory, threads, uint32(len(want))) //nolint:gosec // длина проверена выше: 16..1024

	// Сравнение за постоянное время: обычное == выходит по первому
	// несовпавшему байту, и по времени ответа хэш подбирается побайтово.
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}
