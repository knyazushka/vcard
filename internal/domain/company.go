package domain

import (
	"time"

	"github.com/google/uuid"
)

// Company — тенант. Единица изоляции данных: всё, что принадлежит компании,
// доступно только её участникам.
type Company struct {
	ID        uuid.UUID
	Name      string
	LogoKey   string
	Address   string
	Phones    []Phone
	Status    string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Phone — телефон компании. Порядок значим: он определяет вид визитки.
type Phone struct {
	Value string
	Label string
}

// Position — должность из справочника компании.
type Position struct {
	ID         uuid.UUID
	CompanyID  uuid.UUID
	Title      string
	UsageCount int32
}

// Tag — тег из справочника компании.
type Tag struct {
	ID         uuid.UUID
	CompanyID  uuid.UUID
	Title      string
	UsageCount int32
}

// Employee — строка состава компании для админки.
type Employee struct {
	UserID        uuid.UUID
	Email         string
	Role          Role
	FullName      string
	ProfileID     uuid.UUID
	ProfileSlug   string
	ProfileStatus string
	JoinedAt      time.Time
}

// Page — параметры и результат постраничной выдачи.
type Page struct {
	Limit  int32
	Offset int32
}

// DefaultPositions — стартовый набор должностей, копируемый в новую компанию.
//
// Без него администратор попадает на пустой экран справочника и бросает
// настройку на первом же шаге: заполнять список с нуля никто не хочет.
var DefaultPositions = []string{
	"Генеральный директор",
	"Руководитель отдела",
	"Менеджер по продажам",
	"Менеджер по работе с клиентами",
	"Маркетолог",
	"Backend-разработчик",
	"Frontend-разработчик",
	"Дизайнер",
	"HR-менеджер",
	"Бухгалтер",
}
