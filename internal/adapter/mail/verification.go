// Package mailadapter implements the identity mail port on top of pkg/mail: it builds the link, renders the
// message from the embedded template and hands it to the mailer.
package mailadapter

import (
	"context"
	"net/url"
	"strings"
	"time"

	"rental-management/pkg/mail"
)

// VerificationMailer sends the "verify your email address" message.
type VerificationMailer struct {
	mailer   mail.Mailer
	renderer *mail.Renderer
	webURL   string
}

// NewVerificationMailer returns a mailer whose links point at webURL, the origin of the web app.
func NewVerificationMailer(mailer mail.Mailer, renderer *mail.Renderer, webURL string) *VerificationMailer {
	return &VerificationMailer{mailer: mailer, renderer: renderer, webURL: webURL}
}

// SendVerification emails the link that carries the token, in the language ("vi" or "en"). The token only goes
// into the message: it is never logged.
func (m *VerificationMailer) SendVerification(ctx context.Context, to, fullName, token, lang string, expiresIn time.Duration) error {
	link := strings.TrimRight(m.webURL, "/") + "/verify-email?token=" + url.QueryEscape(token)
	msg, err := m.renderer.VerifyEmail(lang, mail.VerifyEmail{To: to, FullName: fullName, URL: link, ExpiresIn: expiresIn})
	if err != nil {
		return err
	}
	return m.mailer.Send(ctx, msg)
}
