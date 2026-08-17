// Package auth содержит сценарии аутентификации.
//
// Интерфейсы хранилищ объявлены здесь, у потребителя, а не в пакете
// репозитория: тогда сервис не зависит от реализации, а моки в тестах
// остаются локальными.
package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/knyazushka/vcard/internal/domain"
)

// UserRepo — то, что сценариям нужно от хранилища пользователей.
type UserRepo interface {
	Create(ctx context.Context, u domain.User) error
	ByEmail(ctx context.Context, email string) (domain.User, error)
	ByID(ctx context.Context, id uuid.UUID) (domain.User, error)
	Memberships(ctx context.Context, userID uuid.UUID) ([]domain.Membership, error)
}

// SessionRepo — то, что сценариям нужно от хранилища сессий.
type SessionRepo interface {
	Create(ctx context.Context, s domain.Session, tokenHash []byte) error
	ByTokenHash(ctx context.Context, hash []byte) (domain.Session, error)
	MarkUsed(ctx context.Context, id uuid.UUID, at time.Time) error
	RevokeFamily(ctx context.Context, familyID uuid.UUID, at time.Time) error
	RevokeAllForUser(ctx context.Context, userID uuid.UUID, at time.Time) error
	// RotateWithinFamily атомарно помечает старую сессию использованной
	// и заводит новую в той же цепочке.
	RotateWithinFamily(ctx context.Context, oldID uuid.UUID, next domain.Session, nextHash []byte, at time.Time) error
}

// Service — сценарии аутентификации.
type Service struct {
	users    UserRepo
	sessions SessionRepo
	hasher   *Hasher
	tokens   *TokenIssuer
	log      *slog.Logger
	now      func() time.Time
}

// NewService собирает сценарии аутентификации.
func NewService(users UserRepo, sessions SessionRepo, hasher *Hasher, tokens *TokenIssuer, log *slog.Logger) *Service {
	return &Service{
		users:    users,
		sessions: sessions,
		hasher:   hasher,
		tokens:   tokens,
		log:      log,
		now:      time.Now,
	}
}

// Tokens — то, что уходит клиенту после успешного входа.
type Tokens struct {
	Access     string
	AccessTTL  time.Duration
	Refresh    string
	RefreshTTL time.Duration
}

// ClientInfo — необязательные сведения о клиенте для списка сессий.
type ClientInfo struct {
	UserAgent string
	IP        string
}

// Register заводит учётку и сразу открывает сессию.
func (s *Service) Register(ctx context.Context, email, password string, ci ClientInfo) (Tokens, error) {
	email = domain.NormalizeEmail(email)

	hash, err := s.hasher.Hash(password)
	if err != nil {
		return Tokens{}, fmt.Errorf("hash password: %w", err)
	}

	user := domain.User{
		ID:           uuid.New(),
		Email:        email,
		PasswordHash: hash,
	}

	// Занятость адреса не проверяется отдельным запросом: между проверкой
	// и вставкой успевает вклиниться другая регистрация. Единственный
	// надёжный арбитр — уникальный индекс, его ошибку и разбираем.
	if err := s.users.Create(ctx, user); err != nil {
		return Tokens{}, err
	}

	return s.openSession(ctx, user.ID, ci)
}

// Login проверяет пару логин/пароль и открывает сессию.
func (s *Service) Login(ctx context.Context, email, password string, ci ClientInfo) (Tokens, error) {
	user, err := s.users.ByEmail(ctx, domain.NormalizeEmail(email))
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			// Хэш всё равно считается: иначе несуществующий адрес отвечает
			// заметно быстрее существующего, и по времени ответа
			// перебираются зарегистрированные почты.
			_, _ = s.hasher.Verify(password, dummyHash)
			return Tokens{}, domain.ErrInvalidCredentials
		}
		return Tokens{}, err
	}

	ok, err := s.hasher.Verify(password, user.PasswordHash)
	if err != nil {
		return Tokens{}, fmt.Errorf("verify password: %w", err)
	}
	if !ok {
		return Tokens{}, domain.ErrInvalidCredentials
	}

	return s.openSession(ctx, user.ID, ci)
}

// Refresh обменивает refresh-токен на новую пару.
//
// Здесь же живёт обнаружение кражи: предъявление уже израсходованного токена
// означает, что у него была копия. Легитимный клиент так поступить не может —
// он уже получил замену, — поэтому гасится вся цепочка, а не только
// предъявленный экземпляр. Вор и жертва оказываются разлогинены оба,
// и жертва об этом узнаёт.
func (s *Service) Refresh(ctx context.Context, refreshToken string, ci ClientInfo) (Tokens, error) {
	now := s.now()

	session, err := s.sessions.ByTokenHash(ctx, HashRefreshToken(refreshToken))
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return Tokens{}, domain.ErrSessionInvalid
		}
		return Tokens{}, err
	}

	if session.UsedAt != nil {
		s.log.WarnContext(ctx, "refresh token reuse detected, revoking family",
			"user_id", session.UserID, "family_id", session.FamilyID)

		if err := s.sessions.RevokeFamily(ctx, session.FamilyID, now); err != nil {
			return Tokens{}, fmt.Errorf("revoke family: %w", err)
		}
		return Tokens{}, domain.ErrSessionInvalid
	}

	if !session.Active(now) {
		return Tokens{}, domain.ErrSessionInvalid
	}

	next := domain.Session{
		ID:        uuid.New(),
		FamilyID:  session.FamilyID,
		UserID:    session.UserID,
		IssuedAt:  now,
		ExpiresAt: now.Add(s.tokens.RefreshTTL()),
		UserAgent: ci.UserAgent,
		IP:        ci.IP,
	}

	refresh, hash, err := NewRefreshToken()
	if err != nil {
		return Tokens{}, err
	}

	if err := s.sessions.RotateWithinFamily(ctx, session.ID, next, hash, now); err != nil {
		return Tokens{}, err
	}

	// sid остаётся прежним: он идентифицирует вход, а не экземпляр токена.
	access, err := s.tokens.IssueAccess(session.UserID, session.FamilyID)
	if err != nil {
		return Tokens{}, err
	}

	return Tokens{
		Access:     access,
		AccessTTL:  s.tokens.AccessTTL(),
		Refresh:    refresh,
		RefreshTTL: s.tokens.RefreshTTL(),
	}, nil
}

// Logout гасит одну цепочку сессий.
//
// Идемпотентна: «выйти» и «уже вышел» для клиента — одно состояние,
// и отвечать на второе ошибкой значит заставлять фронт обрабатывать
// несуществующую проблему.
func (s *Service) Logout(ctx context.Context, familyID uuid.UUID) error {
	return s.sessions.RevokeFamily(ctx, familyID, s.now())
}

// LogoutByRefreshToken гасит цепочку, к которой принадлежит токен.
// Нужна, когда access уже истёк, а выйти надо.
func (s *Service) LogoutByRefreshToken(ctx context.Context, refreshToken string) error {
	session, err := s.sessions.ByTokenHash(ctx, HashRefreshToken(refreshToken))
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil
		}
		return err
	}
	return s.sessions.RevokeFamily(ctx, session.FamilyID, s.now())
}

// LogoutAll гасит все сессии пользователя.
func (s *Service) LogoutAll(ctx context.Context, userID uuid.UUID) error {
	return s.sessions.RevokeAllForUser(ctx, userID, s.now())
}

// Me отдаёт пользователя и его членства.
type Me struct {
	User        domain.User
	Memberships []domain.Membership
}

// Me отдаёт пользователя вместе со списком его компаний.
func (s *Service) Me(ctx context.Context, userID uuid.UUID) (Me, error) {
	user, err := s.users.ByID(ctx, userID)
	if err != nil {
		return Me{}, err
	}

	memberships, err := s.users.Memberships(ctx, userID)
	if err != nil {
		return Me{}, err
	}

	return Me{User: user, Memberships: memberships}, nil
}

// OpenSession открывает сессию для уже существующего пользователя.
//
// Нужна сценариям, которые заводят учётку сами — например, приёму
// приглашения с регистрацией: человек только что подтвердил владение
// ящиком переходом по ссылке, заставлять его тут же вводить пароль
// повторно незачем.
func (s *Service) OpenSession(ctx context.Context, userID uuid.UUID, ci ClientInfo) (Tokens, error) {
	return s.openSession(ctx, userID, ci)
}

func (s *Service) openSession(ctx context.Context, userID uuid.UUID, ci ClientInfo) (Tokens, error) {
	now := s.now()

	refresh, hash, err := NewRefreshToken()
	if err != nil {
		return Tokens{}, err
	}

	// Первая сессия цепочки: id и family_id совпадают, дальше ротации
	// заводят новые строки с тем же family_id.
	familyID := uuid.New()

	session := domain.Session{
		ID:        familyID,
		FamilyID:  familyID,
		UserID:    userID,
		IssuedAt:  now,
		ExpiresAt: now.Add(s.tokens.RefreshTTL()),
		UserAgent: ci.UserAgent,
		IP:        ci.IP,
	}

	if err := s.sessions.Create(ctx, session, hash); err != nil {
		return Tokens{}, err
	}

	access, err := s.tokens.IssueAccess(userID, familyID)
	if err != nil {
		return Tokens{}, err
	}

	return Tokens{
		Access:     access,
		AccessTTL:  s.tokens.AccessTTL(),
		Refresh:    refresh,
		RefreshTTL: s.tokens.RefreshTTL(),
	}, nil
}

// dummyHash — заведомо невалидный, но корректно оформленный хэш. Нужен
// исключительно чтобы вход по несуществующему адресу занимал столько же
// времени, сколько вход по существующему.
const dummyHash = "$argon2id$v=19$m=65536,t=3,p=4$" +
	"AAAAAAAAAAAAAAAAAAAAAA$" +
	"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
