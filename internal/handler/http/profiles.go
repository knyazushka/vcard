package http

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/knyazushka/vcard/internal/domain"
	"github.com/knyazushka/vcard/internal/gen/openapi"
	profilesvc "github.com/knyazushka/vcard/internal/service/profile"
)

// GetProfile реализует операцию getProfile.
func (a *API) GetProfile(ctx context.Context, params openapi.GetProfileParams) (openapi.GetProfileRes, error) {
	actor, err := a.principal(ctx)
	if err != nil {
		return nil, err
	}

	p, err := a.profiles.Get(ctx, uuidOf(params.ProfileId), actor.UserID)
	if err != nil {
		return nil, err
	}

	out := a.profile(p)
	return &out, nil
}

// UpdateProfile реализует операцию updateProfile.
func (a *API) UpdateProfile(
	ctx context.Context, req *openapi.UpdateProfileRequest, params openapi.UpdateProfileParams,
) (openapi.UpdateProfileRes, error) {
	actor, err := a.principal(ctx)
	if err != nil {
		return nil, err
	}

	in := profilesvc.UpdateInput{
		// Должность считается заданной, если пришло хотя бы одно из двух
		// полей: они описывают одно и то же свойство с разных сторон.
		PositionTouched: req.PositionId.Set || req.PositionCustom.Set,
		AboutTouched:    req.AboutHtml.Set,
		TagsTouched:     req.TagIds != nil || req.CustomTags != nil,
	}

	if v, ok := req.FullName.Get(); ok {
		in.FullName = &v
	}
	if id, ok := req.PositionId.Get(); ok {
		parsed := uuid.UUID(id)
		in.PositionID = &parsed
	}
	if v, ok := req.PositionCustom.Get(); ok {
		in.PositionCustom = &v
	}
	if v, ok := req.AboutHtml.Get(); ok {
		in.AboutHTML = &v
	}
	for _, id := range req.TagIds {
		in.TagIDs = append(in.TagIDs, uuidOf(id))
	}
	in.CustomTags = append(in.CustomTags, req.CustomTags...)

	if c, ok := req.Contacts.Get(); ok {
		in.Contacts = &domain.Contacts{
			Phone:    c.Phone.Or(""),
			WhatsApp: c.Whatsapp.Or(""),
			Telegram: c.Telegram.Or(""),
		}
	}
	if v, ok := req.ShowCompanyContacts.Get(); ok {
		in.ShowCompanyContacts = &v
	}

	p, err := a.profiles.Update(ctx, uuidOf(params.ProfileId), actor.UserID, in)
	switch {
	case errors.Is(err, domain.ErrPositionAmbiguous):
		return &openapi.UpdateProfileUnprocessableEntity{
			Code:      "position_ambiguous",
			Message:   "position is set both by reference and by text",
			RequestId: a.reqID(ctx),
		}, nil
	case errors.Is(err, domain.ErrTooManyTags):
		return &openapi.UpdateProfileUnprocessableEntity{
			Code:      "too_many_tags",
			Message:   "no more than three tags are allowed",
			RequestId: a.reqID(ctx),
		}, nil
	case err != nil:
		return nil, err
	}

	out := a.profile(p)
	return &out, nil
}

// UploadAvatar реализует операцию uploadAvatar.
func (a *API) UploadAvatar(
	ctx context.Context, req *openapi.UploadAvatarReq, params openapi.UploadAvatarParams,
) (openapi.UploadAvatarRes, error) {
	actor, err := a.principal(ctx)
	if err != nil {
		return nil, err
	}
	profileID := uuidOf(params.ProfileId)

	key, err := a.storeImage(ctx, req.File.File, "avatars/"+profileID.String(), a.limits.Avatar)
	switch {
	case errors.Is(err, errTooLarge):
		return &openapi.UploadAvatarRequestEntityTooLarge{
			Code: "file_too_large", Message: "avatar exceeds the size limit", RequestId: a.reqID(ctx),
		}, nil
	case errors.Is(err, errUnsupportedType):
		return &openapi.UploadAvatarUnsupportedMediaType{
			Code: "unsupported_media_type", Message: "only PNG, JPEG and WebP are accepted", RequestId: a.reqID(ctx),
		}, nil
	case err != nil:
		return nil, err
	}

	crop := cropFromUpload(req)

	p, err := a.profiles.SetAvatar(ctx, profileID, actor.UserID, key, crop)
	if err != nil {
		return nil, err
	}

	out := a.avatar(p)
	return out, nil
}

// cropFromUpload собирает кроп из полей формы.
//
// Область задаётся целиком или не задаётся вовсе: частично заполненный кроп
// в базе запрещён CHECK-констрейнтом, и лучше не отправлять его туда вовсе.
func cropFromUpload(req *openapi.UploadAvatarReq) *domain.Crop {
	x, okX := req.CropX.Get()
	y, okY := req.CropY.Get()
	size, okSize := req.CropSize.Get()

	if !okX || !okY || !okSize {
		return nil
	}
	return &domain.Crop{X: x, Y: y, Size: size}
}

// DeleteAvatar реализует операцию deleteAvatar.
func (a *API) DeleteAvatar(ctx context.Context, params openapi.DeleteAvatarParams) (openapi.DeleteAvatarRes, error) {
	actor, err := a.principal(ctx)
	if err != nil {
		return nil, err
	}

	if err := a.profiles.DeleteAvatar(ctx, uuidOf(params.ProfileId), actor.UserID); err != nil {
		return nil, err
	}
	return &openapi.DeleteAvatarNoContent{}, nil
}

// UpdateAvatarCrop реализует операцию updateAvatarCrop.
func (a *API) UpdateAvatarCrop(
	ctx context.Context, req *openapi.Crop, params openapi.UpdateAvatarCropParams,
) (openapi.UpdateAvatarCropRes, error) {
	actor, err := a.principal(ctx)
	if err != nil {
		return nil, err
	}

	p, err := a.profiles.SetCrop(ctx, uuidOf(params.ProfileId), actor.UserID,
		domain.Crop{X: req.X, Y: req.Y, Size: req.Size})
	if err != nil {
		return nil, err
	}

	out := a.avatar(p)
	return out, nil
}

// UpdateProfileSlug реализует операцию updateProfileSlug.
func (a *API) UpdateProfileSlug(
	ctx context.Context, req *openapi.UpdateSlugRequest, params openapi.UpdateProfileSlugParams,
) (openapi.UpdateProfileSlugRes, error) {
	actor, err := a.principal(ctx)
	if err != nil {
		return nil, err
	}

	p, err := a.profiles.UpdateSlug(ctx, uuidOf(params.ProfileId), actor.UserID, string(req.Slug))
	switch {
	case errors.Is(err, domain.ErrSlugTaken):
		return &openapi.UpdateProfileSlugConflict{
			Code: "slug_taken", Message: "this address is already taken", RequestId: a.reqID(ctx),
		}, nil
	case errors.Is(err, domain.ErrSlugReserved):
		return &openapi.UpdateProfileSlugConflict{
			Code: "slug_reserved", Message: "this address is reserved", RequestId: a.reqID(ctx),
		}, nil
	case err != nil:
		return nil, err
	}

	out := a.profile(p)
	return &out, nil
}

// CheckSlugAvailability реализует операцию checkSlugAvailability.
func (a *API) CheckSlugAvailability(
	ctx context.Context, params openapi.CheckSlugAvailabilityParams,
) (openapi.CheckSlugAvailabilityRes, error) {
	if _, err := a.principal(ctx); err != nil {
		return nil, err
	}

	slug := string(params.Slug)

	available, reason, err := a.profiles.SlugAvailability(ctx, slug)
	if err != nil {
		return nil, err
	}

	return &openapi.SlugAvailability{
		Slug:      openapi.Slug(slug),
		Available: available,
		Reason:    optNilString(reason),
	}, nil
}

// UpdateProfileStatus реализует операцию updateProfileStatus.
//
// Две разные операции под одним ресурсом, и роли у них разные: публикация —
// владельца, блокировка и её снятие — только администратора компании.
func (a *API) UpdateProfileStatus(
	ctx context.Context, req *openapi.UpdateProfileStatusRequest, params openapi.UpdateProfileStatusParams,
) (openapi.UpdateProfileStatusRes, error) {
	actor, err := a.principal(ctx)
	if err != nil {
		return nil, err
	}
	profileID := uuidOf(params.ProfileId)

	var p domain.Profile
	switch req.Status {
	case openapi.UpdateProfileStatusRequestStatusPUBLISHED:
		p, err = a.profiles.Publish(ctx, profileID, actor.UserID)
	case openapi.UpdateProfileStatusRequestStatusBLOCKED:
		p, err = a.profiles.Block(ctx, profileID, actor.UserID)
	}

	if errors.Is(err, domain.ErrProfileIncomplete) {
		return &openapi.UpdateProfileStatusUnprocessableEntity{
			Code:      "profile_incomplete",
			Message:   "profile needs a full name and exactly three tags to be published",
			RequestId: a.reqID(ctx),
		}, nil
	}
	if err != nil {
		return nil, err
	}

	out := a.profile(p)
	return &out, nil
}

// GetPublicProfile реализует операцию getPublicProfile.
//
// Заблокированная страница отвечает 200 со status = BLOCKED и profile = null,
// а не 404: ссылку уже разослали, и она обязана открываться заглушкой,
// а не страницей ошибки.
func (a *API) GetPublicProfile(ctx context.Context, params openapi.GetPublicProfileParams) (openapi.GetPublicProfileRes, error) {
	p, err := a.profiles.Public(ctx, string(params.Slug))
	if err != nil {
		return nil, err
	}

	if !p.PublicVisible() {
		return &openapi.PublicProfileResponseHeaders{
			CacheControl: openapi.NewOptString(publicCacheControl),
			ETag:         openapi.NewOptString(profileETag(p)),
			Response: openapi.PublicProfileResponse{
				Status:  openapi.PublicProfileResponseStatusBLOCKED,
				Profile: openapi.OptNilProfilePublic{Set: true, Null: true},
			},
		}, nil
	}

	return &openapi.PublicProfileResponseHeaders{
		CacheControl: openapi.NewOptString(publicCacheControl),
		ETag:         openapi.NewOptString(profileETag(p)),
		Response: openapi.PublicProfileResponse{
			Status: openapi.PublicProfileResponseStatusPUBLISHED,
			// Нарезка ленивая и кэшируется по хэшу: платит первый запрос
			// после смены фотографии или кропа, остальные читают готовое.
			Profile: openapi.NewOptNilProfilePublic(
				a.publicProfile(p, a.avatars.CroppedKey(ctx, p))),
		},
	}, nil
}

// publicCacheControl — короткий кэш плюс проверка по ETag.
//
// Страница может измениться в любой момент, поэтому вечный кэш здесь
// недопустим: адрес стабилен, а содержимое — нет. Минуты хватает, чтобы
// снять пик при массовом переходе по ссылке, а stale-while-revalidate
// прячет задержку обновления от посетителя.
const publicCacheControl = "public, max-age=60, stale-while-revalidate=300"

// profileETag строится из состояния и времени последнего изменения.
//
// Пересчитывать хэш всего ответа незачем: updated_at меняется триггером
// при любой правке профиля, а статус — единственное, что меняется помимо
// содержимого и влияет на вид страницы.
func profileETag(p domain.Profile) string {
	return fmt.Sprintf(`W/"%s-%d"`, p.Status, p.UpdatedAt.UnixNano())
}
