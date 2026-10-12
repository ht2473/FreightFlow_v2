// Package mail отправляет письма через SMTP.
package mail

import (
	"context"
	"fmt"

	gomail "github.com/wneessen/go-mail"
)

// Message — письмо. HTML необязателен; текстовая версия обязательна.
type Message struct {
	To      string
	Subject string
	Text    string
	HTML    string
}

// Config — параметры SMTP-сервера.
type Config struct {
	Host     string
	Port     int
	User     string
	Password string
	From     string
}

// Sender отправляет письма.
type Sender struct {
	cfg Config
}

// NewSender создаёт отправителя.
func NewSender(cfg Config) *Sender {
	return &Sender{cfg: cfg}
}

// Send отправляет письмо. Без учётных данных используется соединение без авторизации и TLS
// (Mailpit в разработке); с учётными данными — обязательный STARTTLS.
func (s *Sender) Send(ctx context.Context, m Message) error {
	msg := gomail.NewMsg()
	if err := msg.From(s.cfg.From); err != nil {
		return fmt.Errorf("set from: %w", err)
	}
	if err := msg.To(m.To); err != nil {
		return fmt.Errorf("set to: %w", err)
	}
	msg.Subject(m.Subject)
	msg.SetBodyString(gomail.TypeTextPlain, m.Text)
	if m.HTML != "" {
		msg.AddAlternativeString(gomail.TypeTextHTML, m.HTML)
	}

	opts := []gomail.Option{gomail.WithPort(s.cfg.Port)}
	if s.cfg.User != "" {
		opts = append(opts,
			gomail.WithSMTPAuth(gomail.SMTPAuthAutoDiscover),
			gomail.WithUsername(s.cfg.User),
			gomail.WithPassword(s.cfg.Password),
			gomail.WithTLSPolicy(gomail.TLSMandatory))
	} else {
		opts = append(opts, gomail.WithTLSPolicy(gomail.NoTLS))
	}
	client, err := gomail.NewClient(s.cfg.Host, opts...)
	if err != nil {
		return fmt.Errorf("create smtp client: %w", err)
	}
	if err := client.DialAndSendWithContext(ctx, msg); err != nil {
		return fmt.Errorf("send mail: %w", err)
	}
	return nil
}
