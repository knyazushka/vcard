package auth

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

const testSecret = "test-secret-at-least-32-bytes-long!!"

func TestAccessTokenRoundTrip(t *testing.T) {
	issuer := NewTokenIssuer(testSecret, 15*time.Minute, time.Hour)

	userID, sessionID := uuid.New(), uuid.New()

	token, err := issuer.IssueAccess(userID, sessionID)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	claims, err := issuer.ParseAccess(token)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	got, err := claims.UserID()
	if err != nil {
		t.Fatalf("user id: %v", err)
	}
	if got != userID {
		t.Errorf("user id: получили %s, ждали %s", got, userID)
	}
	if claims.SessionID != sessionID {
		t.Errorf("session id: получили %s, ждали %s", claims.SessionID, sessionID)
	}
}

// Два токена одной сессии должны различаться. Без jti все поля совпадают,
// подпись детерминирована, и в пределах секунды получаются побайтово
// одинаковые токены.
func TestAccessTokensAreUnique(t *testing.T) {
	issuer := NewTokenIssuer(testSecret, 15*time.Minute, time.Hour)
	userID, sessionID := uuid.New(), uuid.New()

	first, err := issuer.IssueAccess(userID, sessionID)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	second, err := issuer.IssueAccess(userID, sessionID)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	if first == second {
		t.Fatal("два токена одной сессии совпали")
	}
}

func TestParseRejectsForeignSecret(t *testing.T) {
	issuer := NewTokenIssuer(testSecret, 15*time.Minute, time.Hour)
	other := NewTokenIssuer("another-secret-at-least-32-bytes-ok!", 15*time.Minute, time.Hour)

	token, err := issuer.IssueAccess(uuid.New(), uuid.New())
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	if _, err := other.ParseAccess(token); err == nil {
		t.Fatal("токен, подписанный чужим ключом, принят")
	}
}

func TestParseRejectsExpired(t *testing.T) {
	issuer := NewTokenIssuer(testSecret, time.Minute, time.Hour)
	// Выписываем «в прошлом»: срок истёк две минуты назад.
	issuer.now = func() time.Time { return time.Now().Add(-3 * time.Minute) }

	token, err := issuer.IssueAccess(uuid.New(), uuid.New())
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	issuer.now = time.Now
	if _, err := issuer.ParseAccess(token); err == nil {
		t.Fatal("истёкший токен принят")
	}
}

// Классическая дыра разбора JWT: токен с alg=none принимается, если
// алгоритм не зафиксирован явно при проверке.
func TestParseRejectsAlgNone(t *testing.T) {
	issuer := NewTokenIssuer(testSecret, 15*time.Minute, time.Hour)

	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        uuid.NewString(),
			Subject:   uuid.New().String(),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
		SessionID: uuid.New(),
	}

	unsigned, err := jwt.NewWithClaims(jwt.SigningMethodNone, claims).
		SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("build unsigned token: %v", err)
	}

	if _, err := issuer.ParseAccess(unsigned); err == nil {
		t.Fatal("токен с alg=none принят")
	}
}

func TestParseRejectsGarbage(t *testing.T) {
	issuer := NewTokenIssuer(testSecret, 15*time.Minute, time.Hour)

	for _, token := range []string{"", "garbage", "a.b.c", strings.Repeat("x", 500)} {
		if _, err := issuer.ParseAccess(token); err == nil {
			t.Errorf("мусорный токен %q принят", token)
		}
	}
}

func TestRefreshTokenIsRandomAndHashed(t *testing.T) {
	first, firstHash, err := NewRefreshToken()
	if err != nil {
		t.Fatalf("new refresh: %v", err)
	}
	second, _, err := NewRefreshToken()
	if err != nil {
		t.Fatalf("new refresh: %v", err)
	}

	if first == second {
		t.Fatal("два refresh-токена совпали")
	}

	raw, err := base64.RawURLEncoding.DecodeString(first)
	if err != nil {
		t.Fatalf("токен не декодируется: %v", err)
	}
	if len(raw) != 32 {
		t.Errorf("энтропия токена: %d байт, ждали 32", len(raw))
	}

	// Хэш должен быть воспроизводим — по нему ищется сессия.
	if string(firstHash) != string(HashRefreshToken(first)) {
		t.Error("хэш токена невоспроизводим")
	}
	if string(firstHash) == first {
		t.Error("в хранилище попадёт сам токен, а не его хэш")
	}
}
