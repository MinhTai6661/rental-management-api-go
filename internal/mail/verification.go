package mail

import (
	"bytes"
	"embed"
	"fmt"
	htmltemplate "html/template"
	"strings"
	"text/template"
	"time"
)

//go:embed templates/*
var templateFS embed.FS

// VerifyEmail is the data of the "verify your email address" message.
type VerifyEmail struct {
	To       string
	FullName string
	// URL is the link the person opens to confirm the address.
	URL string
	// ExpiresIn is how long the link works; shown in whole hours.
	ExpiresIn time.Duration
}

// verifySubject is the subject line of the verification message in the language.
func verifySubject(lang string) string {
	if lang == "en" {
		return "Confirm your email address"
	}
	return "Xác nhận địa chỉ email của bạn"
}

// Renderer builds messages from the embedded templates.
type Renderer struct {
	text *template.Template
	html *htmltemplate.Template
}

// NewRenderer parses the embedded templates once.
func NewRenderer() (*Renderer, error) {
	text, err := template.ParseFS(templateFS, "templates/*.txt")
	if err != nil {
		return nil, fmt.Errorf("parse text templates: %w", err)
	}
	html, err := htmltemplate.ParseFS(templateFS, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("parse html templates: %w", err)
	}
	return &Renderer{text: text, html: html}, nil
}

// VerifyEmail renders the verification message in the language ("vi" or "en"; anything else gives Vietnamese).
func (r *Renderer) VerifyEmail(lang string, data VerifyEmail) (Message, error) {
	if lang != "en" {
		lang = "vi"
	}
	view := struct {
		FullName string
		URL      string
		Hours    int
	}{data.FullName, data.URL, int(data.ExpiresIn.Hours())}

	name := "verify_email." + lang
	var text, html bytes.Buffer
	if err := r.text.ExecuteTemplate(&text, name+".txt", view); err != nil {
		return Message{}, fmt.Errorf("render %s text: %w", name, err)
	}
	if err := r.html.ExecuteTemplate(&html, name+".html", view); err != nil {
		return Message{}, fmt.Errorf("render %s html: %w", name, err)
	}
	return Message{
		To:      data.To,
		Subject: verifySubject(lang),
		Text:    strings.TrimLeft(text.String(), "\n"),
		HTML:    html.String(),
	}, nil
}
