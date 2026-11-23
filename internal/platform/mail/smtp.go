package mail

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	gomail "github.com/wneessen/go-mail"
)

// dialTimeout bounds opening the connection to the SMTP server; the request context bounds the whole conversation.
const dialTimeout = 30 * time.Second

// SMTP delivers messages through an SMTP server (Mailpit in development) using go-mail.
type SMTP struct {
	// Addr is host:port of the server.
	Addr string
	// From is the sender, e.g. "Rental Management <no-reply@rental.test>".
	From string
	// Username and Password enable authentication (the strongest mechanism the server offers); leave them empty for none.
	Username, Password string
	// Now stamps the Date header; the time of sending when nil.
	Now func() time.Time
}

// Send delivers msg as multipart/alternative: the plain text first, the HTML last (the preferred part). The
// connection uses STARTTLS when the server offers it. The context bounds the connection and the whole conversation.
func (s *SMTP) Send(ctx context.Context, msg Message) error {
	// go-mail encodes headers, but a subject with a line break is a mistake worth refusing outright.
	if strings.ContainsAny(msg.Subject, "\r\n") {
		return errors.New("subject must be a single line")
	}
	host, portText, err := net.SplitHostPort(s.Addr)
	if err != nil {
		return fmt.Errorf("parse SMTP address: %w", err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		return fmt.Errorf("parse SMTP port: %w", err)
	}

	m := gomail.NewMsg()
	if err := m.From(s.From); err != nil {
		return fmt.Errorf("parse sender: %w", err)
	}
	if err := m.To(msg.To); err != nil {
		return fmt.Errorf("parse recipient: %w", err)
	}
	m.Subject(msg.Subject)
	m.SetBodyString(gomail.TypeTextPlain, msg.Text)
	m.AddAlternativeString(gomail.TypeTextHTML, msg.HTML)
	if s.Now != nil {
		m.SetDateWithValue(s.Now())
	}

	opts := []gomail.Option{
		gomail.WithPort(port),
		gomail.WithTimeout(dialTimeout),
		gomail.WithTLSPolicy(gomail.TLSOpportunistic),
		gomail.WithTLSConfig(&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}),
	}
	if s.Username != "" {
		opts = append(opts, gomail.WithSMTPAuth(gomail.SMTPAuthAutoDiscover), gomail.WithUsername(s.Username), gomail.WithPassword(s.Password))
	}
	client, err := gomail.NewClient(host, opts...)
	if err != nil {
		return fmt.Errorf("create SMTP client: %w", err)
	}
	if err := client.DialAndSendWithContext(ctx, m); err != nil {
		return fmt.Errorf("send email: %w", err)
	}
	return nil
}
