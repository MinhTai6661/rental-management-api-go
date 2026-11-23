package mail

import (
	"strings"
	"testing"
	"time"
)

func renderInvitation(t *testing.T, lang string, data ContractInvitation) Message {
	t.Helper()
	r, err := NewRenderer()
	if err != nil {
		t.Fatal(err)
	}
	msg, err := r.ContractInvitation(lang, data)
	if err != nil {
		t.Fatal(err)
	}
	return msg
}

func invitationData() ContractInvitation {
	return ContractInvitation{
		To: "a@example.vn", Landlord: "Chủ Trọ", Room: "P101", URL: "http://localhost:2001/contract-invitation?invitation=abc",
		OptOutURL: "http://localhost:2001/invitation-opt-out?email=a%40example.vn&token=t", ExpiresAt: time.Date(2026, 11, 30, 3, 0, 0, 0, time.UTC),
	}
}

func TestContractInvitationRendersBothLanguages(t *testing.T) {
	vi := renderInvitation(t, "vi", invitationData())
	en := renderInvitation(t, "en", invitationData())
	if !strings.Contains(vi.Subject, "P101") || !strings.Contains(vi.Text, "mời bạn thuê phòng P101") || !strings.Contains(vi.Text, "30/11/2026") {
		t.Errorf("vi message is not Vietnamese:\n%s\n%s", vi.Subject, vi.Text)
	}
	if !strings.Contains(en.Subject, "P101") || !strings.Contains(en.Text, "offers you the room P101") || !strings.Contains(en.Text, "30 November 2026") {
		t.Errorf("en message is not English:\n%s\n%s", en.Subject, en.Text)
	}
	if vi.Subject == en.Subject {
		t.Error("the subjects differ by language")
	}
	for _, m := range []Message{vi, en} {
		if m.To != "a@example.vn" || !strings.Contains(m.Text, "invitation=abc") || !strings.Contains(m.HTML, "invitation=abc") {
			t.Errorf("recipient or link missing: %+v", m)
		}
		if !strings.Contains(m.Text, "invitation-opt-out") || !strings.Contains(m.HTML, "invitation-opt-out") {
			t.Errorf("the opt-out link is missing:\n%s", m.Text)
		}
	}
}

func TestContractInvitationLeavesOutTheOptOutLineWithoutALink(t *testing.T) {
	data := invitationData()
	data.OptOutURL = ""
	for _, lang := range []string{"vi", "en"} {
		m := renderInvitation(t, lang, data)
		if strings.Contains(m.Text, "opt-out") || strings.Contains(m.HTML, "opt-out") {
			t.Errorf("%s: no opt-out link was given", lang)
		}
	}
}

func TestContractInvitationEscapesTheNamesInHTMLAndFallsBackToVietnamese(t *testing.T) {
	data := invitationData()
	data.Landlord = `<script>alert(1)</script>`
	m := renderInvitation(t, "fr", data)
	if strings.Contains(m.HTML, "<script>") {
		t.Errorf("landlord was not escaped in HTML:\n%s", m.HTML)
	}
	if !strings.Contains(m.Text, "mời bạn thuê phòng") {
		t.Errorf("an unknown language gives Vietnamese:\n%s", m.Text)
	}
}
