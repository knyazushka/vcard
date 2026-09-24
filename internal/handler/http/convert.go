package http

import (
	"net/url"
	"time"

	"github.com/google/uuid"

	"github.com/knyazushka/vcard/internal/domain"
	"github.com/knyazushka/vcard/internal/gen/openapi"
)

// Преобразования домена в типы контракта.
//
// Сгенерированные структуры живут только здесь и в обработчиках: дальше
// вглубь они не проходят. Иначе смена версии OpenAPI переписывала бы домен.

func (a *API) company(c domain.Company) openapi.Company {
	return openapi.Company{
		ID:        openapi.UUID(c.ID),
		Name:      c.Name,
		LogoUrl:   optURL(a.files.URL(c.LogoKey)),
		Address:   optNilString(c.Address),
		Phones:    phones(c.Phones),
		CreatedAt: openapi.Timestamp(c.CreatedAt),
	}
}

func phones(in []domain.Phone) []openapi.Phone {
	out := make([]openapi.Phone, 0, len(in))
	for _, p := range in {
		out = append(out, openapi.Phone{
			Value: p.Value,
			Label: optNilString(p.Label),
		})
	}
	return out
}

func domainPhones(in []openapi.Phone) []domain.Phone {
	out := make([]domain.Phone, 0, len(in))
	for _, p := range in {
		out = append(out, domain.Phone{
			Value: p.Value,
			Label: p.Label.Or(""),
		})
	}
	return out
}

func position(p domain.Position) openapi.Position {
	return openapi.Position{
		ID:         openapi.UUID(p.ID),
		Title:      p.Title,
		UsageCount: openapi.NewOptInt32(p.UsageCount),
	}
}

func tag(t domain.Tag) openapi.Tag {
	return openapi.Tag{
		ID:         openapi.UUID(t.ID),
		Title:      t.Title,
		UsageCount: openapi.NewOptInt32(t.UsageCount),
	}
}

func employee(e domain.Employee) openapi.Employee {
	return openapi.Employee{
		UserId:        openapi.UUID(e.UserID),
		Email:         openapi.Email(e.Email),
		Role:          openapi.Role(e.Role),
		FullName:      optNilString(e.FullName),
		ProfileId:     optUUIDNullable(e.ProfileID),
		ProfileSlug:   optNilSlug(e.ProfileSlug),
		ProfileStatus: optNilProfileStatus(e.ProfileStatus),
		JoinedAt:      openapi.Timestamp(e.JoinedAt),
	}
}

// invitation переводит приглашение в тип контракта.
//
// Статус берётся вычисленный: в базе протухшее приглашение всё ещё PENDING,
// но действующим оно уже не является, и админка обязана показывать правду.
func invitationOut(inv domain.Invitation, now time.Time) openapi.Invitation {
	return openapi.Invitation{
		ID:             openapi.UUID(inv.ID),
		Email:          openapi.Email(inv.Email),
		Role:           openapi.Role(inv.Role),
		Status:         openapi.InvitationStatus(inv.EffectiveStatus(now)),
		ExpiresAt:      openapi.Timestamp(inv.ExpiresAt),
		CreatedAt:      openapi.Timestamp(inv.CreatedAt),
		InvitedByEmail: openapi.NewOptEmail(openapi.Email(inv.InvitedByEmail)),
		AcceptedAt:     optNilTimestamp(inv.AcceptedAt),
		LastSentAt:     optNilDateTime(inv.LastSentAt),
	}
}

func pageInfo(total int64, page domain.Page) openapi.PageInfo {
	return openapi.PageInfo{
		Total:  total,
		Limit:  page.Limit,
		Offset: page.Offset,
	}
}

func optNilString(s string) openapi.OptNilString {
	if s == "" {
		return openapi.OptNilString{Set: true, Null: true}
	}
	return openapi.NewOptNilString(s)
}

func optNilSlug(s string) openapi.OptNilSlugNullable {
	if s == "" {
		return openapi.OptNilSlugNullable{Set: true, Null: true}
	}
	return openapi.NewOptNilSlugNullable(openapi.SlugNullable(s))
}

func optNilProfileStatus(s string) openapi.OptNilProfileStatus {
	if s == "" {
		return openapi.OptNilProfileStatus{Set: true, Null: true}
	}
	return openapi.NewOptNilProfileStatus(openapi.ProfileStatus(s))
}

func optNilTimestamp(t *time.Time) openapi.OptNilTimestampNullable {
	if t == nil {
		return openapi.OptNilTimestampNullable{Set: true, Null: true}
	}
	return openapi.NewOptNilTimestampNullable(openapi.TimestampNullable(*t))
}

func optNilDateTime(t *time.Time) openapi.OptNilDateTime {
	if t == nil {
		return openapi.OptNilDateTime{Set: true, Null: true}
	}
	return openapi.NewOptNilDateTime(*t)
}

func optUUIDNullable(id uuid.UUID) openapi.OptNilUUIDNullable {
	if id == uuid.Nil {
		return openapi.OptNilUUIDNullable{Set: true, Null: true}
	}
	return openapi.NewOptNilUUIDNullable(openapi.UUIDNullable(id))
}

// optNilURL — опциональный адрес поля ответа. Пустой ключ и любой
// неразбираемый адрес дают явный null: отдать битую ссылку хуже,
// чем честно сказать, что файла нет.
func optNilURL(raw string) openapi.OptNilURLNullable { return optURL(raw) }

// mustURL нужен там, где схема требует непустой адрес. Пустое значение
// сюда не приходит: вызывающий проверяет ключ заранее.
func mustURL(raw string) openapi.URL {
	u, err := url.Parse(raw)
	if err != nil {
		return openapi.URL{}
	}
	return openapi.URL(*u)
}

// optNilURI — то же для полей, объявленных без явного nullable-двойника.
func optNilURI(raw string) openapi.OptNilURI {
	if raw == "" {
		return openapi.OptNilURI{Set: true, Null: true}
	}
	u, err := url.Parse(raw)
	if err != nil {
		return openapi.OptNilURI{Set: true, Null: true}
	}
	return openapi.NewOptNilURI(*u)
}

func uuidOf(v openapi.UUID) uuid.UUID { return uuid.UUID(v) }

// --- визитка -----------------------------------------------------------------

func (a *API) profile(p domain.Profile) openapi.Profile {
	out := openapi.Profile{
		ID:                  openapi.UUID(p.ID),
		CompanyId:           openapi.UUID(p.CompanyID),
		Slug:                openapi.Slug(p.Slug),
		Status:              openapi.ProfileStatus(p.Status),
		FullName:            optNilString(p.FullName),
		AboutHtml:           optNilString(p.AboutHTML),
		Tags:                profileTags(p.Tags),
		Contacts:            openapi.NewOptContacts(contacts(p.Contacts)),
		ShowCompanyContacts: p.ShowCompanyContacts,
		CardImageUrl:        optNilURI(a.files.URL(p.CardKey)),
		CreatedAt:           openapi.Timestamp(p.CreatedAt),
		UpdatedAt:           openapi.Timestamp(p.UpdatedAt),
	}

	if av := a.avatar(p); av != nil {
		out.Avatar = openapi.NewOptNilAvatar(*av)
	} else {
		out.Avatar = openapi.OptNilAvatar{Set: true, Null: true}
	}

	if !p.Position.Empty() {
		out.Position = openapi.NewOptNilProfilePosition(openapi.ProfilePosition{
			ID:     optUUID(p.Position.ID),
			Title:  p.Position.Title,
			Custom: !p.Position.FromDictionary(),
		})
	} else {
		out.Position = openapi.OptNilProfilePosition{Set: true, Null: true}
	}

	return out
}

// avatar собирает представление фотографии. nil означает, что её нет —
// вызывающий сам решает, как это выразить в своём ответе.
func (a *API) avatar(p domain.Profile) *openapi.Avatar {
	if p.AvatarKey == "" {
		return nil
	}

	out := openapi.Avatar{URL: mustURL(a.files.URL(p.AvatarKey))}
	if p.Crop != nil {
		out.Crop = openapi.NewOptNilCrop(openapi.Crop{
			X: p.Crop.X, Y: p.Crop.Y, Size: p.Crop.Size,
		})
	} else {
		out.Crop = openapi.OptNilCrop{Set: true, Null: true}
	}
	return &out
}

// publicProfile — проекция для страницы визитки.
//
// Отдельный тип, а не Profile с занулением: здесь нет ни идентификаторов,
// ни компании-владельца, ни служебных полей, и следующее поле, добавленное
// в Profile, физически не может сюда просочиться.
// avatarKey приходит извне, а не берётся из профиля: для публичной
// страницы это НАРЕЗАННЫЙ по кропу файл, и решение о его подготовке
// принимает обработчик — здесь только сборка ответа, без обращений
// к хранилищу.
func (a *API) publicProfile(p domain.Profile, avatarKey string) openapi.ProfilePublic {
	titles := make([]string, 0, len(p.Tags))
	for _, t := range p.Tags {
		titles = append(titles, t.Title)
	}

	out := openapi.ProfilePublic{
		Slug:          openapi.Slug(p.Slug),
		FullName:      optNilString(p.FullName),
		AvatarUrl:     optNilURL(a.files.URL(avatarKey)),
		PositionTitle: optNilString(p.Position.Title),
		AboutHtml:     optNilString(p.AboutHTML),
		Tags:          titles,
		Links:         openapi.NewOptPublicLinks(publicLinks(p.Contacts)),
		CardImageUrl:  optURL(a.files.URL(p.CardKey)),
	}

	// Реквизиты компании появляются на странице, только если сотрудник
	// сам включил их показ.
	if p.ShowCompanyContacts {
		out.Company = openapi.NewOptNilCompanyPublic(openapi.CompanyPublic{
			Name:    p.Company.Name,
			LogoUrl: optNilURL(a.files.URL(p.Company.LogoKey)),
			Address: optNilString(p.Company.Address),
			Phones:  phones(p.Company.Phones),
		})
	} else {
		out.Company = openapi.NewOptNilCompanyPublic(openapi.CompanyPublic{
			Name:    p.Company.Name,
			LogoUrl: optNilURL(a.files.URL(p.Company.LogoKey)),
			Address: optNilString(""),
		})
	}

	return out
}

func profileTags(in []domain.TagRef) []openapi.ProfileTag {
	out := make([]openapi.ProfileTag, 0, len(in))
	for _, t := range in {
		out = append(out, openapi.ProfileTag{
			ID:     optUUIDNullable(t.ID),
			Title:  t.Title,
			Custom: !t.FromDictionary(),
		})
	}
	return out
}

func contacts(c domain.Contacts) openapi.Contacts {
	return openapi.Contacts{
		Phone:    optNilString(c.Phone),
		Whatsapp: optNilString(c.WhatsApp),
		Telegram: optNilString(c.Telegram),
	}
}

// publicLinks отдаёт готовые ссылки: фронт не должен знать правила их сборки.
func publicLinks(c domain.Contacts) openapi.PublicLinks {
	return openapi.PublicLinks{
		Phone:    optNilString(c.Phone),
		Whatsapp: optNilURI(c.WhatsAppURL()),
		Telegram: optNilURI(c.TelegramURL()),
	}
}
