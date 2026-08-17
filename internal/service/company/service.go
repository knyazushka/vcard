// Package company содержит сценарии работы с компанией-тенантом.
package company

import (
	"context"
	"strings"

	"github.com/google/uuid"

	"github.com/knyazushka/vcard/internal/domain"
)

// Repo — то, что сценариям нужно от хранилища компаний.
type Repo interface {
	Create(ctx context.Context, c domain.Company, ownerID uuid.UUID, slugCandidates []string) (domain.Company, error)
	ByID(ctx context.Context, id uuid.UUID) (domain.Company, error)
	Update(ctx context.Context, id uuid.UUID, name, address *string, phones []domain.Phone, replacePhones bool) (domain.Company, error)
	SetLogoKey(ctx context.Context, id uuid.UUID, key string) error

	Membership(ctx context.Context, companyID, userID uuid.UUID) (domain.Role, error)
	Employees(ctx context.Context, companyID uuid.UUID, page domain.Page) ([]domain.Employee, int64, error)
	EmployeeByID(ctx context.Context, companyID, userID uuid.UUID) (domain.Employee, error)
	SetRole(ctx context.Context, companyID, userID uuid.UUID, role domain.Role) error
	RemoveEmployee(ctx context.Context, companyID, userID uuid.UUID) error

	Positions(ctx context.Context, companyID uuid.UUID) ([]domain.Position, error)
	CreatePosition(ctx context.Context, companyID uuid.UUID, title string) (domain.Position, error)
	UpdatePosition(ctx context.Context, companyID, id uuid.UUID, title string) (domain.Position, error)
	DeletePosition(ctx context.Context, companyID, id uuid.UUID) error

	Tags(ctx context.Context, companyID uuid.UUID) ([]domain.Tag, error)
	CreateTag(ctx context.Context, companyID uuid.UUID, title string) (domain.Tag, error)
	DeleteTag(ctx context.Context, companyID, id uuid.UUID) error
}

// Users — то, что нужно от хранилища пользователей.
type Users interface {
	ByID(ctx context.Context, id uuid.UUID) (domain.User, error)
}

// Service — сценарии работы с компанией.
type Service struct {
	repo  Repo
	users Users
}

// NewService создаёт сценарии работы с компанией.
func NewService(repo Repo, users Users) *Service { return &Service{repo: repo, users: users} }

// Authorize проверяет, что пользователь состоит в компании и его роль
// не ниже требуемой.
//
// Отсутствие компании и отсутствие членства дают одинаковый ErrNotFound:
// различать их снаружи — значит позволить перебирать чужие идентификаторы
// по кодам ответов.
func (s *Service) Authorize(ctx context.Context, companyID, userID uuid.UUID, need domain.Role) (domain.Role, error) {
	role, err := s.repo.Membership(ctx, companyID, userID)
	if err != nil {
		return "", err
	}
	if need == domain.RoleAdmin && role != domain.RoleAdmin {
		return role, domain.ErrForbidden
	}
	return role, nil
}

// Create заводит компанию; создатель становится её администратором.
func (s *Service) Create(ctx context.Context, ownerID uuid.UUID, c domain.Company) (domain.Company, error) {
	c.Name = strings.TrimSpace(c.Name)
	c.Address = strings.TrimSpace(c.Address)

	owner, err := s.users.ByID(ctx, ownerID)
	if err != nil {
		return domain.Company{}, err
	}

	candidates := domain.SlugCandidates(domain.SlugFromEmail(owner.Email), slugAttempts)
	return s.repo.Create(ctx, c, ownerID, candidates)
}

// Membership отдаёт роль пользователя в компании, ErrNotFound — если он в ней
// не состоит. Права не проверяет: это и есть сама проверка.
func (s *Service) Membership(ctx context.Context, companyID, userID uuid.UUID) (domain.Role, error) {
	return s.repo.Membership(ctx, companyID, userID)
}

// ByID отдаёт компанию БЕЗ проверки прав.
//
// Только для внутренних сценариев, где право уже проверено выше по стеку —
// например, чтобы подставить название компании в письмо с приглашением.
// В обработчики API этот метод попадать не должен.
func (s *Service) ByID(ctx context.Context, companyID uuid.UUID) (domain.Company, error) {
	return s.repo.ByID(ctx, companyID)
}

// Get отдаёт компанию любому её участнику.
func (s *Service) Get(ctx context.Context, companyID, userID uuid.UUID) (domain.Company, error) {
	if _, err := s.Authorize(ctx, companyID, userID, domain.RoleEmployee); err != nil {
		return domain.Company{}, err
	}
	return s.repo.ByID(ctx, companyID)
}

// Update меняет реквизиты компании.
func (s *Service) Update(
	ctx context.Context, companyID, userID uuid.UUID,
	name, address *string, phones []domain.Phone, replacePhones bool,
) (domain.Company, error) {
	if _, err := s.Authorize(ctx, companyID, userID, domain.RoleAdmin); err != nil {
		return domain.Company{}, err
	}
	return s.repo.Update(ctx, companyID, name, address, phones, replacePhones)
}

// SetLogo сохраняет ключ загруженного логотипа.
func (s *Service) SetLogo(ctx context.Context, companyID, userID uuid.UUID, key string) (domain.Company, error) {
	if _, err := s.Authorize(ctx, companyID, userID, domain.RoleAdmin); err != nil {
		return domain.Company{}, err
	}
	if err := s.repo.SetLogoKey(ctx, companyID, key); err != nil {
		return domain.Company{}, err
	}
	return s.repo.ByID(ctx, companyID)
}

// Employees отдаёт состав компании — только администратору: в списке
// адреса почты остальных сотрудников.
func (s *Service) Employees(
	ctx context.Context, companyID, userID uuid.UUID, page domain.Page,
) ([]domain.Employee, int64, error) {
	if _, err := s.Authorize(ctx, companyID, userID, domain.RoleAdmin); err != nil {
		return nil, 0, err
	}
	return s.repo.Employees(ctx, companyID, page)
}

// SetRole меняет роль участника.
//
// Попытка понизить последнего администратора отклоняется базой: два
// одновременных понижения по отдельности выглядят допустимыми, поэтому
// проверять это здесь бессмысленно.
func (s *Service) SetRole(
	ctx context.Context, companyID, actorID, targetID uuid.UUID, role domain.Role,
) (domain.Employee, error) {
	if _, err := s.Authorize(ctx, companyID, actorID, domain.RoleAdmin); err != nil {
		return domain.Employee{}, err
	}
	if err := s.repo.SetRole(ctx, companyID, targetID, role); err != nil {
		return domain.Employee{}, err
	}
	return s.repo.EmployeeByID(ctx, companyID, targetID)
}

// RemoveEmployee исключает участника; его профиль уходит в архив,
// а адрес визитки остаётся занятым.
func (s *Service) RemoveEmployee(ctx context.Context, companyID, actorID, targetID uuid.UUID) error {
	if _, err := s.Authorize(ctx, companyID, actorID, domain.RoleAdmin); err != nil {
		return err
	}
	return s.repo.RemoveEmployee(ctx, companyID, targetID)
}

// Positions отдаёт справочник должностей любому участнику: из него сотрудник
// выбирает свою должность.
func (s *Service) Positions(ctx context.Context, companyID, userID uuid.UUID) ([]domain.Position, error) {
	if _, err := s.Authorize(ctx, companyID, userID, domain.RoleEmployee); err != nil {
		return nil, err
	}
	return s.repo.Positions(ctx, companyID)
}

// CreatePosition добавляет должность в справочник.
func (s *Service) CreatePosition(ctx context.Context, companyID, userID uuid.UUID, title string) (domain.Position, error) {
	if _, err := s.Authorize(ctx, companyID, userID, domain.RoleAdmin); err != nil {
		return domain.Position{}, err
	}
	return s.repo.CreatePosition(ctx, companyID, normalizeTitle(title))
}

// UpdatePosition переименовывает должность.
func (s *Service) UpdatePosition(ctx context.Context, companyID, userID, id uuid.UUID, title string) (domain.Position, error) {
	if _, err := s.Authorize(ctx, companyID, userID, domain.RoleAdmin); err != nil {
		return domain.Position{}, err
	}
	return s.repo.UpdatePosition(ctx, companyID, id, normalizeTitle(title))
}

// DeletePosition убирает должность из справочника.
func (s *Service) DeletePosition(ctx context.Context, companyID, userID, id uuid.UUID) error {
	if _, err := s.Authorize(ctx, companyID, userID, domain.RoleAdmin); err != nil {
		return err
	}
	return s.repo.DeletePosition(ctx, companyID, id)
}

// Tags отдаёт справочник тегов любому участнику.
func (s *Service) Tags(ctx context.Context, companyID, userID uuid.UUID) ([]domain.Tag, error) {
	if _, err := s.Authorize(ctx, companyID, userID, domain.RoleEmployee); err != nil {
		return nil, err
	}
	return s.repo.Tags(ctx, companyID)
}

// CreateTag добавляет тег в справочник.
func (s *Service) CreateTag(ctx context.Context, companyID, userID uuid.UUID, title string) (domain.Tag, error) {
	if _, err := s.Authorize(ctx, companyID, userID, domain.RoleAdmin); err != nil {
		return domain.Tag{}, err
	}
	return s.repo.CreateTag(ctx, companyID, normalizeTitle(title))
}

// DeleteTag убирает тег из справочника; у профилей он становится своим.
func (s *Service) DeleteTag(ctx context.Context, companyID, userID, id uuid.UUID) error {
	if _, err := s.Authorize(ctx, companyID, userID, domain.RoleAdmin); err != nil {
		return err
	}
	return s.repo.DeleteTag(ctx, companyID, id)
}

// slugAttempts ограничивает перебор адресов визитки.
const slugAttempts = 10

// normalizeTitle убирает лишние пробелы. Регистр сохраняется — его видит
// пользователь; уникальность проверяется по нормализованному значению
// в самой базе, генерируемым столбцом.
func normalizeTitle(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
