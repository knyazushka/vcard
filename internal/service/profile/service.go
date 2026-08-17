// Package profile содержит сценарии работы с визиткой сотрудника.
package profile

import (
	"context"
	"errors"
	"regexp"
	"strings"

	"github.com/google/uuid"

	"github.com/knyazushka/vcard/internal/domain"
)

// qlClassPattern пропускает только классы редактора Quill.
var qlClassPattern = regexp.MustCompile(`^ql-[a-z0-9-]+$`)

// Repo — то, что сценариям нужно от хранилища визиток.
type Repo interface {
	ByID(ctx context.Context, id uuid.UUID) (domain.Profile, error)
	BySlug(ctx context.Context, slug string) (domain.Profile, error)
	Update(ctx context.Context, id uuid.UUID, u domain.ProfileUpdate) (domain.Profile, error)
	SetAvatar(ctx context.Context, id uuid.UUID, key string, crop *domain.Crop) (domain.Profile, error)
	UpdateSlug(ctx context.Context, id uuid.UUID, slug string) (domain.Profile, error)
	SlugAvailable(ctx context.Context, slug string) (bool, string, error)
	SetStatus(ctx context.Context, id uuid.UUID, status domain.ProfileStatus) (domain.Profile, error)
}

// Companies — проверка роли вызывающего в компании профиля.
type Companies interface {
	Membership(ctx context.Context, companyID, userID uuid.UUID) (domain.Role, error)
}

// Service — сценарии работы с визиткой.
type Service struct {
	repo      Repo
	companies Companies
	sanitizer *Sanitizer
}

// NewService создаёт сценарии работы с визиткой.
func NewService(repo Repo, companies Companies) *Service {
	return &Service{repo: repo, companies: companies, sanitizer: NewSanitizer()}
}

// access описывает, кем приходится вызывающий этой визитке.
type access struct {
	profile domain.Profile
	owner   bool
	admin   bool
}

// authorize проверяет доступ к визитке.
//
// Редактировать может владелец либо администратор компании — второе прямо
// требует ТЗ («администратор может редактировать странички пользователей»).
// Посторонний получает ErrNotFound, а не ErrForbidden: иначе по кодам
// ответов перебираются идентификаторы чужих визиток.
func (s *Service) authorize(ctx context.Context, profileID, userID uuid.UUID) (access, error) {
	p, err := s.repo.ByID(ctx, profileID)
	if err != nil {
		return access{}, err
	}

	if p.UserID == userID {
		return access{profile: p, owner: true}, nil
	}

	role, err := s.companies.Membership(ctx, p.CompanyID, userID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return access{}, domain.ErrNotFound
		}
		return access{}, err
	}
	if role != domain.RoleAdmin {
		return access{}, domain.ErrNotFound
	}

	return access{profile: p, admin: true}, nil
}

// Get отдаёт визитку владельцу или администратору компании.
func (s *Service) Get(ctx context.Context, profileID, userID uuid.UUID) (domain.Profile, error) {
	acc, err := s.authorize(ctx, profileID, userID)
	if err != nil {
		return domain.Profile{}, err
	}
	return acc.profile, nil
}

// Public отдаёт визитку по публичному адресу.
//
// Черновик и архив дают ErrNotFound: снаружи их вообще не должно быть видно.
// Заблокированная возвращается — обработчик покажет заглушку, потому что
// ссылка уже разослана и обязана открываться чем-то осмысленным.
func (s *Service) Public(ctx context.Context, slug string) (domain.Profile, error) {
	p, err := s.repo.BySlug(ctx, slug)
	if err != nil {
		return domain.Profile{}, err
	}

	switch p.Status {
	case domain.ProfilePublished, domain.ProfileBlocked:
		return p, nil
	default:
		return domain.Profile{}, domain.ErrNotFound
	}
}

// UpdateInput — то, что приходит из запроса на изменение.
type UpdateInput struct {
	FullName            *string
	PositionID          *uuid.UUID
	PositionCustom      *string
	PositionTouched     bool
	AboutHTML           *string
	AboutTouched        bool
	TagIDs              []uuid.UUID
	CustomTags          []string
	TagsTouched         bool
	Contacts            *domain.Contacts
	ShowCompanyContacts *bool
}

// Update меняет визитку.
func (s *Service) Update(ctx context.Context, profileID, userID uuid.UUID, in UpdateInput) (domain.Profile, error) {
	if _, err := s.authorize(ctx, profileID, userID); err != nil {
		return domain.Profile{}, err
	}

	u := domain.ProfileUpdate{
		ShowCompanyContacts: in.ShowCompanyContacts,
	}

	if in.FullName != nil {
		name := strings.Join(strings.Fields(*in.FullName), " ")
		u.FullName = &name
	}

	if in.PositionTouched {
		pos, err := positionRef(in.PositionID, in.PositionCustom)
		if err != nil {
			return domain.Profile{}, err
		}
		u.Position = &pos
	}

	if in.AboutTouched {
		var safe, plain string
		if in.AboutHTML != nil {
			safe, plain = s.sanitizer.Sanitize(*in.AboutHTML)
		}
		u.AboutHTML = &safe
		u.AboutText = plain
	}

	if in.TagsTouched {
		tags, err := tagRefs(in.TagIDs, in.CustomTags)
		if err != nil {
			return domain.Profile{}, err
		}
		u.Tags = &tags
	}

	if in.Contacts != nil {
		c := domain.Contacts{
			Phone:    domain.NormalizePhone(in.Contacts.Phone),
			WhatsApp: domain.NormalizePhone(in.Contacts.WhatsApp),
			Telegram: domain.NormalizeTelegram(in.Contacts.Telegram),
		}
		u.Contacts = &c
	}

	return s.repo.Update(ctx, profileID, u)
}

// positionRef собирает должность из двух взаимоисключающих полей.
func positionRef(id *uuid.UUID, custom *string) (domain.PositionRef, error) {
	var ref domain.PositionRef

	if id != nil && *id != uuid.Nil {
		ref.ID = *id
	}
	if custom != nil {
		ref.Custom = strings.Join(strings.Fields(*custom), " ")
	}

	// Схема этого выразить не может, а CHECK в базе отвергнет запись
	// невнятной ошибкой — понятнее отказать здесь.
	if ref.ID != uuid.Nil && ref.Custom != "" {
		return domain.PositionRef{}, domain.ErrPositionAmbiguous
	}
	return ref, nil
}

// tagRefs собирает список тегов из справочных и пользовательских.
//
// Пользовательские нормализуются и дедуплицируются: без этого «Golang»
// и «golang  » занимают два из трёх мест на визитке.
func tagRefs(ids []uuid.UUID, custom []string) ([]domain.TagRef, error) {
	out := make([]domain.TagRef, 0, len(ids)+len(custom))
	seen := make(map[string]bool, len(custom))

	for _, id := range ids {
		if id == uuid.Nil {
			continue
		}
		out = append(out, domain.TagRef{ID: id})
	}

	for _, title := range custom {
		title = strings.Join(strings.Fields(title), " ")
		if title == "" {
			continue
		}
		key := strings.ToLower(title)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, domain.TagRef{Custom: title})
	}

	if len(out) > domain.MaxProfileTags {
		return nil, domain.ErrTooManyTags
	}
	return out, nil
}

// SetAvatar сохраняет ключ загруженной фотографии и область кадрирования.
func (s *Service) SetAvatar(
	ctx context.Context, profileID, userID uuid.UUID, key string, crop *domain.Crop,
) (domain.Profile, error) {
	if _, err := s.authorize(ctx, profileID, userID); err != nil {
		return domain.Profile{}, err
	}
	return s.repo.SetAvatar(ctx, profileID, key, crop)
}

// SetCrop меняет только область кадрирования — файл не перезаливается.
func (s *Service) SetCrop(ctx context.Context, profileID, userID uuid.UUID, crop domain.Crop) (domain.Profile, error) {
	acc, err := s.authorize(ctx, profileID, userID)
	if err != nil {
		return domain.Profile{}, err
	}
	if acc.profile.AvatarKey == "" {
		return domain.Profile{}, domain.ErrNotFound
	}
	return s.repo.SetAvatar(ctx, profileID, acc.profile.AvatarKey, &crop)
}

// DeleteAvatar убирает фотографию вместе с кропом.
func (s *Service) DeleteAvatar(ctx context.Context, profileID, userID uuid.UUID) error {
	if _, err := s.authorize(ctx, profileID, userID); err != nil {
		return err
	}
	_, err := s.repo.SetAvatar(ctx, profileID, "", nil)
	return err
}

// UpdateSlug меняет адрес визитки.
func (s *Service) UpdateSlug(ctx context.Context, profileID, userID uuid.UUID, slug string) (domain.Profile, error) {
	if _, err := s.authorize(ctx, profileID, userID); err != nil {
		return domain.Profile{}, err
	}

	slug = domain.NormalizeSlug(slug)
	if len(slug) < domain.SlugMinLen {
		return domain.Profile{}, domain.ErrInvalidInput
	}

	return s.repo.UpdateSlug(ctx, profileID, slug)
}

// SlugAvailability проверяет, свободен ли адрес.
func (s *Service) SlugAvailability(ctx context.Context, slug string) (bool, string, error) {
	return s.repo.SlugAvailable(ctx, domain.NormalizeSlug(slug))
}

// Publish публикует визитку. Доступно владельцу и администратору.
func (s *Service) Publish(ctx context.Context, profileID, userID uuid.UUID) (domain.Profile, error) {
	acc, err := s.authorize(ctx, profileID, userID)
	if err != nil {
		return domain.Profile{}, err
	}

	// Заблокированную страницу владелец опубликовать не может — иначе
	// блокировка не была бы блокировкой. Снять её вправе только админ.
	if acc.profile.Status == domain.ProfileBlocked && !acc.admin {
		return domain.Profile{}, domain.ErrForbidden
	}
	// Архивная принадлежит человеку, которого в компании больше нет.
	if acc.profile.Status == domain.ProfileArchived {
		return domain.Profile{}, domain.ErrForbidden
	}

	if err := acc.profile.ReadyToPublish(); err != nil {
		return domain.Profile{}, err
	}

	return s.repo.SetStatus(ctx, profileID, domain.ProfilePublished)
}

// Block блокирует визитку. Только администратор компании.
//
// Страница перестаёт открываться, но адрес остаётся занятым — это прямое
// требование ТЗ: снять его и отдать другому нельзя.
func (s *Service) Block(ctx context.Context, profileID, userID uuid.UUID) (domain.Profile, error) {
	acc, err := s.authorize(ctx, profileID, userID)
	if err != nil {
		return domain.Profile{}, err
	}
	if !acc.admin {
		return domain.Profile{}, domain.ErrForbidden
	}
	if acc.profile.Status == domain.ProfileArchived {
		return domain.Profile{}, domain.ErrForbidden
	}

	return s.repo.SetStatus(ctx, profileID, domain.ProfileBlocked)
}
