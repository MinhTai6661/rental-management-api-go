package mail

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"strings"
	"time"
)

// SMTP delivers messages through an SMTP server (Mailpit in development).
type SMTP struct {
	// Addr is host:port of the server.
	Addr string
	// From is the sender, e.g. "Rental Management <no-reply@rental.test>".
	From string
	// Username and Password enable authentication when the server offers it; leave them empty for none.
	Username, Password string
	// Now stamps the Date header; time.Now when nil.
	Now func() time.Time
}

// Send delivers msg. The context bounds the connection and the whole conversation with the server.
func (s *SMTP) Send(ctx context.Context, msg Message) error {
	from, err := mail.ParseAddress(s.From)
	if err != nil {
		return fmt.Errorf("parse sender: %w", err)
	}
	to, err := mail.ParseAddress(msg.To)
	if err != nil {
		return fmt.Errorf("parse recipient: %w", err)
	}
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	body, err := buildMIME(from, to, msg, now())
	if err != nil {
		return err
	}

	host, _, err := net.SplitHostPort(s.Addr)
	if err != nil {
		return fmt.Errorf("parse SMTP address: %w", err)
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", s.Addr)
	if err != nil {
		return fmt.Errorf("connect to SMTP server: %w", err)
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	client, err := smtp.NewClient(conn, host)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("start SMTP session: %w", err)
	}
	defer func() { _ = client.Close() }()

	if ok, _ := client.Extension("STARTTLS"); ok {
		if err := client.StartTLS(&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}); err != nil {
			return fmt.Errorf("start TLS: %w", err)
		}
	}
	if s.Username != "" {
		if err := client.Auth(smtp.PlainAuth("", s.Username, s.Password, host)); err != nil {
			return fmt.Errorf("authenticate: %w", err)
		}
	}
	if err := client.Mail(from.Address); err != nil {
		return fmt.Errorf("MAIL FROM: %w", err)
	}
	if err := client.Rcpt(to.Address); err != nil {
		return fmt.Errorf("RCPT TO: %w", err)
	}
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("DATA: %w", err)
	}
	if _, err := w.Write(body); err != nil {
		return fmt.Errorf("write message: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("finish message: %w", err)
	}
	return client.Quit()
}

// buildMIME renders a multipart/alternative message: the plain text first, the HTML last (the preferred part).
// Header values are built from parsed addresses and an encoded subject, so none of them can carry a line break.
func buildMIME(from, to *mail.Address, msg Message, date time.Time) ([]byte, error) {
	if strings.ContainsAny(msg.Subject, "\r\n") {
		return nil, errors.New("subject must be a single line")
	}
	boundary, err := newBoundary()
	if err != nil {
		return nil, err
	}

	var b bytes.Buffer
	header := func(name, value string) { fmt.Fprintf(&b, "%s: %s\r\n", name, value) }
	header("From", from.String())
	header("To", to.String())
	header("Subject", mime.QEncoding.Encode("utf-8", msg.Subject))
	header("Date", date.Format(time.RFC1123Z))
	header("MIME-Version", "1.0")
	header("Content-Type", fmt.Sprintf("multipart/alternative; boundary=%q", boundary))
	b.WriteString("\r\n")

	part := func(contentType, content string) error {
		fmt.Fprintf(&b, "--%s\r\nContent-Type: %s; charset=utf-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n", boundary, contentType)
		w := quotedprintable.NewWriter(&b)
		if _, err := w.Write([]byte(content)); err != nil {
			return err
		}
		if err := w.Close(); err != nil {
			return err
		}
		b.WriteString("\r\n")
		return nil
	}
	if err := part("text/plain", msg.Text); err != nil {
		return nil, err
	}
	if err := part("text/html", msg.HTML); err != nil {
		return nil, err
	}
	fmt.Fprintf(&b, "--%s--\r\n", boundary)
	return b.Bytes(), nil
}

func newBoundary() (string, error) {
	raw := make([]byte, 12)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("make MIME boundary: %w", err)
	}
	return "rental-management-" + hex.EncodeToString(raw), nil
}
