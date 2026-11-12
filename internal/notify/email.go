package notify

import (
	"context"
	"errors"
	"strconv"

	mail "github.com/wneessen/go-mail"
)

// Email sends through smtp_host. Empty smtp_host skips (old code fell back to PHP mail()).
func (n *Notifier) Email(ctx context.Context, to, subject, body string) error {
	if n.get("smtp_host") == "" || to == "" || body == "" {
		return nil
	}
	c, m, err := n.mailer(to, subject, body)
	if err == nil {
		err = c.DialAndSendWithContext(ctx, m)
	}
	return n.logged("email", to, subject, body, err)
}

func (n *Notifier) mailer(to, subject, body string) (*mail.Client, *mail.Msg, error) {
	port, err := strconv.Atoi(n.get("smtp_port"))
	if err != nil || port <= 0 {
		return nil, nil, errors.New("smtp_port invalid")
	}
	if n.get("mail_from") == "" {
		return nil, nil, errors.New("mail_from required")
	}
	opts := []mail.Option{mail.WithPort(port), mail.WithTLSPolicy(mail.TLSOpportunistic)}
	switch n.get("smtp_ssltls") {
	case "ssl":
		opts = append(opts, mail.WithSSL())
	case "tls":
		opts = append(opts, mail.WithTLSPolicy(mail.TLSMandatory))
	}
	if n.get("smtp_user") != "" {
		opts = append(opts, mail.WithSMTPAuth(mail.SMTPAuthAutoDiscover), mail.WithUsername(n.get("smtp_user")), mail.WithPassword(n.get("smtp_pass")))
	}
	c, err := mail.NewClient(n.get("smtp_host"), opts...)
	if err != nil {
		return nil, nil, err
	}
	m := mail.NewMsg()
	if err := m.From(n.get("mail_from")); err != nil {
		return nil, nil, err
	}
	if err := m.To(to); err != nil {
		return nil, nil, err
	}
	if r := n.get("mail_reply_to"); r != "" {
		_ = m.ReplyTo(r)
	}
	m.Subject(subject)
	m.SetBodyString(mail.TypeTextPlain, body)
	return c, m, nil
}

// EmailSubject is the subject of a customer email: the company name in brackets, then subject.
func (n *Notifier) EmailSubject(subject string) string {
	return "[" + n.get("company_name") + "] " + subject
}
