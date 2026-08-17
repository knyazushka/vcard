package email

import (
	"encoding/base64"
	"strings"
	"testing"
)

func testData() InvitationData {
	return InvitationData{
		CompanyName:  "Acme GmbH",
		InviterEmail: "boss@acme.com",
		AcceptURL:    "http://localhost:3000/invite?token=abc123",
		ExpiresAt:    "23.08.2026 15:47 MSK",
	}
}

func TestInvitationRendersBothParts(t *testing.T) {
	r, err := NewRenderer()
	if err != nil {
		t.Fatalf("new renderer: %v", err)
	}

	msg, err := r.Invitation("laura@acme.com", testData())
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	// Обе части обязательны: текстовую показывают клиенты с отключённым HTML,
	// и без неё письмо чаще уезжает в спам.
	for name, part := range map[string]string{"text": msg.Text, "html": msg.HTML} {
		if part == "" {
			t.Errorf("%s-часть пустая", name)
		}
		if !strings.Contains(part, testData().AcceptURL) {
			t.Errorf("%s-часть не содержит ссылку", name)
		}
		if !strings.Contains(part, "Acme GmbH") {
			t.Errorf("%s-часть не содержит название компании", name)
		}
	}
}

// html/template обязан экранировать подставляемые значения: название компании
// приходит от пользователя, и без экранирования администратор одной компании
// внедрял бы разметку в письма своим сотрудникам.
func TestInvitationEscapesHTML(t *testing.T) {
	r, err := NewRenderer()
	if err != nil {
		t.Fatalf("new renderer: %v", err)
	}

	data := testData()
	data.CompanyName = `<script>alert(1)</script>`

	msg, err := r.Invitation("laura@acme.com", data)
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	if strings.Contains(msg.HTML, "<script>") {
		t.Error("HTML-часть содержит неэкранированный тег")
	}
}

func TestBuildMessageStructure(t *testing.T) {
	raw, err := build("no-reply@vcard.local", Message{
		To:      "laura@acme.com",
		Subject: "Приглашение в компанию «Acme GmbH»",
		Text:    "текст",
		HTML:    "<p>текст</p>",
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	out := string(raw)

	if !strings.Contains(out, "multipart/alternative") {
		t.Error("нет multipart/alternative")
	}
	// Кириллица в теме обязана быть закодирована по RFC 2047: заголовок
	// не может содержать не-ASCII напрямую.
	if strings.Contains(out, "Приглашение") {
		t.Error("тема не закодирована")
	}
	if !strings.Contains(strings.ToUpper(out), "=?UTF-8?B?") {
		t.Error("нет base64-кодировки темы")
	}
	if strings.Contains(out, "текст") {
		t.Error("тело не закодировано в base64")
	}

	// Строки base64 не должны превышать 76 символов: длинные строки
	// нарушают SMTP и режутся почтовыми серверами.
	for _, line := range strings.Split(out, "\r\n") {
		if len(line) > 78 {
			t.Errorf("строка длиной %d превышает предел", len(line))
		}
	}

	if _, err := base64.StdEncoding.DecodeString(extractFirstBody(out)); err != nil {
		t.Errorf("тело не декодируется как base64: %v", err)
	}
}

func TestBuildRejectsEmptyRecipient(t *testing.T) {
	if _, err := build("no-reply@vcard.local", Message{Subject: "x"}); err == nil {
		t.Error("письмо без получателя собралось без ошибки")
	}
}

// extractFirstBody достаёт содержимое первой части.
func extractFirstBody(msg string) string {
	parts := strings.Split(msg, "Content-Transfer-Encoding: base64\r\n\r\n")
	if len(parts) < 2 {
		return ""
	}
	body, _, _ := strings.Cut(parts[1], "\r\n\r\n")
	return strings.ReplaceAll(body, "\r\n", "")
}
