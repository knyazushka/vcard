package main

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/knyazushka/vcard/internal/gen/openapi"
	apihttp "github.com/knyazushka/vcard/internal/handler/http"
	"github.com/knyazushka/vcard/internal/handler/middleware"
	"github.com/knyazushka/vcard/internal/service/auth"
)

func newTestServer(t *testing.T) *openapi.Server {
	t.Helper()

	tokens := auth.NewTokenIssuer("test-secret-0123456789abcdef0123456789", time.Minute, time.Hour)
	srv, err := openapi.NewServer(
		openapi.UnimplementedHandler{},
		apihttp.NewSecurity(tokens),
		openapi.WithPathPrefix("/api/v1"),
	)
	if err != nil {
		t.Fatal(err)
	}
	return srv
}

// Имена строгих операций — строки, и опечатка в одной из них молча
// перевела бы вход или приглашения на общий лимит. Поэтому каждое имя
// проверяется настоящим путём через роутер ogen.
func TestOperationResolver(t *testing.T) {
	resolve := operationResolver(newTestServer(t))

	cases := []struct {
		method, path, want string
	}{
		{http.MethodPost, "/api/v1/auth/login", "login"},
		{http.MethodPost, "/api/v1/auth/register", "register"},
		{http.MethodGet, "/api/v1/invitations/tok3n", "getInvitationPreview"},
		{http.MethodPost, "/api/v1/invitations/tok3n/accept", "acceptInvitation"},
		{http.MethodPost, "/api/v1/companies/0b7f4a52-5d0e-4c1a-9a51-6f1d2b0c9e11/invitations", "createInvitation"},
		{http.MethodPost, "/api/v1/companies/0b7f4a52-5d0e-4c1a-9a51-6f1d2b0c9e11/invitations/5c3e9b1a-0f7d-4e2b-8a6c-1d9e0f2a3b4c/resend", "resendInvitation"},
		{http.MethodGet, "/api/v1/public/employee/laura", "getPublicProfile"},
		{http.MethodGet, "/files/avatars/ab.png", middleware.OperationFiles},
		{http.MethodGet, "/wp-admin/", middleware.OperationUnknown},
		{http.MethodGet, "/auth/login", middleware.OperationUnknown}, // без префикса
	}

	resolved := make([]string, 0, len(cases))
	for _, c := range cases {
		r := httptest.NewRequestWithContext(t.Context(), c.method, c.path, nil)
		got := resolve(r)
		if got != c.want {
			t.Errorf("%s %s: получили %q, ждали %q", c.method, c.path, got, c.want)
		}
		resolved = append(resolved, got)
	}

	for _, op := range strictOperations {
		if !slices.Contains(resolved, op) {
			t.Errorf("строгая операция %q не покрыта ни одним путём в тесте", op)
		}
	}

	preflight := httptest.NewRequestWithContext(t.Context(), http.MethodOptions, "/api/v1/auth/login", nil)
	preflight.Header.Set("Access-Control-Request-Method", http.MethodPost)
	if got := resolve(preflight); got != middleware.OperationPreflight {
		t.Errorf("preflight: получили %q", got)
	}
}
