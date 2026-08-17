// Package email отправляет письма и разгребает очередь исходящих.
package email

import (
	"encoding/base64"
	"fmt"
	"mime"
	"net/smtp"
	"strings"
)

// Message — готовое к отправке письмо.
type Message struct {
	To      string
	Subject string
	Text    string
	HTML    string
}

// Sender отправляет письма.
type Sender interface {
	Send(msg Message) error
}

// SMTPSender — отправка через SMTP.
type SMTPSender struct {
	addr string
	from string
	auth smtp.Auth
}

// NewSMTPSender создаёт отправителя. Пустой пользователь означает отправку
// без аутентификации — так работает локальный Mailpit.
func NewSMTPSender(addr, from, user, password string) *SMTPSender {
	var auth smtp.Auth
	if user != "" {
		host, _, _ := strings.Cut(addr, ":")
		auth = smtp.PlainAuth("", user, password, host)
	}
	return &SMTPSender{addr: addr, from: from, auth: auth}
}

// Send отправляет письмо.
func (s *SMTPSender) Send(msg Message) error {
	body, err := build(s.from, msg)
	if err != nil {
		return err
	}
	if err := smtp.SendMail(s.addr, s.auth, s.from, []string{msg.To}, body); err != nil {
		return fmt.Errorf("send mail: %w", err)
	}
	return nil
}

// build собирает multipart/alternative.
//
// Обе части обязательны: текстовую показывают почтовые клиенты с отключённым
// HTML и она же попадает в предпросмотр, а без неё письмо чаще уезжает
// в спам. Тело кодируется base64 — так кириллица и длинные ссылки переживают
// любые ограничения на длину строки в SMTP.
func build(from string, msg Message) ([]byte, error) {
	if msg.To == "" {
		return nil, fmt.Errorf("recipient is empty")
	}

	const boundary = "vcard-boundary-b3f1c2"

	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", from)
	fmt.Fprintf(&b, "To: %s\r\n", msg.To)
	// Заголовок не может содержать не-ASCII напрямую — кодируем по RFC 2047.
	writeHeader(&b, "Subject", mime.BEncoding.Encode("UTF-8", msg.Subject))
	b.WriteString("MIME-Version: 1.0\r\n")
	fmt.Fprintf(&b, "Content-Type: multipart/alternative; boundary=%q\r\n\r\n", boundary)

	writePart(&b, boundary, "text/plain", msg.Text)
	writePart(&b, boundary, "text/html", msg.HTML)

	fmt.Fprintf(&b, "--%s--\r\n", boundary)
	return []byte(b.String()), nil
}

// headerLineLimit — рекомендованный RFC 5322 предел длины строки.
// Жёсткий предел выше (998), но часть почтовых шлюзов режет по мягкому.
const headerLineLimit = 78

// writeHeader пишет заголовок, сворачивая его по строкам.
//
// Кодировщик разбивает кириллическую тему на несколько encoded-word,
// но склеивает их пробелом — выходит одна строка за сотню символов.
// Пробел между encoded-word при разборе игнорируется, поэтому его
// безопасно заменить на перенос с отступом. Если и первый фрагмент
// вместе с именем заголовка не помещается, перенос ставится сразу
// после двоеточия — это тоже допустимое сворачивание.
func writeHeader(b *strings.Builder, name, value string) {
	folded := strings.ReplaceAll(value, "?= =?", "?=\r\n =?")

	first, _, _ := strings.Cut(folded, "\r\n")
	if len(name)+2+len(first) > headerLineLimit {
		fmt.Fprintf(b, "%s:\r\n %s\r\n", name, folded)
		return
	}
	fmt.Fprintf(b, "%s: %s\r\n", name, folded)
}

func writePart(b *strings.Builder, boundary, contentType, content string) {
	fmt.Fprintf(b, "--%s\r\n", boundary)
	fmt.Fprintf(b, "Content-Type: %s; charset=UTF-8\r\n", contentType)
	b.WriteString("Content-Transfer-Encoding: base64\r\n\r\n")

	encoded := base64.StdEncoding.EncodeToString([]byte(content))
	for len(encoded) > 76 {
		b.WriteString(encoded[:76])
		b.WriteString("\r\n")
		encoded = encoded[76:]
	}
	b.WriteString(encoded)
	b.WriteString("\r\n\r\n")
}
