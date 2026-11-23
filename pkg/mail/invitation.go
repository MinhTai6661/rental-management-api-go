package mail

import (
	"bytes"
	"fmt"
	"strings"
	"time"
)

// ContractInvitation is the data of the "you are offered a room" message.
type ContractInvitation struct {
	To       string
	Landlord string
	Room     string
	// URL is the link the person opens to see the offer and answer it.
	URL string
	// OptOutURL is the link that stops further invitation emails; empty leaves the line out.
	OptOutURL string
	// ExpiresAt is when the offer stops being open.
	ExpiresAt time.Time
}

// contractInvitationSubject is the subject line of the invitation message in the language.
func contractInvitationSubject(lang, room string) string {
	if lang == "en" {
		return "You are offered the room " + room
	}
	return "Lời mời thuê phòng " + room
}

// ContractInvitation renders the invitation message in the language ("vi" or "en"; anything else gives Vietnamese).
func (r *Renderer) ContractInvitation(lang string, data ContractInvitation) (Message, error) {
	if lang != "en" {
		lang = "vi"
	}
	layout := "02/01/2006"
	if lang == "en" {
		layout = "2 January 2006"
	}
	view := struct {
		Landlord  string
		Room      string
		URL       string
		OptOutURL string
		Expires   string
	}{data.Landlord, data.Room, data.URL, data.OptOutURL, data.ExpiresAt.Format(layout)}

	name := "contract_invitation." + lang
	var text, html bytes.Buffer
	if err := r.text.ExecuteTemplate(&text, name+".txt", view); err != nil {
		return Message{}, fmt.Errorf("render %s text: %w", name, err)
	}
	if err := r.html.ExecuteTemplate(&html, name+".html", view); err != nil {
		return Message{}, fmt.Errorf("render %s html: %w", name, err)
	}
	return Message{
		To:      data.To,
		Subject: contractInvitationSubject(lang, data.Room),
		Text:    strings.TrimLeft(text.String(), "\n"),
		HTML:    html.String(),
	}, nil
}
