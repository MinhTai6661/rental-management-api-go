package mailadapter

import (
	"context"
	"strings"
	"testing"
	"time"

	"rental-management/pkg/mail"
)

func TestInvitationMailerSendsTheMessageWithTheOptOutLink(t *testing.T) {
	renderer, err := mail.NewRenderer()
	if err != nil {
		t.Fatal(err)
	}
	rec := &mail.Recorder{}
	m := NewInvitationMailer(rec, renderer, func(email string) string { return "http://web.test/invitation-opt-out?email=" + email })

	expires := time.Date(2026, 11, 30, 3, 0, 0, 0, time.UTC)
	if err := m.Send(context.Background(), "ann@example.vn", "Chủ Trọ", "P101", "http://web.test/contract-invitation?token=abc", expires); err != nil {
		t.Fatal(err)
	}
	sent := rec.Sent()
	if len(sent) != 1 || sent[0].To != "ann@example.vn" || !strings.Contains(sent[0].Subject, "P101") {
		t.Fatalf("one message to the address expected: %+v", sent)
	}
	for _, want := range []string{"token=abc", "invitation-opt-out?email=ann@example.vn", "Chủ Trọ"} {
		if !strings.Contains(sent[0].Text, want) {
			t.Errorf("%q is missing from the text:\n%s", want, sent[0].Text)
		}
	}
}

func TestInvitationMailerWithoutAnOptOutLinkOmitsIt(t *testing.T) {
	renderer, _ := mail.NewRenderer()
	rec := &mail.Recorder{}
	if err := NewInvitationMailer(rec, renderer, nil).Send(context.Background(), "a@example.vn", "L", "P1", "http://web.test/x", time.Now()); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rec.Sent()[0].Text, "opt-out") {
		t.Error("no opt-out link was configured")
	}
}
