package email

import (
	"bytes"
	"embed"
	"fmt"
	htmltemplate "html/template"
	texttemplate "text/template"
)

// Виды писем. Значение попадает в email_outbox.kind и разбирается воркером.
const (
	KindInvitation = "invitation"
)

//go:embed templates/*.tmpl
var templatesFS embed.FS

// Renderer собирает письма из шаблонов.
//
// Шаблоны вкомпилированы, а не читаются с диска: иначе в образе появляется
// ещё один путь, за наличием которого надо следить, и письмо ломается
// не при сборке, а при отправке.
type Renderer struct {
	text *texttemplate.Template
	html *htmltemplate.Template
}

// NewRenderer разбирает шаблоны. Ошибка здесь — это ошибка сборки,
// поэтому вызывать его нужно на старте, а не при отправке.
func NewRenderer() (*Renderer, error) {
	// text/template для текстовой части и html/template для HTML — не
	// вопрос вкуса: только второй экранирует подставляемые значения.
	// Название компании приходит от пользователя, и в текстовой версии
	// экранирование не нужно, а в HTML — обязательно.
	text, err := texttemplate.ParseFS(templatesFS, "templates/*.txt.tmpl")
	if err != nil {
		return nil, fmt.Errorf("parse text templates: %w", err)
	}
	html, err := htmltemplate.ParseFS(templatesFS, "templates/*.html.tmpl")
	if err != nil {
		return nil, fmt.Errorf("parse html templates: %w", err)
	}
	return &Renderer{text: text, html: html}, nil
}

// InvitationData — что подставляется в письмо о приглашении.
type InvitationData struct {
	CompanyName  string
	InviterEmail string
	AcceptURL    string
	ExpiresAt    string
	IsAdmin      bool
}

// Invitation собирает письмо с приглашением.
func (r *Renderer) Invitation(to string, data InvitationData) (Message, error) {
	var text, html bytes.Buffer

	if err := r.text.ExecuteTemplate(&text, "invitation.txt.tmpl", data); err != nil {
		return Message{}, fmt.Errorf("render text: %w", err)
	}
	if err := r.html.ExecuteTemplate(&html, "invitation.html.tmpl", data); err != nil {
		return Message{}, fmt.Errorf("render html: %w", err)
	}

	return Message{
		To:      to,
		Subject: fmt.Sprintf("Приглашение в компанию «%s»", data.CompanyName),
		Text:    text.String(),
		HTML:    html.String(),
	}, nil
}
