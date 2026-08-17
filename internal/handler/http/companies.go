package http

import (
	"context"
	"errors"
	"io"
	"net/http"

	"github.com/knyazushka/vcard/internal/domain"
	"github.com/knyazushka/vcard/internal/gen/openapi"
)

// CreateCompany реализует операцию createCompany.
func (a *API) CreateCompany(ctx context.Context, req *openapi.CreateCompanyRequest) (openapi.CreateCompanyRes, error) {
	actor, err := a.principal(ctx)
	if err != nil {
		return nil, err
	}

	created, err := a.companies.Create(ctx, actor.UserID, domain.Company{
		Name:    req.Name,
		Address: req.Address.Or(""),
		Phones:  domainPhones(req.Phones),
	})
	if err != nil {
		return nil, err
	}

	out := a.company(created)
	return &out, nil
}

// GetCompany реализует операцию getCompany.
func (a *API) GetCompany(ctx context.Context, params openapi.GetCompanyParams) (openapi.GetCompanyRes, error) {
	actor, err := a.principal(ctx)
	if err != nil {
		return nil, err
	}

	c, err := a.companies.Get(ctx, uuidOf(params.CompanyId), actor.UserID)
	if err != nil {
		return nil, err
	}

	out := a.company(c)
	return &out, nil
}

// UpdateCompany реализует операцию updateCompany.
func (a *API) UpdateCompany(
	ctx context.Context, req *openapi.UpdateCompanyRequest, params openapi.UpdateCompanyParams,
) (openapi.UpdateCompanyRes, error) {
	actor, err := a.principal(ctx)
	if err != nil {
		return nil, err
	}

	// Отсутствующее поле и явный null — разные вещи: первое не трогает
	// значение, второе очищает. Опциональные типы ogen различают их сами,
	// поэтому здесь достаточно превратить «задано» в указатель.
	var name *string
	if v, ok := req.Name.Get(); ok {
		name = &v
	}
	var address *string
	if req.Address.Set {
		v := req.Address.Or("")
		address = &v
	}

	c, err := a.companies.Update(ctx, uuidOf(params.CompanyId), actor.UserID,
		name, address, domainPhones(req.Phones), req.Phones != nil)
	if err != nil {
		return nil, err
	}

	out := a.company(c)
	return &out, nil
}

// UploadCompanyLogo реализует операцию uploadCompanyLogo.
func (a *API) UploadCompanyLogo(
	ctx context.Context, req *openapi.UploadCompanyLogoReq, params openapi.UploadCompanyLogoParams,
) (openapi.UploadCompanyLogoRes, error) {
	actor, err := a.principal(ctx)
	if err != nil {
		return nil, err
	}
	companyID := uuidOf(params.CompanyId)

	key, err := a.storeImage(ctx, req.File.File, "logos/"+companyID.String(), a.limits.Logo)
	switch {
	case errors.Is(err, errTooLarge):
		return &openapi.UploadCompanyLogoRequestEntityTooLarge{
			Code: "file_too_large", Message: "logo exceeds the size limit", RequestId: a.reqID(ctx),
		}, nil
	case errors.Is(err, errUnsupportedType):
		return &openapi.UploadCompanyLogoUnsupportedMediaType{
			Code: "unsupported_media_type", Message: "only PNG, JPEG and WebP are accepted", RequestId: a.reqID(ctx),
		}, nil
	case err != nil:
		return nil, err
	}

	c, err := a.companies.SetLogo(ctx, companyID, actor.UserID, key)
	if err != nil {
		return nil, err
	}

	out := a.company(c)
	return &out, nil
}

// DeleteCompanyLogo реализует операцию deleteCompanyLogo.
func (a *API) DeleteCompanyLogo(ctx context.Context, params openapi.DeleteCompanyLogoParams) (openapi.DeleteCompanyLogoRes, error) {
	actor, err := a.principal(ctx)
	if err != nil {
		return nil, err
	}

	if _, err := a.companies.SetLogo(ctx, uuidOf(params.CompanyId), actor.UserID, ""); err != nil {
		return nil, err
	}
	return &openapi.DeleteCompanyLogoNoContent{}, nil
}

// --- справочники ------------------------------------------------------------

// ListPositions реализует операцию listPositions.
func (a *API) ListPositions(ctx context.Context, params openapi.ListPositionsParams) (openapi.ListPositionsRes, error) {
	actor, err := a.principal(ctx)
	if err != nil {
		return nil, err
	}

	items, err := a.companies.Positions(ctx, uuidOf(params.CompanyId), actor.UserID)
	if err != nil {
		return nil, err
	}

	out := make(openapi.ListPositionsOKApplicationJSON, 0, len(items))
	for _, p := range items {
		out = append(out, position(p))
	}
	return &out, nil
}

// CreatePosition реализует операцию createPosition.
func (a *API) CreatePosition(
	ctx context.Context, req *openapi.PositionRequest, params openapi.CreatePositionParams,
) (openapi.CreatePositionRes, error) {
	actor, err := a.principal(ctx)
	if err != nil {
		return nil, err
	}

	p, err := a.companies.CreatePosition(ctx, uuidOf(params.CompanyId), actor.UserID, req.Title)
	if errors.Is(err, domain.ErrConflict) {
		return &openapi.CreatePositionConflict{
			Code: "already_exists", Message: "position with this title already exists", RequestId: a.reqID(ctx),
		}, nil
	}
	if err != nil {
		return nil, err
	}

	out := position(p)
	return &out, nil
}

// UpdatePosition реализует операцию updatePosition.
func (a *API) UpdatePosition(
	ctx context.Context, req *openapi.PositionRequest, params openapi.UpdatePositionParams,
) (openapi.UpdatePositionRes, error) {
	actor, err := a.principal(ctx)
	if err != nil {
		return nil, err
	}

	p, err := a.companies.UpdatePosition(ctx, uuidOf(params.CompanyId), actor.UserID, uuidOf(params.PositionId), req.Title)
	if errors.Is(err, domain.ErrConflict) {
		return &openapi.UpdatePositionConflict{
			Code: "already_exists", Message: "position with this title already exists", RequestId: a.reqID(ctx),
		}, nil
	}
	if err != nil {
		return nil, err
	}

	out := position(p)
	return &out, nil
}

// DeletePosition реализует операцию deletePosition.
func (a *API) DeletePosition(ctx context.Context, params openapi.DeletePositionParams) (openapi.DeletePositionRes, error) {
	actor, err := a.principal(ctx)
	if err != nil {
		return nil, err
	}

	if err := a.companies.DeletePosition(ctx, uuidOf(params.CompanyId), actor.UserID, uuidOf(params.PositionId)); err != nil {
		return nil, err
	}
	return &openapi.DeletePositionNoContent{}, nil
}

// ListTags реализует операцию listTags.
func (a *API) ListTags(ctx context.Context, params openapi.ListTagsParams) (openapi.ListTagsRes, error) {
	actor, err := a.principal(ctx)
	if err != nil {
		return nil, err
	}

	items, err := a.companies.Tags(ctx, uuidOf(params.CompanyId), actor.UserID)
	if err != nil {
		return nil, err
	}

	out := make(openapi.ListTagsOKApplicationJSON, 0, len(items))
	for _, t := range items {
		out = append(out, tag(t))
	}
	return &out, nil
}

// CreateTag реализует операцию createTag.
func (a *API) CreateTag(ctx context.Context, req *openapi.TagRequest, params openapi.CreateTagParams) (openapi.CreateTagRes, error) {
	actor, err := a.principal(ctx)
	if err != nil {
		return nil, err
	}

	t, err := a.companies.CreateTag(ctx, uuidOf(params.CompanyId), actor.UserID, req.Title)
	if errors.Is(err, domain.ErrConflict) {
		// Нормализованное название уже занято — регистр и лишние пробелы
		// не создают нового тега.
		return &openapi.CreateTagConflict{
			Code: "already_exists", Message: "tag with this title already exists", RequestId: a.reqID(ctx),
		}, nil
	}
	if err != nil {
		return nil, err
	}

	out := tag(t)
	return &out, nil
}

// DeleteTag реализует операцию deleteTag.
func (a *API) DeleteTag(ctx context.Context, params openapi.DeleteTagParams) (openapi.DeleteTagRes, error) {
	actor, err := a.principal(ctx)
	if err != nil {
		return nil, err
	}

	if err := a.companies.DeleteTag(ctx, uuidOf(params.CompanyId), actor.UserID, uuidOf(params.TagId)); err != nil {
		return nil, err
	}
	return &openapi.DeleteTagNoContent{}, nil
}

// --- состав -----------------------------------------------------------------

// ListEmployees реализует операцию listEmployees.
func (a *API) ListEmployees(ctx context.Context, params openapi.ListEmployeesParams) (openapi.ListEmployeesRes, error) {
	actor, err := a.principal(ctx)
	if err != nil {
		return nil, err
	}

	page := domain.Page{Limit: params.Limit.Or(20), Offset: params.Offset.Or(0)}

	items, total, err := a.companies.Employees(ctx, uuidOf(params.CompanyId), actor.UserID, page)
	if err != nil {
		return nil, err
	}

	out := openapi.EmployeeList{
		Items: make([]openapi.Employee, 0, len(items)),
		Page:  pageInfo(total, page),
	}
	for _, e := range items {
		out.Items = append(out.Items, employee(e))
	}
	return &out, nil
}

// UpdateEmployeeRole реализует операцию updateEmployeeRole.
func (a *API) UpdateEmployeeRole(
	ctx context.Context, req *openapi.UpdateEmployeeRequest, params openapi.UpdateEmployeeRoleParams,
) (openapi.UpdateEmployeeRoleRes, error) {
	actor, err := a.principal(ctx)
	if err != nil {
		return nil, err
	}

	e, err := a.companies.SetRole(ctx, uuidOf(params.CompanyId), actor.UserID,
		uuidOf(params.UserId), domain.Role(req.Role))
	if errors.Is(err, domain.ErrLastAdmin) {
		return &openapi.UpdateEmployeeRoleConflict{
			Code:      "last_admin",
			Message:   "company must keep at least one admin",
			RequestId: a.reqID(ctx),
		}, nil
	}
	if err != nil {
		return nil, err
	}

	out := employee(e)
	return &out, nil
}

// RemoveEmployee реализует операцию removeEmployee.
func (a *API) RemoveEmployee(ctx context.Context, params openapi.RemoveEmployeeParams) (openapi.RemoveEmployeeRes, error) {
	actor, err := a.principal(ctx)
	if err != nil {
		return nil, err
	}

	err = a.companies.RemoveEmployee(ctx, uuidOf(params.CompanyId), actor.UserID, uuidOf(params.UserId))
	if errors.Is(err, domain.ErrLastAdmin) {
		return &openapi.RemoveEmployeeConflict{
			Code:      "last_admin",
			Message:   "company must keep at least one admin",
			RequestId: a.reqID(ctx),
		}, nil
	}
	if err != nil {
		return nil, err
	}
	return &openapi.RemoveEmployeeNoContent{}, nil
}

// --- загрузка изображений ----------------------------------------------------

var (
	errTooLarge        = errors.New("file is too large")
	errUnsupportedType = errors.New("unsupported media type")
)

// storeImage проверяет и сохраняет загруженное изображение.
//
// Тип определяется по сигнатуре содержимого, а не по Content-Type
// и не по расширению: и то и другое задаёт клиент. Размер ограничивается
// на чтении, а не по заголовку Content-Length — заголовок тоже клиентский.
func (a *API) storeImage(ctx context.Context, r io.Reader, keyPrefix string, maxBytes int64) (string, error) {
	// Читаем на байт больше предела: если он прочитался, файл слишком велик.
	data, err := io.ReadAll(io.LimitReader(r, maxBytes+1))
	if err != nil {
		return "", err
	}
	if int64(len(data)) > maxBytes {
		return "", errTooLarge
	}

	ext, ok := imageExtension(data)
	if !ok {
		return "", errUnsupportedType
	}

	key := keyPrefix + "/" + contentHash(data) + ext
	if err := a.files.Put(ctx, key, bytesReader(data), http.DetectContentType(data)); err != nil {
		return "", err
	}
	return key, nil
}

// imageExtension распознаёт формат по сигнатуре файла.
func imageExtension(data []byte) (string, bool) {
	switch http.DetectContentType(data) {
	case "image/png":
		return ".png", true
	case "image/jpeg":
		return ".jpg", true
	case "image/webp":
		return ".webp", true
	default:
		return "", false
	}
}

func (a *API) principal(ctx context.Context) (Principal, error) {
	p, ok := PrincipalFromContext(ctx)
	if !ok {
		return Principal{}, errUnauthenticated
	}
	return p, nil
}
