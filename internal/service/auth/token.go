package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// Claims access-токена.
//
// Внутри только «кто» и «какой вход». Ни роли, ни компании: они меняются
// без ведома токена, и любое право, вшитое в подпись, живёт до истечения
// срока. При тенантной модели это означало бы доступ к данным компании
// ещё пятнадцать минут после исключения сотрудника.
type Claims struct {
	jwt.RegisteredClaims
	SessionID uuid.UUID `json:"sid"`
}

// TokenIssuer выписывает и проверяет токены.
type TokenIssuer struct {
	secret     []byte
	accessTTL  time.Duration
	refreshTTL time.Duration
	now        func() time.Time
}

// NewTokenIssuer создаёт выпускающего токены.
func NewTokenIssuer(secret string, accessTTL, refreshTTL time.Duration) *TokenIssuer {
	return &TokenIssuer{
		secret:     []byte(secret),
		accessTTL:  accessTTL,
		refreshTTL: refreshTTL,
		now:        time.Now,
	}
}

// AccessTTL — срок жизни access-токена.
func (t *TokenIssuer) AccessTTL() time.Duration { return t.accessTTL }

// RefreshTTL — срок жизни refresh-токена.
func (t *TokenIssuer) RefreshTTL() time.Duration { return t.refreshTTL }

// IssueAccess подписывает access-токен.
func (t *TokenIssuer) IssueAccess(userID, sessionID uuid.UUID) (string, error) {
	now := t.now()

	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			// Без jti два токена, выписанных одной сессии в пределах
			// секунды, получаются побайтово одинаковыми: все остальные
			// поля совпадают, а подпись детерминирована. Одинаковые токены
			// невозможно различить ни в логах, ни в будущем списке
			// отозванных.
			ID:        uuid.NewString(),
			Subject:   userID.String(),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(t.accessTTL)),
			NotBefore: jwt.NewNumericDate(now),
		},
		SessionID: sessionID,
	}

	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(t.secret)
	if err != nil {
		return "", fmt.Errorf("sign access token: %w", err)
	}
	return signed, nil
}

// ErrInvalidToken — токен не разобран, не прошёл проверку подписи или истёк.
var ErrInvalidToken = errors.New("invalid access token")

// ParseAccess проверяет подпись и срок. В базу не ходит — это и есть смысл
// stateless-токена: проверка стоит одну хэш-операцию.
func (t *TokenIssuer) ParseAccess(token string) (*Claims, error) {
	var claims Claims

	parsed, err := jwt.ParseWithClaims(token, &claims,
		func(*jwt.Token) (any, error) { return t.secret, nil },
		// Алгоритм фиксируется явно. Без этого токен с alg=none или
		// подменённым на асимметричный алгоритмом может быть принят —
		// классическая дыра в разборе JWT.
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithExpirationRequired(),
	)
	if err != nil || !parsed.Valid {
		return nil, ErrInvalidToken
	}
	if claims.Subject == "" || claims.SessionID == uuid.Nil {
		return nil, ErrInvalidToken
	}
	return &claims, nil
}

// UserID достаёт идентификатор пользователя из claims.
func (c *Claims) UserID() (uuid.UUID, error) {
	id, err := uuid.Parse(c.Subject)
	if err != nil {
		return uuid.Nil, ErrInvalidToken
	}
	return id, nil
}

// NewRefreshToken возвращает сам токен и его хэш.
//
// Токен непрозрачный, а не JWT: отозвать подписанный токен невозможно,
// а отзыв — единственное, ради чего refresh существует. В базе хранится
// только sha256, поэтому утечка дампа не даёт войти ни под кем.
func NewRefreshToken() (token string, hash []byte, err error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, fmt.Errorf("generate refresh token: %w", err)
	}
	token = base64.RawURLEncoding.EncodeToString(raw)
	return token, HashRefreshToken(token), nil
}

// HashRefreshToken считает хэш для поиска сессии.
//
// Без соли и без замедления намеренно: токен — 256 бит случайности,
// перебирать нечего, а поиск по индексу должен быть одной операцией.
func HashRefreshToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}
