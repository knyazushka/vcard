package http

import (
	"bytes"
	"context"
	"fmt"

	"github.com/knyazushka/vcard/internal/gen/openapi"
	cardsvc "github.com/knyazushka/vcard/internal/service/card"
)

// cardCacheControl — короткий кэш с проверкой по ETag.
//
// Вечный кэш здесь недопустим: адрес карточки построен на слаге, он
// стабилен, а содержимое меняется вместе с профилем. Вечно кэшируемая
// ссылка — это `cardImageUrl` из ответа профиля: в её имени стоит хэш,
// и по этому адресу картинка уже не изменится.
const cardCacheControl = "public, max-age=60, stale-while-revalidate=300"

// GetProfileCardPng реализует операцию getProfileCardPng.
func (a *API) GetProfileCardPng(
	ctx context.Context, params openapi.GetProfileCardPngParams,
) (openapi.GetProfileCardPngRes, error) {
	p, err := a.profiles.Public(ctx, string(params.Slug))
	if err != nil {
		return nil, err
	}

	// Заблокированная страница отдаёт заглушку, но карточку — нет:
	// картинка после блокировки не должна продолжать расходиться
	// по мессенджерам.
	if !p.PublicVisible() {
		return &openapi.Error{
			Code: "not_found", Message: "card is not available", RequestId: a.reqID(ctx),
		}, nil
	}

	res, err := a.cards.Render(ctx, p, cardsvc.FormatPNG)
	if err != nil {
		return nil, err
	}

	out := &openapi.GetProfileCardPngOKHeaders{
		CacheControl: openapi.NewOptString(cardCacheControl),
		ETag:         openapi.NewOptString(res.ETag),
		Response:     openapi.GetProfileCardPngOK{Data: bytes.NewReader(res.Body)},
	}

	if params.Download.Or(false) {
		out.ContentDisposition = openapi.NewOptString(
			fmt.Sprintf("attachment; filename=%q", p.Slug+".png"))
	}
	return out, nil
}

// GetProfileCardSvg реализует операцию getProfileCardSvg.
//
// Тот же макет в векторе — не второе представление, а исходник первого.
// Фронт вставляет его прямо в DOM для живого предпросмотра и не дублирует
// вёрстку карточки у себя.
func (a *API) GetProfileCardSvg(
	ctx context.Context, params openapi.GetProfileCardSvgParams,
) (openapi.GetProfileCardSvgRes, error) {
	p, err := a.profiles.Public(ctx, string(params.Slug))
	if err != nil {
		return nil, err
	}
	if !p.PublicVisible() {
		return &openapi.Error{
			Code: "not_found", Message: "card is not available", RequestId: a.reqID(ctx),
		}, nil
	}

	res, err := a.cards.Render(ctx, p, cardsvc.FormatSVG)
	if err != nil {
		return nil, err
	}

	return &openapi.GetProfileCardSvgOKHeaders{
		CacheControl: openapi.NewOptString(cardCacheControl),
		ETag:         openapi.NewOptString(res.ETag),
		Response:     openapi.GetProfileCardSvgOK{Data: bytes.NewReader(res.Body)},
	}, nil
}

// GetOwnProfileCard реализует операцию getOwnProfileCard.
//
// Работает и для черновика: человек должен увидеть, что скачает, до того
// как опубликует страницу. Публичный маршрут для DRAFT отдаёт 404, поэтому
// без этой операции предпросмотр был бы невозможен.
func (a *API) GetOwnProfileCard(
	ctx context.Context, params openapi.GetOwnProfileCardParams,
) (openapi.GetOwnProfileCardRes, error) {
	actor, err := a.principal(ctx)
	if err != nil {
		return nil, err
	}

	p, err := a.profiles.Get(ctx, uuidOf(params.ProfileId), actor.UserID)
	if err != nil {
		return nil, err
	}

	res, err := a.cards.Render(ctx, p, cardsvc.FormatPNG)
	if err != nil {
		return nil, err
	}

	return &openapi.GetOwnProfileCardOK{Data: bytes.NewReader(res.Body)}, nil
}
