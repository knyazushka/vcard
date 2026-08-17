package domain

import (
	"strings"
	"time"

	"github.com/google/uuid"
)

// ProfileStatus — состояние визитки.
type ProfileStatus string

// Состояния визитки.
//
// Адрес не освобождается ни в одном из них — это требование ТЗ, поэтому
// уникальный индекс по слагу безусловный, без partial-условия.
const (
	// ProfileDraft создаётся вместе с членством и наружу не виден.
	ProfileDraft ProfileStatus = "DRAFT"
	// ProfilePublished открывается по ссылке.
	ProfilePublished ProfileStatus = "PUBLISHED"
	// ProfileBlocked заблокирован администратором: отдаётся заглушка.
	ProfileBlocked ProfileStatus = "BLOCKED"
	// ProfileArchived — сотрудник исключён из компании.
	ProfileArchived ProfileStatus = "ARCHIVED"
)

// MaxProfileTags — потолок числа тегов. Ровно столько требуется
// для публикации; у черновика может быть меньше.
const MaxProfileTags = 3

// Profile — визитка сотрудника.
type Profile struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	CompanyID uuid.UUID
	Slug      string
	Status    ProfileStatus

	FullName  string
	AvatarKey string
	Crop      *Crop

	Position PositionRef
	Tags     []TagRef

	AboutHTML string
	AboutText string

	Contacts            Contacts
	ShowCompanyContacts bool

	CardKey string

	Company   Company
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Crop — область кадрирования аватара в пикселях исходного изображения.
//
// Хранится метаданными, а не выжигается в файл: исходник остаётся целым,
// и кроп можно переиграть, не заставляя человека загружать фотографию заново.
type Crop struct {
	X, Y, Size int
}

// PositionRef — должность: либо из справочника, либо своя, но не обе сразу.
type PositionRef struct {
	ID     uuid.UUID
	Custom string
	// Title заполняется на чтении: для справочной должности — из справочника,
	// для своей — из Custom.
	Title string
}

// FromDictionary сообщает, ссылается ли должность на справочник.
func (p PositionRef) FromDictionary() bool { return p.ID != uuid.Nil }

// Empty сообщает, что должность не задана.
func (p PositionRef) Empty() bool { return p.ID == uuid.Nil && p.Custom == "" }

// TagRef — тег профиля: либо из справочника компании, либо свой.
type TagRef struct {
	ID     uuid.UUID
	Custom string
	Title  string
}

// FromDictionary сообщает, ссылается ли тег на справочник.
func (t TagRef) FromDictionary() bool { return t.ID != uuid.Nil }

// Contacts — способы связи, которые сотрудник выносит на визитку.
type Contacts struct {
	Phone    string
	WhatsApp string
	Telegram string
}

// WhatsAppURL собирает ссылку вида wa.me/{номер}.
//
// Ссылка строится на выдаче, а не хранится: правило её сборки принадлежит
// WhatsApp и может измениться, а в базе тогда останутся тысячи устаревших URL.
func (c Contacts) WhatsAppURL() string {
	digits := digitsOnly(c.WhatsApp)
	if digits == "" {
		return ""
	}
	return "https://wa.me/" + digits
}

// TelegramURL собирает ссылку вида t.me/{аккаунт}.
func (c Contacts) TelegramURL() string {
	account := strings.TrimPrefix(strings.TrimSpace(c.Telegram), "@")
	if account == "" {
		return ""
	}
	return "https://t.me/" + account
}

// NormalizePhone оставляет от номера только цифры, сохраняя ведущий плюс.
//
// Полноценная проверка по E.164 потребовала бы библиотеки с базой кодов стран;
// пока номер только показывается и превращается в ссылку, этого достаточно.
// Когда понадобится проверять принадлежность номера стране — например, для
// SMS — сюда придёт phonenumbers, и место для этого будет ровно одно.
func NormalizePhone(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	digits := digitsOnly(s)
	if digits == "" {
		return ""
	}
	if strings.HasPrefix(s, "+") {
		return "+" + digits
	}
	return digits
}

func digitsOnly(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// NormalizeTelegram приводит аккаунт к виду без «собаки» и ссылки.
func NormalizeTelegram(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "https://t.me/")
	s = strings.TrimPrefix(s, "t.me/")
	return strings.TrimPrefix(s, "@")
}

// ProfileUpdate — частичное изменение визитки.
//
// nil означает «не трогать», а не «очистить»: без этого различия PATCH
// с одним полем стирал бы всё остальное.
type ProfileUpdate struct {
	FullName  *string
	Position  *PositionRef
	AboutHTML *string
	// AboutText — та же запись без разметки. Считается вместе с очисткой
	// HTML, чтобы обе версии всегда были согласованы между собой.
	AboutText           string
	Tags                *[]TagRef
	Contacts            *Contacts
	ShowCompanyContacts *bool
}

// ReadyToPublish проверяет условия публикации.
//
// Требования из ТЗ: имя и ровно три тега. Проверка живёт здесь, а не в базе:
// у черновика тегов может быть меньше, и constraint на «ровно три» ломал бы
// каждое промежуточное сохранение.
func (p Profile) ReadyToPublish() error {
	if strings.TrimSpace(p.FullName) == "" {
		return ErrProfileIncomplete
	}
	if len(p.Tags) != MaxProfileTags {
		return ErrProfileIncomplete
	}
	return nil
}

// PublicVisible сообщает, показывать ли страницу целиком.
func (p Profile) PublicVisible() bool { return p.Status == ProfilePublished }
