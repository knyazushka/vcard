package postgres

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/knyazushka/vcard/internal/domain"
)

// ProfileRepo — хранилище визиток.
type ProfileRepo struct {
	db *pgxpool.Pool
}

// NewProfileRepo создаёт хранилище визиток.
func NewProfileRepo(db *pgxpool.Pool) *ProfileRepo { return &ProfileRepo{db: db} }

const profileColumns = `
	p.id, p.user_id, p.company_id, p.slug::text, p.status,
	coalesce(p.full_name, ''), coalesce(p.avatar_key, ''),
	p.avatar_crop_x, p.avatar_crop_y, p.avatar_crop_size,
	p.position_id, coalesce(p.position_custom, ''), coalesce(pos.title, ''),
	coalesce(p.about_html, ''), coalesce(p.about_text, ''),
	coalesce(p.phone, ''), coalesce(p.whatsapp, ''), coalesce(p.telegram, ''),
	p.show_company_contacts, coalesce(p.card_key, ''),
	p.created_at, p.updated_at,
	c.id, c.name, coalesce(c.logo_key, ''), coalesce(c.address, '')`

func scanProfile(row pgx.CollectableRow) (domain.Profile, error) {
	var p domain.Profile
	var positionID *uuid.UUID
	var cropX, cropY, cropSize *int

	err := row.Scan(
		&p.ID, &p.UserID, &p.CompanyID, &p.Slug, &p.Status,
		&p.FullName, &p.AvatarKey,
		&cropX, &cropY, &cropSize,
		&positionID, &p.Position.Custom, &p.Position.Title,
		&p.AboutHTML, &p.AboutText,
		&p.Contacts.Phone, &p.Contacts.WhatsApp, &p.Contacts.Telegram,
		&p.ShowCompanyContacts, &p.CardKey,
		&p.CreatedAt, &p.UpdatedAt,
		&p.Company.ID, &p.Company.Name, &p.Company.LogoKey, &p.Company.Address,
	)
	if err != nil {
		return domain.Profile{}, err
	}

	if positionID != nil {
		p.Position.ID = *positionID
	} else if p.Position.Custom != "" {
		p.Position.Title = p.Position.Custom
	}
	if cropX != nil && cropY != nil && cropSize != nil {
		p.Crop = &domain.Crop{X: *cropX, Y: *cropY, Size: *cropSize}
	}
	return p, nil
}

// ByID отдаёт визитку по идентификатору.
func (r *ProfileRepo) ByID(ctx context.Context, id uuid.UUID) (domain.Profile, error) {
	q := `select ` + profileColumns + `
		    from profiles p
		    join companies c on c.id = p.company_id
		    left join positions pos on pos.id = p.position_id
		   where p.id = $1`

	return r.one(ctx, q, id)
}

// BySlug отдаёт визитку по публичному адресу.
func (r *ProfileRepo) BySlug(ctx context.Context, slug string) (domain.Profile, error) {
	q := `select ` + profileColumns + `
		    from profiles p
		    join companies c on c.id = p.company_id
		    left join positions pos on pos.id = p.position_id
		   where p.slug = $1`

	return r.one(ctx, q, slug)
}

func (r *ProfileRepo) one(ctx context.Context, q string, arg any) (domain.Profile, error) {
	rows, err := r.db.Query(ctx, q, arg)
	if err != nil {
		return domain.Profile{}, mapError(err)
	}
	defer rows.Close()

	p, err := pgx.CollectExactlyOneRow(rows, scanProfile)
	if err != nil {
		return domain.Profile{}, mapError(err)
	}

	p.Tags, err = r.tags(ctx, p.ID)
	if err != nil {
		return domain.Profile{}, err
	}
	if p.ShowCompanyContacts {
		p.Company.Phones, err = r.companyPhones(ctx, p.CompanyID)
		if err != nil {
			return domain.Profile{}, err
		}
	}
	return p, nil
}

func (r *ProfileRepo) tags(ctx context.Context, profileID uuid.UUID) ([]domain.TagRef, error) {
	const q = `
		select pt.tag_id, coalesce(pt.custom_title, ''), coalesce(t.title, pt.custom_title, '')
		  from profile_tags pt
		  left join tags t on t.id = pt.tag_id
		 where pt.profile_id = $1
		 order by pt.sort_order`

	rows, err := r.db.Query(ctx, q, profileID)
	if err != nil {
		return nil, fmt.Errorf("query profile tags: %w", mapError(err))
	}
	defer rows.Close()

	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.TagRef, error) {
		var t domain.TagRef
		var id *uuid.UUID
		if err := row.Scan(&id, &t.Custom, &t.Title); err != nil {
			return t, err
		}
		if id != nil {
			t.ID = *id
		}
		return t, nil
	})
	if err != nil {
		return nil, fmt.Errorf("scan profile tags: %w", err)
	}
	return out, nil
}

func (r *ProfileRepo) companyPhones(ctx context.Context, companyID uuid.UUID) ([]domain.Phone, error) {
	const q = `
		select value, coalesce(label, '')
		  from company_phones where company_id = $1 order by sort_order`

	rows, err := r.db.Query(ctx, q, companyID)
	if err != nil {
		return nil, fmt.Errorf("query company phones: %w", mapError(err))
	}
	defer rows.Close()

	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Phone, error) {
		var p domain.Phone
		return p, row.Scan(&p.Value, &p.Label)
	})
	if err != nil {
		return nil, fmt.Errorf("scan company phones: %w", err)
	}
	return out, nil
}

// Update применяет частичное изменение визитки.
//
// Теги переписываются целиком, когда переданы: список короткий и клиент
// присылает его как единое значение, поэтому вычислять разницу построчно
// незачем. Проверка принадлежности тега компании — в самом запросе вставки,
// иначе сотрудник одной компании подставил бы тег чужой.
func (r *ProfileRepo) Update(ctx context.Context, id uuid.UUID, u domain.ProfileUpdate) (domain.Profile, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return domain.Profile{}, fmt.Errorf("begin update profile: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const q = `
		update profiles
		   set full_name       = case when $2 then nullif($3, '')  else full_name end,
		       position_id     = case when $4 then $5              else position_id end,
		       position_custom = case when $4 then nullif($6, '')  else position_custom end,
		       about_html      = case when $7 then nullif($8, '')  else about_html end,
		       about_text      = case when $7 then nullif($9, '')  else about_text end,
		       phone           = case when $10 then nullif($11, '') else phone end,
		       whatsapp        = case when $10 then nullif($12, '') else whatsapp end,
		       telegram        = case when $10 then nullif($13, '') else telegram end,
		       show_company_contacts = coalesce($14, show_company_contacts)
		 where id = $1`

	var (
		positionID     *uuid.UUID
		positionCustom string
	)
	if u.Position != nil {
		if u.Position.ID != uuid.Nil {
			positionID = &u.Position.ID
		}
		positionCustom = u.Position.Custom
	}

	var contacts domain.Contacts
	if u.Contacts != nil {
		contacts = *u.Contacts
	}

	tag, err := tx.Exec(ctx, q, id,
		u.FullName != nil, deref(u.FullName),
		u.Position != nil, positionID, positionCustom,
		u.AboutHTML != nil, deref(u.AboutHTML), derefAboutText(u),
		u.Contacts != nil, contacts.Phone, contacts.WhatsApp, contacts.Telegram,
		u.ShowCompanyContacts,
	)
	if err != nil {
		return domain.Profile{}, fmt.Errorf("update profile: %w", mapError(err))
	}
	if tag.RowsAffected() == 0 {
		return domain.Profile{}, domain.ErrNotFound
	}

	if u.Tags != nil {
		if err := replaceProfileTags(ctx, tx, id, *u.Tags); err != nil {
			return domain.Profile{}, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return domain.Profile{}, fmt.Errorf("commit update profile: %w", mapError(err))
	}
	return r.ByID(ctx, id)
}

func replaceProfileTags(ctx context.Context, tx pgx.Tx, profileID uuid.UUID, tags []domain.TagRef) error {
	if _, err := tx.Exec(ctx, `delete from profile_tags where profile_id = $1`, profileID); err != nil {
		return fmt.Errorf("clear profile tags: %w", mapError(err))
	}

	for i, t := range tags {
		if t.FromDictionary() {
			// Тег берётся только из справочника СВОЕЙ компании: подзапрос
			// сверяет company_id профиля и тега, поэтому чужой идентификатор
			// просто не вставится.
			const insertRef = `
				insert into profile_tags (profile_id, sort_order, tag_id)
				select $1, $2, t.id
				  from tags t
				  join profiles p on p.id = $1
				 where t.id = $3 and t.company_id = p.company_id`

			res, err := tx.Exec(ctx, insertRef, profileID, i, t.ID)
			if err != nil {
				return fmt.Errorf("insert profile tag: %w", mapError(err))
			}
			if res.RowsAffected() == 0 {
				return domain.ErrNotFound
			}
			continue
		}

		const insertCustom = `
			insert into profile_tags (profile_id, sort_order, custom_title) values ($1, $2, $3)`

		if _, err := tx.Exec(ctx, insertCustom, profileID, i, t.Custom); err != nil {
			return fmt.Errorf("insert custom tag: %w", mapError(err))
		}
	}
	return nil
}

// SetAvatar сохраняет ключ файла и область кадрирования.
func (r *ProfileRepo) SetAvatar(ctx context.Context, id uuid.UUID, key string, crop *domain.Crop) (domain.Profile, error) {
	const q = `
		update profiles
		   set avatar_key       = nullif($2, ''),
		       avatar_crop_x    = $3,
		       avatar_crop_y    = $4,
		       avatar_crop_size = $5
		 where id = $1`

	var x, y, size *int
	if crop != nil {
		x, y, size = &crop.X, &crop.Y, &crop.Size
	}

	tag, err := r.db.Exec(ctx, q, id, key, x, y, size)
	if err != nil {
		return domain.Profile{}, fmt.Errorf("set avatar: %w", mapError(err))
	}
	if tag.RowsAffected() == 0 {
		return domain.Profile{}, domain.ErrNotFound
	}
	return r.ByID(ctx, id)
}

// UpdateSlug меняет адрес визитки.
//
// Прежний адрес освобождается — в отличие от блокировки, где он удерживается.
func (r *ProfileRepo) UpdateSlug(ctx context.Context, id uuid.UUID, slug string) (domain.Profile, error) {
	const q = `update profiles set slug = $2 where id = $1`

	tag, err := r.db.Exec(ctx, q, id, slug)
	if err != nil {
		return domain.Profile{}, mapError(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.Profile{}, domain.ErrNotFound
	}
	return r.ByID(ctx, id)
}

// SlugAvailable проверяет, свободен ли адрес.
//
// Ответ носит рекомендательный характер: между проверкой и сохранением
// адрес может занять кто-то другой, поэтому окончательное решение принимает
// уникальный индекс при записи.
func (r *ProfileRepo) SlugAvailable(ctx context.Context, slug string) (available bool, reason string, err error) {
	const q = `
		select exists (select 1 from reserved_slugs where slug = $1),
		       exists (select 1 from profiles where slug = $1)`

	var reserved, taken bool
	if err := r.db.QueryRow(ctx, q, slug).Scan(&reserved, &taken); err != nil {
		return false, "", fmt.Errorf("check slug: %w", mapError(err))
	}

	switch {
	case reserved:
		return false, "slug_reserved", nil
	case taken:
		return false, "slug_taken", nil
	default:
		return true, "", nil
	}
}

// SetStatus меняет состояние визитки.
func (r *ProfileRepo) SetStatus(ctx context.Context, id uuid.UUID, status domain.ProfileStatus) (domain.Profile, error) {
	const q = `update profiles set status = $2 where id = $1`

	tag, err := r.db.Exec(ctx, q, id, status)
	if err != nil {
		return domain.Profile{}, mapError(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.Profile{}, domain.ErrNotFound
	}
	return r.ByID(ctx, id)
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// derefAboutText отдаёт текстовую версию «Обо мне». Она приходит вместе
// с HTML: считает её сервис, чтобы очистка и извлечение текста происходили
// в одном месте и по одним правилам.
func derefAboutText(u domain.ProfileUpdate) string {
	if u.AboutHTML == nil {
		return ""
	}
	return u.AboutText
}

// SetCardKey запоминает путь к отрисованной карточке.
//
// Ключ содержит хэш содержимого, поэтому запись сюда — единственное, что
// связывает профиль с актуальной картинкой: старые файлы просто перестают
// упоминаться.
func (r *ProfileRepo) SetCardKey(ctx context.Context, id uuid.UUID, key string) error {
	const q = `update profiles set card_key = nullif($2, '') where id = $1`

	if _, err := r.db.Exec(ctx, q, id, key); err != nil {
		return fmt.Errorf("set card key: %w", mapError(err))
	}
	return nil
}
