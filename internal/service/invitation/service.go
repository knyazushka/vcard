// Package invitation содержит сценарии приглашения сотрудников.
package invitation

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/knyazushka/vcard/internal/domain"
	"github.com/knyazushka/vcard/internal/email"
	"github.com/knyazushka/vcard/internal/repository/postgres"
)

// Repo — то, что сценариям нужно от хранилища приглашений.
type Repo interface {
	Create(ctx context.Context, inv domain.Invitation, tokenHash []byte, msg postgres.OutboxMessage) (domain.Invitation, error)
	ByID(ctx context.Context, companyID, id uuid.UUID) (domain.Invitation, error)
	ByTokenHash(ctx context.Context, hash []byte) (domain.Invitation, error)
	List(ctx context.Context, companyID uuid.UUID, status string, page domain.Page) ([]domain.Invitation, int64, error)
	Revoke(ctx context.Context, companyID, id uuid.UUID) error
	Resend(ctx context.Context, companyID, id uuid.UUID, tokenHash []byte, expiresAt time.Time, msg postgres.OutboxMessage) (domain.Invitation, error)
	Accept(ctx context.Context, inv domain.Invitation, userID uuid.UUID, newUser *domain.User, slugCandidates []string, now time.Time) (postgres.AcceptResult, error)
}

// Users — то, что нужно от хранилища пользователей.
type Users interface {
	ByEmail(ctx context.Context, email string) (domain.User, error)
	ByID(ctx context.Context, id uuid.UUID) (domain.User, error)
}

// Companies — проверка прав вызывающего.
type Companies interface {
	Authorize(ctx context.Context, companyID, userID uuid.UUID, need domain.Role) (domain.Role, error)
	Membership(ctx context.Context, companyID, userID uuid.UUID) (domain.Role, error)
	ByID(ctx context.Context, companyID uuid.UUID) (domain.Company, error)
}

// Hasher считает хэш пароля при регистрации по приглашению.
type Hasher interface {
	Hash(password string) (string, error)
}

// Service — сценарии приглашений.
type Service struct {
	repo      Repo
	users     Users
	companies Companies
	hasher    Hasher

	appURL string
	ttl    time.Duration
	now    func() time.Time
}

// NewService создаёт сценарии приглашений.
func NewService(repo Repo, users Users, companies Companies, hasher Hasher, appURL string, ttl time.Duration) *Service {
	return &Service{
		repo:      repo,
		users:     users,
		companies: companies,
		hasher:    hasher,
		appURL:    strings.TrimRight(appURL, "/"),
		ttl:       ttl,
		now:       time.Now,
	}
}

// Create выписывает приглашение и ставит письмо в очередь.
func (s *Service) Create(
	ctx context.Context, companyID, actorID uuid.UUID, invitedEmail string, role domain.Role,
) (domain.Invitation, error) {
	if _, err := s.companies.Authorize(ctx, companyID, actorID, domain.RoleAdmin); err != nil {
		return domain.Invitation{}, err
	}

	invitedEmail = domain.NormalizeEmail(invitedEmail)

	// Уже состоящего в компании звать незачем: он бы получил письмо,
	// перешёл по ссылке и увидел, что ничего не изменилось.
	if user, err := s.users.ByEmail(ctx, invitedEmail); err == nil {
		if _, err := s.companies.Membership(ctx, companyID, user.ID); err == nil {
			return domain.Invitation{}, domain.ErrAlreadyMember
		} else if !errors.Is(err, domain.ErrNotFound) {
			return domain.Invitation{}, err
		}
	} else if !errors.Is(err, domain.ErrNotFound) {
		return domain.Invitation{}, err
	}

	actor, err := s.users.ByID(ctx, actorID)
	if err != nil {
		return domain.Invitation{}, err
	}

	token, hash, err := newToken()
	if err != nil {
		return domain.Invitation{}, err
	}

	inv := domain.Invitation{
		CompanyID: companyID,
		Email:     invitedEmail,
		Role:      role,
		InvitedBy: actorID,
		ExpiresAt: s.now().Add(s.ttl),
	}

	company, err := s.companies.ByID(ctx, companyID)
	if err != nil {
		return domain.Invitation{}, err
	}

	msg := s.message(invitedEmail, company.Name, actor.Email, role, token, inv.ExpiresAt)

	return s.repo.Create(ctx, inv, hash, msg)
}

// List отдаёт приглашения компании.
func (s *Service) List(
	ctx context.Context, companyID, actorID uuid.UUID, status string, page domain.Page,
) ([]domain.Invitation, int64, error) {
	if _, err := s.companies.Authorize(ctx, companyID, actorID, domain.RoleAdmin); err != nil {
		return nil, 0, err
	}
	// EXPIRED в базе не хранится, фильтровать по нему нечего — отсекаем,
	// чтобы запрос не вернул пустоту без объяснений.
	if status == string(domain.InvitationExpired) {
		status = string(domain.InvitationPending)
	}
	return s.repo.List(ctx, companyID, status, page)
}

// Revoke отзывает приглашение.
func (s *Service) Revoke(ctx context.Context, companyID, actorID, id uuid.UUID) error {
	if _, err := s.companies.Authorize(ctx, companyID, actorID, domain.RoleAdmin); err != nil {
		return err
	}
	return s.repo.Revoke(ctx, companyID, id)
}

// Resend выпускает новый токен и ставит письмо в очередь заново.
func (s *Service) Resend(ctx context.Context, companyID, actorID, id uuid.UUID) (domain.Invitation, error) {
	if _, err := s.companies.Authorize(ctx, companyID, actorID, domain.RoleAdmin); err != nil {
		return domain.Invitation{}, err
	}

	inv, err := s.repo.ByID(ctx, companyID, id)
	if err != nil {
		return domain.Invitation{}, err
	}
	if inv.Status != domain.InvitationPending {
		return domain.Invitation{}, domain.ErrConflict
	}

	actor, err := s.users.ByID(ctx, actorID)
	if err != nil {
		return domain.Invitation{}, err
	}

	token, hash, err := newToken()
	if err != nil {
		return domain.Invitation{}, err
	}
	expiresAt := s.now().Add(s.ttl)

	msg := s.message(inv.Email, inv.CompanyName, actor.Email, inv.Role, token, expiresAt)

	return s.repo.Resend(ctx, companyID, id, hash, expiresAt, msg)
}

// Preview отдаёт сведения о приглашении по токену из письма.
type Preview struct {
	Invitation           domain.Invitation
	RequiresRegistration bool
}

// Preview показывает, куда и кого зовут, до того как человек примет решение.
func (s *Service) Preview(ctx context.Context, token string) (Preview, error) {
	inv, err := s.usable(ctx, token)
	if err != nil {
		return Preview{}, err
	}

	_, err = s.users.ByEmail(ctx, inv.Email)
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return Preview{Invitation: inv, RequiresRegistration: true}, nil
	case err != nil:
		return Preview{}, err
	default:
		return Preview{Invitation: inv, RequiresRegistration: false}, nil
	}
}

// AcceptInput — то, с чем приходит принимающий.
type AcceptInput struct {
	Token string
	// Password заполняется только при регистрации по приглашению.
	Password string
	// ActorID — вошедший пользователь, если он есть.
	ActorID uuid.UUID
}

// Accept принимает приглашение.
//
// Ветка зависит от того, есть ли учётка с адресом приглашения, и кто именно
// пришёл по ссылке. Токена самого по себе недостаточно: письма пересылают,
// поэтому адрес принимающего обязан совпадать с адресом приглашения.
func (s *Service) Accept(ctx context.Context, in AcceptInput) (postgres.AcceptResult, error) {
	inv, err := s.usable(ctx, in.Token)
	if err != nil {
		return postgres.AcceptResult{}, err
	}

	existing, err := s.users.ByEmail(ctx, inv.Email)
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return s.acceptAsNewUser(ctx, inv, in)
	case err != nil:
		return postgres.AcceptResult{}, err
	}

	// Учётка есть — регистрация не годится, нужен вход именно под ней.
	if in.ActorID == uuid.Nil {
		return postgres.AcceptResult{}, domain.ErrAuthRequired
	}
	if in.ActorID != existing.ID {
		return postgres.AcceptResult{}, domain.ErrEmailMismatch
	}

	return s.repo.Accept(ctx, inv, existing.ID, nil,
		domain.SlugCandidates(domain.SlugFromEmail(inv.Email), slugAttempts), s.now())
}

func (s *Service) acceptAsNewUser(ctx context.Context, inv domain.Invitation, in AcceptInput) (postgres.AcceptResult, error) {
	// Вход под чужой учёткой при регистрации нового адреса — тот же
	// email_mismatch: иначе Иван, кликнув по письму Марии, заведёт ей
	// учётку и заберёт членство себе.
	if in.ActorID != uuid.Nil {
		return postgres.AcceptResult{}, domain.ErrEmailMismatch
	}
	if in.Password == "" {
		return postgres.AcceptResult{}, domain.ErrPasswordRequired
	}

	hash, err := s.hasher.Hash(in.Password)
	if err != nil {
		return postgres.AcceptResult{}, fmt.Errorf("hash password: %w", err)
	}

	newUser := &domain.User{
		ID:            uuid.New(),
		Email:         inv.Email,
		EmailVerified: true,
		PasswordHash:  hash,
	}

	return s.repo.Accept(ctx, inv, uuid.Nil, newUser,
		domain.SlugCandidates(domain.SlugFromEmail(inv.Email), slugAttempts), s.now())
}

// usable находит действующее приглашение по токену.
//
// Неизвестный, отозванный, принятый и протухший токены дают одну и ту же
// ошибку: различать их — значит рассказывать предъявителю чужой ссылки,
// существовала ли она вообще.
func (s *Service) usable(ctx context.Context, token string) (domain.Invitation, error) {
	if token == "" {
		return domain.Invitation{}, domain.ErrInvitationNotFound
	}

	inv, err := s.repo.ByTokenHash(ctx, hashToken(token))
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return domain.Invitation{}, domain.ErrInvitationNotFound
		}
		return domain.Invitation{}, err
	}
	if !inv.Usable(s.now()) {
		return domain.Invitation{}, domain.ErrInvitationNotFound
	}
	return inv, nil
}

// message собирает задание на письмо.
//
// Ссылка строится из адреса фронта, взятого из конфигурации, и никогда
// из заголовка Host входящего запроса: подделанный Host уехал бы в письмо
// жертве вместе с нашим текстом и чужой ссылкой.
func (s *Service) message(
	to, companyName, inviterEmail string, role domain.Role, token string, expiresAt time.Time,
) postgres.OutboxMessage {
	acceptURL := fmt.Sprintf("%s/invite?token=%s", s.appURL, url.QueryEscape(token))

	return postgres.OutboxMessage{
		Kind: email.KindInvitation,
		To:   to,
		Payload: map[string]any{
			"companyName":  companyName,
			"inviterEmail": inviterEmail,
			"acceptUrl":    acceptURL,
			"expiresAt":    expiresAt.Format("02.01.2006 15:04 MST"),
			"role":         string(role),
		},
	}
}

// slugAttempts ограничивает перебор адресов визитки: после десятка попыток
// разумнее сдаться и позволить человеку выбрать адрес самому.
const slugAttempts = 10

// newToken возвращает токен для письма и его хэш для базы.
func newToken() (token string, hash []byte, err error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, fmt.Errorf("generate invitation token: %w", err)
	}
	token = base64.RawURLEncoding.EncodeToString(raw)
	return token, hashToken(token), nil
}

func hashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}
