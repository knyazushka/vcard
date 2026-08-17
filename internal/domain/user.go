package domain

import (
	"strings"
	"time"

	"github.com/google/uuid"
)

// Role — роль пользователя внутри конкретной компании.
type Role string

// Роли внутри компании. RoleEmployee — та самая минимальная роль из ТЗ,
// с которой сотруднику доступны визитка и её карточка.
const (
	RoleAdmin    Role = "ADMIN"
	RoleEmployee Role = "EMPLOYEE"
)

// User — учётная запись. Сама по себе не даёт доступа ни к одной компании.
type User struct {
	ID            uuid.UUID
	Email         string
	EmailVerified bool
	PasswordHash  string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// Membership — связка пользователя с компанией. Роль принадлежит именно ей,
// а не пользователю: один человек бывает администратором в одной компании
// и рядовым сотрудником в другой.
type Membership struct {
	CompanyID      uuid.UUID
	CompanyName    string
	CompanyLogoKey string
	Role           Role
	ProfileID      uuid.UUID
	CreatedAt      time.Time
}

// NormalizeEmail приводит адрес к каноничному виду для сравнения и хранения.
//
// Только регистр и пробелы. Точки и «плюс-адресацию» не трогаем: это
// внутренние правила конкретных почтовых провайдеров, они у всех разные
// и меняются — нормализуя их, мы однажды склеим двух разных людей в одного.
func NormalizeEmail(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// Session — цепочка refresh-токенов одного входа.
//
// FamilyID общий для всей цепочки ротаций и попадает в access-токен
// как sid: access переживает обмен refresh и остаётся привязан к входу,
// а не к конкретному экземпляру токена.
type Session struct {
	ID        uuid.UUID
	FamilyID  uuid.UUID
	UserID    uuid.UUID
	IssuedAt  time.Time
	ExpiresAt time.Time
	UsedAt    *time.Time
	RevokedAt *time.Time
	UserAgent string
	IP        string
}

// Active сообщает, годится ли сессия для обмена.
func (s Session) Active(now time.Time) bool {
	return s.RevokedAt == nil && s.UsedAt == nil && now.Before(s.ExpiresAt)
}
