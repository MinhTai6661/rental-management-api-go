package mailadapter

import (
	"context"
	"time"

	"rental-management/pkg/mail"
)

// InvitationMailer sends the "you are offered a room" message. It takes plain values (adapters never import a use
// case package); the composition root adapts it to the contract port.
type InvitationMailer struct {
	mailer   mail.Mailer
	renderer *mail.Renderer
	optOut   func(email string) string
}

// NewInvitationMailer returns a mailer. optOutURL gives the link of an address that stops further invitation emails
// (empty: the message carries none); it may be nil.
func NewInvitationMailer(mailer mail.Mailer, renderer *mail.Renderer, optOutURL func(email string) string) *InvitationMailer {
	return &InvitationMailer{mailer: mailer, renderer: renderer, optOut: optOutURL}
}

// Send emails the invitation in Vietnamese (no account language is known yet). url is the link the person opens; the
// secret it may carry goes only into the message and is never logged.
func (m *InvitationMailer) Send(ctx context.Context, to, landlord, room, url string, expiresAt time.Time) error {
	data := mail.ContractInvitation{To: to, Landlord: landlord, Room: room, URL: url, ExpiresAt: expiresAt}
	if m.optOut != nil {
		data.OptOutURL = m.optOut(to)
	}
	msg, err := m.renderer.ContractInvitation("vi", data)
	if err != nil {
		return err
	}
	return m.mailer.Send(ctx, msg)
}
