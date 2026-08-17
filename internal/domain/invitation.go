package domain

import (
	"time"

	"github.com/google/uuid"
)

// InvitationStatus — состояние приглашения в базе.
//
// Значения EXPIRED здесь нет намеренно: истечение вычисляется из expires_at
// при чтении. Иначе состояние строки зависело бы от того, жив ли воркер,
// а приглашение считалось бы действующим ровно до тех пор, пока кто-то
// не удосужился его пометить.
type InvitationStatus string

// Состояния приглашения.
const (
	InvitationPending  InvitationStatus = "PENDING"
	InvitationAccepted InvitationStatus = "ACCEPTED"
	InvitationRevoked  InvitationStatus = "REVOKED"
	// InvitationExpired в базу не пишется и появляется только на выдаче.
	InvitationExpired InvitationStatus = "EXPIRED"
)

// Invitation — приглашение в компанию.
//
// Самого токена здесь нет и быть не может: в базе лежит только его sha256,
// а исходное значение существует единственный раз — в отправленном письме.
type Invitation struct {
	ID             uuid.UUID
	CompanyID      uuid.UUID
	CompanyName    string
	CompanyLogoKey string
	Email          string
	Role           Role
	Status         InvitationStatus
	InvitedBy      uuid.UUID
	InvitedByEmail string
	ExpiresAt      time.Time
	AcceptedAt     *time.Time
	LastSentAt     *time.Time
	CreatedAt      time.Time
}

// EffectiveStatus отдаёт состояние с учётом срока: протухшее приглашение
// остаётся в базе PENDING, но действующим уже не является.
func (i Invitation) EffectiveStatus(now time.Time) InvitationStatus {
	if i.Status == InvitationPending && now.After(i.ExpiresAt) {
		return InvitationExpired
	}
	return i.Status
}

// Usable сообщает, можно ли принять приглашение прямо сейчас.
func (i Invitation) Usable(now time.Time) bool {
	return i.EffectiveStatus(now) == InvitationPending
}
