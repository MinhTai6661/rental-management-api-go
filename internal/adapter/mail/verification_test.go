package mailadapter

import (
	"context"
	"net/url"
	"strings"
	"testing"
	"time"

	"rental-management/pkg/mail"
)

func TestSendVerificationBuildsTheLinkAndSendsTheMessage(t *testing.T) {
	renderer, err := mail.NewRenderer()
	if err != nil {
		t.Fatal(err)
	}
	rec := &mail.Recorder{}
	m := NewVerificationMailer(rec, renderer, "http://localhost:2001/")

	if err := m.SendVerification(context.Background(), "ann@example.vn", "Ann", "a+b/c", "en", 24*time.Hour); err != nil {
		t.Fatal(err)
	}
	sent := rec.Sent()
	if len(sent) != 1 || sent[0].To != "ann@example.vn" {
		t.Fatalf("one message to the address expected: %+v", sent)
	}
	if want := "http://localhost:2001/verify-email?token=" + url.QueryEscape("a+b/c"); !strings.Contains(sent[0].Text, want) {
		t.Errorf("the link %q is missing from the text:\n%s", want, sent[0].Text)
	}
}
