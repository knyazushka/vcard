package postgres

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/knyazushka/vcard/internal/domain"
)

// CompanyRepo — хранилище компаний, их справочников и состава.
type CompanyRepo struct {
	db *pgxpool.Pool
}

// NewCompanyRepo создаёт хранилище компаний.
func NewCompanyRepo(db *pgxpool.Pool) *CompanyRepo { return &CompanyRepo{db: db} }

// Create заводит компанию, делает создателя её администратором, наполняет
// справочник должностей стартовым набором и создаёт владельцу черновик
// визитки — одной транзакцией.
//
// Порядок не случаен: членство обязано появиться до коммита, иначе
// отложенный триггер увидит компанию без администратора и откатит всё.
func (r *CompanyRepo) Create(
	ctx context.Context, c domain.Company, ownerID uuid.UUID, slugCandidates []string,
) (domain.Company, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return domain.Company{}, fmt.Errorf("begin create company: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const insertCompany = `
		insert into companies (id, name, address)
		values ($1, $2, nullif($3, ''))
		returning created_at, updated_at`

	c.ID = uuid.New()
	if err := tx.QueryRow(ctx, insertCompany, c.ID, c.Name, c.Address).
		Scan(&c.CreatedAt, &c.UpdatedAt); err != nil {
		return domain.Company{}, fmt.Errorf("insert company: %w", mapError(err))
	}

	if err := replacePhones(ctx, tx, c.ID, c.Phones); err != nil {
		return domain.Company{}, err
	}

	const insertMembership = `
		insert into memberships (user_id, company_id, role) values ($1, $2, 'ADMIN')`

	if _, err := tx.Exec(ctx, insertMembership, ownerID, c.ID); err != nil {
		return domain.Company{}, fmt.Errorf("insert owner membership: %w", mapError(err))
	}

	const seedPositions = `
		insert into positions (company_id, title)
		select $1, unnest($2::text[])`

	if _, err := tx.Exec(ctx, seedPositions, c.ID, domain.DefaultPositions); err != nil {
		return domain.Company{}, fmt.Errorf("seed positions: %w", mapError(err))
	}

	// Создатель компании — такой же сотрудник, как и остальные, и визитка
	// ему нужна не меньше. Без этого он единственный в системе оказывается
	// с членством, но без страницы, и половина сценариев профиля для него
	// не работает.
	if _, _, err := ensureProfile(ctx, tx, ownerID, c.ID, slugCandidates); err != nil {
		return domain.Company{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return domain.Company{}, fmt.Errorf("commit create company: %w", mapError(err))
	}

	c.Status = "ACTIVE"
	return c, nil
}

// ByID отдаёт компанию вместе с телефонами.
func (r *CompanyRepo) ByID(ctx context.Context, id uuid.UUID) (domain.Company, error) {
	const q = `
		select id, name, coalesce(logo_key, ''), coalesce(address, ''), status, created_at, updated_at
		  from companies where id = $1`

	var c domain.Company
	err := r.db.QueryRow(ctx, q, id).Scan(
		&c.ID, &c.Name, &c.LogoKey, &c.Address, &c.Status, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return domain.Company{}, mapError(err)
	}

	c.Phones, err = r.phones(ctx, id)
	if err != nil {
		return domain.Company{}, err
	}
	return c, nil
}

// Update меняет поля компании. nil означает «не трогать».
func (r *CompanyRepo) Update(ctx context.Context, id uuid.UUID, name, address *string, phones []domain.Phone, replacePhoneList bool) (domain.Company, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return domain.Company{}, fmt.Errorf("begin update company: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// coalesce($n, column) оставляет колонку как есть, когда параметр nil —
	// так одна инструкция обслуживает частичное обновление без склейки SQL
	// из кусков.
	const q = `
		update companies
		   set name    = coalesce($2, name),
		       address = case when $4 then nullif($3, '') else address end
		 where id = $1`

	addressGiven := address != nil
	var addressVal string
	if addressGiven {
		addressVal = *address
	}

	tag, err := tx.Exec(ctx, q, id, name, addressVal, addressGiven)
	if err != nil {
		return domain.Company{}, fmt.Errorf("update company: %w", mapError(err))
	}
	if tag.RowsAffected() == 0 {
		return domain.Company{}, domain.ErrNotFound
	}

	if replacePhoneList {
		if err := replacePhones(ctx, tx, id, phones); err != nil {
			return domain.Company{}, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return domain.Company{}, fmt.Errorf("commit update company: %w", mapError(err))
	}
	return r.ByID(ctx, id)
}

// SetLogoKey сохраняет ключ логотипа; пустая строка убирает его.
func (r *CompanyRepo) SetLogoKey(ctx context.Context, id uuid.UUID, key string) error {
	const q = `update companies set logo_key = nullif($2, '') where id = $1`

	tag, err := r.db.Exec(ctx, q, id, key)
	if err != nil {
		return fmt.Errorf("set logo key: %w", mapError(err))
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *CompanyRepo) phones(ctx context.Context, companyID uuid.UUID) ([]domain.Phone, error) {
	const q = `
		select value, coalesce(label, '')
		  from company_phones where company_id = $1 order by sort_order`

	rows, err := r.db.Query(ctx, q, companyID)
	if err != nil {
		return nil, fmt.Errorf("query phones: %w", mapError(err))
	}
	defer rows.Close()

	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Phone, error) {
		var p domain.Phone
		return p, row.Scan(&p.Value, &p.Label)
	})
	if err != nil {
		return nil, fmt.Errorf("scan phones: %w", err)
	}
	return out, nil
}

// replacePhones переписывает список целиком: API заменяет его как единое
// значение, поэтому вычислять разницу построчно незачем.
func replacePhones(ctx context.Context, q execer, companyID uuid.UUID, phones []domain.Phone) error {
	if _, err := q.Exec(ctx, `delete from company_phones where company_id = $1`, companyID); err != nil {
		return fmt.Errorf("clear phones: %w", mapError(err))
	}

	for i, p := range phones {
		const insert = `
			insert into company_phones (company_id, sort_order, value, label)
			values ($1, $2, $3, nullif($4, ''))`

		if _, err := q.Exec(ctx, insert, companyID, i, p.Value, p.Label); err != nil {
			return fmt.Errorf("insert phone: %w", mapError(err))
		}
	}
	return nil
}

// --- членство ---------------------------------------------------------------

// Membership отдаёт роль пользователя в компании.
//
// Возвращает ErrNotFound и когда компании нет, и когда пользователь в ней
// не состоит: снаружи это один и тот же ответ 404, иначе по кодам ответов
// перебираются чужие идентификаторы.
func (r *CompanyRepo) Membership(ctx context.Context, companyID, userID uuid.UUID) (domain.Role, error) {
	const q = `select role from memberships where company_id = $1 and user_id = $2`

	var role domain.Role
	if err := r.db.QueryRow(ctx, q, companyID, userID).Scan(&role); err != nil {
		return "", mapError(err)
	}
	return role, nil
}

// Employees отдаёт состав компании вместе с профилями сотрудников.
func (r *CompanyRepo) Employees(ctx context.Context, companyID uuid.UUID, page domain.Page) ([]domain.Employee, int64, error) {
	const countQ = `select count(*) from memberships where company_id = $1`

	var total int64
	if err := r.db.QueryRow(ctx, countQ, companyID).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count employees: %w", mapError(err))
	}

	const q = `
		select m.user_id, u.email, m.role,
		       coalesce(p.full_name, ''),
		       coalesce(p.id, '00000000-0000-0000-0000-000000000000'::uuid),
		       coalesce(p.slug::text, ''),
		       coalesce(p.status, ''),
		       m.created_at
		  from memberships m
		  join users u on u.id = m.user_id
		  left join profiles p on p.user_id = m.user_id and p.company_id = m.company_id
		 where m.company_id = $1
		 order by m.created_at
		 limit $2 offset $3`

	rows, err := r.db.Query(ctx, q, companyID, page.Limit, page.Offset)
	if err != nil {
		return nil, 0, fmt.Errorf("query employees: %w", mapError(err))
	}
	defer rows.Close()

	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Employee, error) {
		var e domain.Employee
		return e, row.Scan(&e.UserID, &e.Email, &e.Role, &e.FullName,
			&e.ProfileID, &e.ProfileSlug, &e.ProfileStatus, &e.JoinedAt)
	})
	if err != nil {
		return nil, 0, fmt.Errorf("scan employees: %w", err)
	}
	return out, total, nil
}

// EmployeeByID отдаёт одну строку состава.
func (r *CompanyRepo) EmployeeByID(ctx context.Context, companyID, userID uuid.UUID) (domain.Employee, error) {
	employees, _, err := r.Employees(ctx, companyID, domain.Page{Limit: 1000})
	if err != nil {
		return domain.Employee{}, err
	}
	for _, e := range employees {
		if e.UserID == userID {
			return e, nil
		}
	}
	return domain.Employee{}, domain.ErrNotFound
}

// SetRole меняет роль участника.
//
// Отказ прилетает из отложенного триггера в момент коммита, поэтому
// проверять «а остался ли админ» здесь незачем и вредно: между проверкой
// и записью встанет другая транзакция.
func (r *CompanyRepo) SetRole(ctx context.Context, companyID, userID uuid.UUID, role domain.Role) error {
	const q = `update memberships set role = $3 where company_id = $1 and user_id = $2`

	tag, err := r.db.Exec(ctx, q, companyID, userID, role)
	if err != nil {
		return fmt.Errorf("set role: %w", mapError(err))
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// RemoveEmployee исключает участника и архивирует его профиль.
//
// Профиль не удаляется: по ТЗ адрес визитки не освобождается ни при
// блокировке, ни при уходе сотрудника.
func (r *CompanyRepo) RemoveEmployee(ctx context.Context, companyID, userID uuid.UUID) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin remove employee: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const archive = `
		update profiles set status = 'ARCHIVED'
		 where company_id = $1 and user_id = $2 and status <> 'ARCHIVED'`

	if _, err := tx.Exec(ctx, archive, companyID, userID); err != nil {
		return fmt.Errorf("archive profile: %w", mapError(err))
	}

	const remove = `delete from memberships where company_id = $1 and user_id = $2`

	tag, err := tx.Exec(ctx, remove, companyID, userID)
	if err != nil {
		return fmt.Errorf("remove membership: %w", mapError(err))
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit remove employee: %w", mapError(err))
	}
	return nil
}

// --- справочники ------------------------------------------------------------

// Positions отдаёт справочник должностей со счётчиком использований.
func (r *CompanyRepo) Positions(ctx context.Context, companyID uuid.UUID) ([]domain.Position, error) {
	const q = `
		select p.id, p.company_id, p.title, count(pr.id)
		  from positions p
		  left join profiles pr on pr.position_id = p.id
		 where p.company_id = $1
		 group by p.id
		 order by p.title`

	return collectDictionary[domain.Position](ctx, r.db, q, companyID, func(row pgx.CollectableRow) (domain.Position, error) {
		var p domain.Position
		return p, row.Scan(&p.ID, &p.CompanyID, &p.Title, &p.UsageCount)
	})
}

// CreatePosition добавляет должность в справочник компании.
func (r *CompanyRepo) CreatePosition(ctx context.Context, companyID uuid.UUID, title string) (domain.Position, error) {
	const q = `insert into positions (company_id, title) values ($1, $2) returning id`

	p := domain.Position{CompanyID: companyID, Title: title}
	if err := r.db.QueryRow(ctx, q, companyID, title).Scan(&p.ID); err != nil {
		return domain.Position{}, mapError(err)
	}
	return p, nil
}

// UpdatePosition переименовывает должность.
func (r *CompanyRepo) UpdatePosition(ctx context.Context, companyID, id uuid.UUID, title string) (domain.Position, error) {
	const q = `update positions set title = $3 where company_id = $1 and id = $2`

	tag, err := r.db.Exec(ctx, q, companyID, id, title)
	if err != nil {
		return domain.Position{}, mapError(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.Position{}, domain.ErrNotFound
	}
	return domain.Position{ID: id, CompanyID: companyID, Title: title}, nil
}

// DeletePosition убирает должность из справочника.
//
// Профили, ссылавшиеся на неё, сохраняют название как введённое вручную —
// этим занимается триггер в базе.
func (r *CompanyRepo) DeletePosition(ctx context.Context, companyID, id uuid.UUID) error {
	const q = `delete from positions where company_id = $1 and id = $2`

	tag, err := r.db.Exec(ctx, q, companyID, id)
	if err != nil {
		return mapError(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// Tags отдаёт справочник тегов со счётчиком использований.
func (r *CompanyRepo) Tags(ctx context.Context, companyID uuid.UUID) ([]domain.Tag, error) {
	const q = `
		select t.id, t.company_id, t.title, count(pt.profile_id)
		  from tags t
		  left join profile_tags pt on pt.tag_id = t.id
		 where t.company_id = $1
		 group by t.id
		 order by t.title`

	return collectDictionary[domain.Tag](ctx, r.db, q, companyID, func(row pgx.CollectableRow) (domain.Tag, error) {
		var t domain.Tag
		return t, row.Scan(&t.ID, &t.CompanyID, &t.Title, &t.UsageCount)
	})
}

// CreateTag добавляет тег в справочник компании.
func (r *CompanyRepo) CreateTag(ctx context.Context, companyID uuid.UUID, title string) (domain.Tag, error) {
	const q = `insert into tags (company_id, title) values ($1, $2) returning id`

	t := domain.Tag{CompanyID: companyID, Title: title}
	if err := r.db.QueryRow(ctx, q, companyID, title).Scan(&t.ID); err != nil {
		return domain.Tag{}, mapError(err)
	}
	return t, nil
}

// DeleteTag убирает тег из справочника; у профилей он становится своим.
func (r *CompanyRepo) DeleteTag(ctx context.Context, companyID, id uuid.UUID) error {
	const q = `delete from tags where company_id = $1 and id = $2`

	tag, err := r.db.Exec(ctx, q, companyID, id)
	if err != nil {
		return mapError(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func collectDictionary[T any](
	ctx context.Context, db *pgxpool.Pool, q string, companyID uuid.UUID,
	scan func(pgx.CollectableRow) (T, error),
) ([]T, error) {
	rows, err := db.Query(ctx, q, companyID)
	if err != nil {
		return nil, fmt.Errorf("query dictionary: %w", mapError(err))
	}
	defer rows.Close()

	out, err := pgx.CollectRows(rows, scan)
	if err != nil {
		return nil, fmt.Errorf("scan dictionary: %w", err)
	}
	return out, nil
}
