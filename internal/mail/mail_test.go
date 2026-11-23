package mail

import (
	"net/mail"
	"strings"
	"testing"
	"time"
)

func render(t *testing.T, lang string, name string) Message {
	t.Helper()
	r, err := NewRenderer()
	if err != nil {
		t.Fatal(err)
	}
	msg, err := r.VerifyEmail(lang, VerifyEmail{
		To: "a@example.vn", FullName: name, URL: "http://localhost:2001/verify-email?token=abc", ExpiresIn: 24 * time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	return msg
}

func TestVerifyEmailRendersBothLanguages(t *testing.T) {
	vi := render(t, "vi", "Nguyễn Văn A")
	en := render(t, "en", "Nguyen Van A")

	if !strings.Contains(vi.Text, "Xin chào Nguyễn Văn A") || !strings.Contains(vi.HTML, "Xác nhận email") {
		t.Errorf("vi message is not Vietnamese:\n%s\n%s", vi.Text, vi.HTML)
	}
	if !strings.Contains(en.Text, "Hello Nguyen Van A") || !strings.Contains(en.HTML, "Confirm email") {
		t.Errorf("en message is not English:\n%s\n%s", en.Text, en.HTML)
	}
	if vi.Subject == en.Subject || vi.Subject == "" || en.Subject == "" {
		t.Errorf("subjects must be set and differ: %q / %q", vi.Subject, en.Subject)
	}
	for _, m := range []Message{vi, en} {
		if !strings.Contains(m.Text, "token=abc") || !strings.Contains(m.HTML, "token=abc") {
			t.Errorf("link missing from %q:\n%s", m.Subject, m.Text)
		}
		if !strings.Contains(m.Text, "24 ") || m.To != "a@example.vn" {
			t.Errorf("hours or recipient missing: %+v", m)
		}
	}
}

func TestVerifyEmailUnknownLanguageFallsBackToVietnamese(t *testing.T) {
	if got := render(t, "fr", "A"); !strings.Contains(got.Text, "Xin chào") {
		t.Errorf("fallback should be Vietnamese: %s", got.Text)
	}
}

func TestVerifyEmailEscapesTheNameInHTML(t *testing.T) {
	msg := render(t, "en", `<script>alert(1)</script>`)
	if strings.Contains(msg.HTML, "<script>") {
		t.Errorf("name was not escaped in HTML:\n%s", msg.HTML)
	}
}

func TestBuildMIME(t *testing.T) {
	from, _ := mail.ParseAddress("Rental <no-reply@rental.test>")
	to, _ := mail.ParseAddress("a@example.vn")
	date := time.Date(2026, 11, 3, 8, 0, 0, 0, time.UTC)

	raw, err := buildMIME(from, to, Message{Subject: "Xác nhận", Text: "plain", HTML: "<p>html</p>"}, date)
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	for _, want := range []string{
		"From: \"Rental\" <no-reply@rental.test>", "To: <a@example.vn>", "Subject: =?utf-8?q?", "MIME-Version: 1.0",
		"multipart/alternative", "text/plain", "text/html",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("message lacks %q:\n%s", want, s)
		}
	}
	if strings.Index(s, "text/plain") > strings.Index(s, "text/html") {
		t.Error("the HTML part must come last")
	}

	if _, err := buildMIME(from, to, Message{Subject: "a\r\nBcc: x@evil.test"}, date); err == nil {
		t.Error("a subject with a line break must be refused")
	}
}
