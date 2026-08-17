package domain

import (
	"testing"
	"time"
)

// Истечение вычисляется, а не хранится: в базе протухшее приглашение
// остаётся PENDING, и админка обязана показывать не это значение,
// а фактическое состояние.
func TestInvitationEffectiveStatus(t *testing.T) {
	now := time.Now()

	cases := map[string]struct {
		inv  Invitation
		want InvitationStatus
	}{
		"действующее": {
			Invitation{Status: InvitationPending, ExpiresAt: now.Add(time.Hour)},
			InvitationPending,
		},
		"протухшее": {
			Invitation{Status: InvitationPending, ExpiresAt: now.Add(-time.Hour)},
			InvitationExpired,
		},
		// Срок принятого и отозванного значения не имеет: они уже закрыты,
		// и подменять их статус на EXPIRED значило бы терять историю.
		"принятое после срока": {
			Invitation{Status: InvitationAccepted, ExpiresAt: now.Add(-time.Hour)},
			InvitationAccepted,
		},
		"отозванное после срока": {
			Invitation{Status: InvitationRevoked, ExpiresAt: now.Add(-time.Hour)},
			InvitationRevoked,
		},
	}

	for name, tc := range cases {
		if got := tc.inv.EffectiveStatus(now); got != tc.want {
			t.Errorf("%s: получили %s, ждали %s", name, got, tc.want)
		}
		if usable := tc.inv.Usable(now); usable != (tc.want == InvitationPending) {
			t.Errorf("%s: Usable вернул %v", name, usable)
		}
	}
}

func TestNormalizeEmail(t *testing.T) {
	cases := map[string]string{
		"  Laura@Acme.COM ": "laura@acme.com",
		"boss@acme.com":     "boss@acme.com",
		// Точки и «плюс-адресацию» не трогаем: это внутренние правила
		// конкретных провайдеров, и нормализуя их, мы однажды склеим
		// двух разных людей в одного.
		"i.van+work@acme.com": "i.van+work@acme.com",
	}

	for in, want := range cases {
		if got := NormalizeEmail(in); got != want {
			t.Errorf("%q: получили %q, ждали %q", in, got, want)
		}
	}
}
