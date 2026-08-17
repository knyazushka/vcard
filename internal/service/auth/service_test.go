package auth

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/knyazushka/vcard/internal/domain"
)

// Сценарии проверяются на поддельных хранилищах: правила ротации и реакция
// на кражу — это логика сервиса, база в ней не участвует.

type fakeUsers struct {
	byEmail map[string]domain.User
	byID    map[uuid.UUID]domain.User
}

func newFakeUsers() *fakeUsers {
	return &fakeUsers{
		byEmail: map[string]domain.User{},
		byID:    map[uuid.UUID]domain.User{},
	}
}

func (f *fakeUsers) Create(_ context.Context, u domain.User) error {
	if _, ok := f.byEmail[u.Email]; ok {
		return domain.ErrEmailTaken
	}
	f.byEmail[u.Email] = u
	f.byID[u.ID] = u
	return nil
}

func (f *fakeUsers) ByEmail(_ context.Context, email string) (domain.User, error) {
	u, ok := f.byEmail[email]
	if !ok {
		return domain.User{}, domain.ErrNotFound
	}
	return u, nil
}

func (f *fakeUsers) ByID(_ context.Context, id uuid.UUID) (domain.User, error) {
	u, ok := f.byID[id]
	if !ok {
		return domain.User{}, domain.ErrNotFound
	}
	return u, nil
}

func (f *fakeUsers) Memberships(context.Context, uuid.UUID) ([]domain.Membership, error) {
	return nil, nil
}

type fakeSessions struct {
	byHash   map[string]*domain.Session
	revoked  []uuid.UUID
	allRevkd []uuid.UUID
}

func newFakeSessions() *fakeSessions {
	return &fakeSessions{byHash: map[string]*domain.Session{}}
}

func (f *fakeSessions) Create(_ context.Context, s domain.Session, hash []byte) error {
	cp := s
	f.byHash[string(hash)] = &cp
	return nil
}

func (f *fakeSessions) ByTokenHash(_ context.Context, hash []byte) (domain.Session, error) {
	s, ok := f.byHash[string(hash)]
	if !ok {
		return domain.Session{}, domain.ErrNotFound
	}
	return *s, nil
}

func (f *fakeSessions) MarkUsed(_ context.Context, id uuid.UUID, at time.Time) error {
	for _, s := range f.byHash {
		if s.ID == id {
			s.UsedAt = &at
		}
	}
	return nil
}

func (f *fakeSessions) RevokeFamily(_ context.Context, familyID uuid.UUID, at time.Time) error {
	f.revoked = append(f.revoked, familyID)
	for _, s := range f.byHash {
		if s.FamilyID == familyID {
			s.RevokedAt = &at
		}
	}
	return nil
}

func (f *fakeSessions) RevokeAllForUser(_ context.Context, userID uuid.UUID, at time.Time) error {
	f.allRevkd = append(f.allRevkd, userID)
	for _, s := range f.byHash {
		if s.UserID == userID {
			s.RevokedAt = &at
		}
	}
	return nil
}

func (f *fakeSessions) RotateWithinFamily(
	ctx context.Context, oldID uuid.UUID, next domain.Session, nextHash []byte, at time.Time,
) error {
	if err := f.MarkUsed(ctx, oldID, at); err != nil {
		return err
	}
	return f.Create(ctx, next, nextHash)
}

func newTestService() (*Service, *fakeUsers, *fakeSessions) {
	users, sessions := newFakeUsers(), newFakeSessions()
	svc := NewService(
		users, sessions,
		NewHasher(8*1024, 1, 1),
		NewTokenIssuer(testSecret, 15*time.Minute, 24*time.Hour),
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	return svc, users, sessions
}

func TestRegisterThenLogin(t *testing.T) {
	svc, _, _ := newTestService()
	ctx := context.Background()

	if _, err := svc.Register(ctx, "Laura@Acme.com", "correct-horse-battery", ClientInfo{}); err != nil {
		t.Fatalf("register: %v", err)
	}

	// Адрес нормализуется, поэтому вход в любом регистре находит ту же учётку.
	if _, err := svc.Login(ctx, "LAURA@ACME.COM", "correct-horse-battery", ClientInfo{}); err != nil {
		t.Fatalf("login: %v", err)
	}
}

func TestLoginErrorsAreIndistinguishable(t *testing.T) {
	svc, _, _ := newTestService()
	ctx := context.Background()

	if _, err := svc.Register(ctx, "laura@acme.com", "correct-horse-battery", ClientInfo{}); err != nil {
		t.Fatalf("register: %v", err)
	}

	_, wrongPass := svc.Login(ctx, "laura@acme.com", "wrong-password", ClientInfo{})
	_, noSuchUser := svc.Login(ctx, "nobody@acme.com", "wrong-password", ClientInfo{})

	if !errors.Is(wrongPass, domain.ErrInvalidCredentials) {
		t.Errorf("неверный пароль дал %v", wrongPass)
	}
	if !errors.Is(noSuchUser, domain.ErrInvalidCredentials) {
		t.Errorf("несуществующий адрес дал %v", noSuchUser)
	}
}

func TestRefreshRotatesWithinFamily(t *testing.T) {
	svc, _, _ := newTestService()
	ctx := context.Background()

	issued, err := svc.Register(ctx, "laura@acme.com", "correct-horse-battery", ClientInfo{})
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	rotated, err := svc.Refresh(ctx, issued.Refresh, ClientInfo{})
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}

	if rotated.Refresh == issued.Refresh {
		t.Fatal("refresh-токен не сменился")
	}

	// sid идентифицирует вход, а не экземпляр токена, поэтому переживает
	// ротацию.
	issuer := NewTokenIssuer(testSecret, 15*time.Minute, 24*time.Hour)
	before, err := issuer.ParseAccess(issued.Access)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	after, err := issuer.ParseAccess(rotated.Access)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if before.SessionID != after.SessionID {
		t.Error("sid изменился при ротации")
	}
}

// Главный сценарий защиты: у токена оказалась копия. Легитимный клиент
// израсходованный токен предъявить не может — он уже получил замену.
func TestRefreshReuseRevokesWholeFamily(t *testing.T) {
	svc, _, sessions := newTestService()
	ctx := context.Background()

	issued, err := svc.Register(ctx, "laura@acme.com", "correct-horse-battery", ClientInfo{})
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	rotated, err := svc.Refresh(ctx, issued.Refresh, ClientInfo{})
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}

	// Вор предъявляет перехваченный старый токен.
	if _, err := svc.Refresh(ctx, issued.Refresh, ClientInfo{}); !errors.Is(err, domain.ErrSessionInvalid) {
		t.Fatalf("повторное использование дало %v, ждали ErrSessionInvalid", err)
	}
	if len(sessions.revoked) == 0 {
		t.Fatal("цепочка не была погашена")
	}

	// И жертва тоже теряет доступ — это цена обнаружения, зато вор не остаётся
	// в системе незамеченным.
	if _, err := svc.Refresh(ctx, rotated.Refresh, ClientInfo{}); !errors.Is(err, domain.ErrSessionInvalid) {
		t.Fatalf("действующий токен пережил обнаружение кражи: %v", err)
	}
}

func TestRefreshRejectsRevokedAndExpired(t *testing.T) {
	svc, _, _ := newTestService()
	ctx := context.Background()

	issued, err := svc.Register(ctx, "laura@acme.com", "correct-horse-battery", ClientInfo{})
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	// Выход гасит цепочку.
	claims, err := NewTokenIssuer(testSecret, 15*time.Minute, 24*time.Hour).ParseAccess(issued.Access)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := svc.Logout(ctx, claims.SessionID); err != nil {
		t.Fatalf("logout: %v", err)
	}

	if _, err := svc.Refresh(ctx, issued.Refresh, ClientInfo{}); !errors.Is(err, domain.ErrSessionInvalid) {
		t.Fatalf("отозванная сессия обменялась: %v", err)
	}

	if _, err := svc.Refresh(ctx, "never-existed", ClientInfo{}); !errors.Is(err, domain.ErrSessionInvalid) {
		t.Fatalf("неизвестный токен дал %v", err)
	}
}

func TestLogoutAllRevokesEverySession(t *testing.T) {
	svc, users, _ := newTestService()
	ctx := context.Background()

	first, err := svc.Register(ctx, "laura@acme.com", "correct-horse-battery", ClientInfo{})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	second, err := svc.Login(ctx, "laura@acme.com", "correct-horse-battery", ClientInfo{})
	if err != nil {
		t.Fatalf("login: %v", err)
	}

	user := users.byEmail["laura@acme.com"]
	if err := svc.LogoutAll(ctx, user.ID); err != nil {
		t.Fatalf("logout all: %v", err)
	}

	for name, token := range map[string]string{"первая": first.Refresh, "вторая": second.Refresh} {
		if _, err := svc.Refresh(ctx, token, ClientInfo{}); !errors.Is(err, domain.ErrSessionInvalid) {
			t.Errorf("%s сессия пережила logout-all: %v", name, err)
		}
	}
}

func TestSessionActive(t *testing.T) {
	now := time.Now()
	used := now.Add(-time.Minute)

	cases := map[string]struct {
		session domain.Session
		want    bool
	}{
		"свежая":     {domain.Session{ExpiresAt: now.Add(time.Hour)}, true},
		"истёкшая":   {domain.Session{ExpiresAt: now.Add(-time.Hour)}, false},
		"обменянная": {domain.Session{ExpiresAt: now.Add(time.Hour), UsedAt: &used}, false},
		"отозванная": {domain.Session{ExpiresAt: now.Add(time.Hour), RevokedAt: &used}, false},
	}

	for name, tc := range cases {
		if got := tc.session.Active(now); got != tc.want {
			t.Errorf("%s: получили %v, ждали %v", name, got, tc.want)
		}
	}
}
