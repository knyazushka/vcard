package profile

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/knyazushka/vcard/internal/domain"
)

// stubRepo отдаёт одну заранее заданную визитку и запоминает,
// в какой статус её попросили перевести.
type stubRepo struct {
	profile domain.Profile
	status  domain.ProfileStatus
}

func (r *stubRepo) ByID(context.Context, uuid.UUID) (domain.Profile, error) {
	return r.profile, nil
}

func (r *stubRepo) SetStatus(
	_ context.Context, _ uuid.UUID, status domain.ProfileStatus,
) (domain.Profile, error) {
	r.status = status
	p := r.profile
	p.Status = status
	return p, nil
}

func (r *stubRepo) BySlug(context.Context, string) (domain.Profile, error) {
	return domain.Profile{}, domain.ErrNotFound
}

func (r *stubRepo) Update(
	context.Context, uuid.UUID, domain.ProfileUpdate,
) (domain.Profile, error) {
	return r.profile, nil
}

func (r *stubRepo) SetAvatar(
	context.Context, uuid.UUID, string, *domain.Crop,
) (domain.Profile, error) {
	return r.profile, nil
}

func (r *stubRepo) UpdateSlug(context.Context, uuid.UUID, string) (domain.Profile, error) {
	return r.profile, nil
}

func (r *stubRepo) SlugAvailable(context.Context, string) (bool, string, error) {
	return true, "", nil
}

// stubCompanies отвечает одной ролью на любой вопрос о членстве,
// либо сообщает, что членства нет.
type stubCompanies struct {
	role   domain.Role
	absent bool
}

func (c stubCompanies) Membership(
	context.Context, uuid.UUID, uuid.UUID,
) (domain.Role, error) {
	if c.absent {
		return "", domain.ErrNotFound
	}
	return c.role, nil
}

// Регрессия: администратор, открывший собственную визитку, раньше получал
// доступ по ветке владельца и терял признак admin. Блокировка своей же
// страницы отвечала 403, и компанию-одиночку нельзя было снять с публикации.
func TestOwnerAdminCanBlockOwnProfile(t *testing.T) {
	userID := uuid.New()
	repo := &stubRepo{profile: domain.Profile{
		ID:        uuid.New(),
		UserID:    userID,
		CompanyID: uuid.New(),
		Status:    domain.ProfilePublished,
	}}
	svc := NewService(repo, stubCompanies{role: domain.RoleAdmin})

	got, err := svc.Block(context.Background(), repo.profile.ID, userID)
	if err != nil {
		t.Fatalf("Block: %v", err)
	}
	if got.Status != domain.ProfileBlocked {
		t.Errorf("статус = %q, ожидался %q", got.Status, domain.ProfileBlocked)
	}
}

// Обычный сотрудник блокировать не вправе даже свою визитку: скрывать
// страницы — операция администратора.
func TestOwnerEmployeeCannotBlockOwnProfile(t *testing.T) {
	userID := uuid.New()
	repo := &stubRepo{profile: domain.Profile{
		ID:        uuid.New(),
		UserID:    userID,
		CompanyID: uuid.New(),
		Status:    domain.ProfilePublished,
	}}
	svc := NewService(repo, stubCompanies{role: domain.RoleEmployee})

	if _, err := svc.Block(context.Background(), repo.profile.ID, userID); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("ошибка = %v, ожидалась ErrForbidden", err)
	}
}

// Исключённый сотрудник членства уже не имеет, но свою архивную визитку
// видеть должен — иначе она пропадает у него из кабинета без объяснений.
func TestFormerEmployeeKeepsAccessToOwnProfile(t *testing.T) {
	userID := uuid.New()
	repo := &stubRepo{profile: domain.Profile{
		ID:        uuid.New(),
		UserID:    userID,
		CompanyID: uuid.New(),
		Status:    domain.ProfileArchived,
	}}
	svc := NewService(repo, stubCompanies{absent: true})

	if _, err := svc.Get(context.Background(), repo.profile.ID, userID); err != nil {
		t.Errorf("Get: %v", err)
	}
}

// Посторонний не должен отличать чужую визитку от несуществующей:
// иначе по кодам ответов перебираются идентификаторы.
func TestStrangerGetsNotFound(t *testing.T) {
	repo := &stubRepo{profile: domain.Profile{
		ID:        uuid.New(),
		UserID:    uuid.New(),
		CompanyID: uuid.New(),
		Status:    domain.ProfilePublished,
	}}
	svc := NewService(repo, stubCompanies{absent: true})

	if _, err := svc.Get(context.Background(), repo.profile.ID, uuid.New()); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("ошибка = %v, ожидалась ErrNotFound", err)
	}
}
