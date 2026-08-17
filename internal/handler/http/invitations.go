package http

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"

	"github.com/knyazushka/vcard/internal/domain"
	"github.com/knyazushka/vcard/internal/gen/openapi"
	invitesvc "github.com/knyazushka/vcard/internal/service/invitation"
)

// CreateInvitation реализует операцию createInvitation.
func (a *API) CreateInvitation(
	ctx context.Context, req *openapi.CreateInvitationRequest, params openapi.CreateInvitationParams,
) (openapi.CreateInvitationRes, error) {
	actor, err := a.principal(ctx)
	if err != nil {
		return nil, err
	}

	role := domain.RoleEmployee
	if v, ok := req.Role.Get(); ok {
		role = domain.Role(v)
	}

	inv, err := a.invitations.Create(ctx, uuidOf(params.CompanyId), actor.UserID, string(req.Email), role)
	switch {
	case errors.Is(err, domain.ErrAlreadyMember):
		return &openapi.CreateInvitationConflict{
			Code: "already_member", Message: "user is already a member of this company", RequestId: a.reqID(ctx),
		}, nil
	case errors.Is(err, domain.ErrConflict):
		return &openapi.CreateInvitationConflict{
			Code: "invitation_pending", Message: "a pending invitation for this email already exists", RequestId: a.reqID(ctx),
		}, nil
	case err != nil:
		return nil, err
	}

	out := invitationOut(inv, a.now())
	return &out, nil
}

// ListInvitations реализует операцию listInvitations.
func (a *API) ListInvitations(ctx context.Context, params openapi.ListInvitationsParams) (openapi.ListInvitationsRes, error) {
	actor, err := a.principal(ctx)
	if err != nil {
		return nil, err
	}

	page := domain.Page{Limit: params.Limit.Or(20), Offset: params.Offset.Or(0)}

	var status string
	if v, ok := params.Status.Get(); ok {
		status = string(v)
	}

	items, total, err := a.invitations.List(ctx, uuidOf(params.CompanyId), actor.UserID, status, page)
	if err != nil {
		return nil, err
	}

	now := a.now()
	out := openapi.InvitationList{
		Items: make([]openapi.Invitation, 0, len(items)),
		Page:  pageInfo(total, page),
	}
	for _, inv := range items {
		out.Items = append(out.Items, invitationOut(inv, now))
	}
	return &out, nil
}

// RevokeInvitation реализует операцию revokeInvitation.
func (a *API) RevokeInvitation(ctx context.Context, params openapi.RevokeInvitationParams) (openapi.RevokeInvitationRes, error) {
	actor, err := a.principal(ctx)
	if err != nil {
		return nil, err
	}

	err = a.invitations.Revoke(ctx, uuidOf(params.CompanyId), actor.UserID, uuidOf(params.InvitationId))
	if err != nil {
		return nil, err
	}
	return &openapi.RevokeInvitationNoContent{}, nil
}

// ResendInvitation реализует операцию resendInvitation.
func (a *API) ResendInvitation(ctx context.Context, params openapi.ResendInvitationParams) (openapi.ResendInvitationRes, error) {
	actor, err := a.principal(ctx)
	if err != nil {
		return nil, err
	}

	inv, err := a.invitations.Resend(ctx, uuidOf(params.CompanyId), actor.UserID, uuidOf(params.InvitationId))
	if errors.Is(err, domain.ErrConflict) {
		return &openapi.ResendInvitationConflict{
			Code: "invitation_closed", Message: "invitation is already accepted or revoked", RequestId: a.reqID(ctx),
		}, nil
	}
	if err != nil {
		return nil, err
	}

	out := invitationOut(inv, a.now())
	return &out, nil
}

// GetInvitationPreview реализует операцию getInvitationPreview.
func (a *API) GetInvitationPreview(ctx context.Context, params openapi.GetInvitationPreviewParams) (openapi.GetInvitationPreviewRes, error) {
	preview, err := a.invitations.Preview(ctx, params.Token)
	if errors.Is(err, domain.ErrInvitationNotFound) {
		body := a.invitationGone(ctx)
		return &body, nil
	}
	if err != nil {
		return nil, err
	}

	inv := preview.Invitation
	return &openapi.InvitationPreview{
		CompanyName:          inv.CompanyName,
		CompanyLogoUrl:       optURL(a.files.URL(inv.CompanyLogoKey)),
		Email:                openapi.Email(inv.Email),
		Role:                 openapi.Role(inv.Role),
		ExpiresAt:            openapi.Timestamp(inv.ExpiresAt),
		RequiresRegistration: preview.RequiresRegistration,
	}, nil
}

// AcceptInvitation реализует операцию acceptInvitation.
//
// Аутентификация здесь необязательна: по ссылке приходит и незнакомый
// системе человек, и уже вошедший пользователь. Ветка выбирается сервисом.
func (a *API) AcceptInvitation(
	ctx context.Context, req openapi.OptAcceptInvitationRequest, params openapi.AcceptInvitationParams,
) (openapi.AcceptInvitationRes, error) {
	in := invitesvc.AcceptInput{Token: params.Token}

	if body, ok := req.Get(); ok {
		if pwd, ok := body.Password.Get(); ok {
			in.Password = string(pwd)
		}
	}
	if p, ok := PrincipalFromContext(ctx); ok {
		in.ActorID = p.UserID
	}

	result, err := a.invitations.Accept(ctx, in)
	switch {
	case errors.Is(err, domain.ErrInvitationNotFound):
		body := a.invitationGone(ctx)
		return (*openapi.AcceptInvitationNotFound)(&body), nil
	case errors.Is(err, domain.ErrPasswordRequired):
		return &openapi.AcceptInvitationBadRequest{
			Code: "password_required", Message: "password is required to register", RequestId: a.reqID(ctx),
		}, nil
	case errors.Is(err, domain.ErrAuthRequired):
		return &openapi.AcceptInvitationUnauthorized{
			Code: "unauthenticated", Message: "sign in with the invited account to accept", RequestId: a.reqID(ctx),
		}, nil
	case errors.Is(err, domain.ErrEmailMismatch):
		return &openapi.AcceptInvitationForbidden{
			Code: "email_mismatch", Message: "invitation was issued for another email", RequestId: a.reqID(ctx),
		}, nil
	case err != nil:
		return nil, err
	}

	out := openapi.AcceptInvitationResponse{
		CompanyId:   openapi.UUID(result.CompanyID),
		Role:        openapi.Role(result.Role),
		ProfileId:   openapi.UUID(result.ProfileID),
		ProfileSlug: openapi.NewOptSlug(openapi.Slug(result.ProfileSlug)),
	}

	resp := &openapi.AcceptInvitationResponseHeaders{Response: out}

	// Токены выдаются только в ветке регистрации: вошедший пользователь
	// свою сессию уже имеет, и подменять её незачем.
	if result.Registered {
		tokens, err := a.auth.OpenSession(ctx, result.UserID, clientInfo(ctx))
		if err != nil {
			return nil, err
		}
		out.AccessToken = openapi.NewOptNilAccessToken(accessToken(tokens))
		resp.Response = out
		resp.SetCookie = openapi.NewOptString(a.refreshCookie(tokens.Refresh, tokens.RefreshTTL))
	}

	return resp, nil
}

// invitationGone отвечает одинаково на неизвестный, отозванный, принятый
// и протухший токен: различать их — значит рассказывать предъявителю чужой
// ссылки, существовала ли она вообще.
func (a *API) invitationGone(ctx context.Context) openapi.Error {
	return openapi.Error{
		Code:      "invitation_not_found",
		Message:   "invitation is unknown, revoked, already used or expired",
		RequestId: a.reqID(ctx),
	}
}

func contentHash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:16])
}

func bytesReader(data []byte) io.Reader { return bytes.NewReader(data) }
