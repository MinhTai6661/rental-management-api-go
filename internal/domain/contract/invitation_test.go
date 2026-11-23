package contract

import (
	"errors"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

var t0 = time.Date(2026, 11, 23, 3, 0, 0, 0, time.UTC)

func pendingInvitation() Invitation {
	sealed := "sealed"
	return Invitation{
		ID: uuid.New(), RoomID: uuid.New(), LandlordID: uuid.New(), Status: InvitationPending, TokenHash: "h",
		Sealed: &sealed, ExpiresAt: t0.Add(24 * time.Hour),
	}
}

func TestOpenAtAndExpiredAroundTheEnd(t *testing.T) {
	inv := pendingInvitation()
	for name, tc := range map[string]struct {
		now           time.Time
		open, expired bool
	}{
		"before":   {inv.ExpiresAt.Add(-time.Nanosecond), true, false},
		"exactly":  {inv.ExpiresAt, false, true},
		"after":    {inv.ExpiresAt.Add(time.Nanosecond), false, true},
		"far past": {inv.ExpiresAt.Add(time.Hour), false, true},
	} {
		if inv.OpenAt(tc.now) != tc.open || inv.Expired(tc.now) != tc.expired {
			t.Errorf("%s: open=%v expired=%v", name, inv.OpenAt(tc.now), inv.Expired(tc.now))
		}
	}
	for _, st := range []InvitationStatus{InvitationAwaitingApproval, InvitationAccepted, InvitationDeclined, InvitationRevoked} {
		inv.Status = st
		if inv.OpenAt(t0) || inv.Expired(t0.Add(48*time.Hour)) {
			t.Errorf("%s is neither open nor expired", st)
		}
	}
}

func TestLinkAvailableNeedsAnOpenOfferAndTheSealedSecret(t *testing.T) {
	inv := pendingInvitation()
	if !inv.LinkAvailable(t0) {
		t.Error("an open offer with its secret shows the link")
	}
	inv.Sealed = nil
	if inv.LinkAvailable(t0) {
		t.Error("without the secret there is no link")
	}
	inv = pendingInvitation()
	if inv.LinkAvailable(inv.ExpiresAt) {
		t.Error("an expired offer shows no link")
	}
}

func TestAnswerWipesTheSecret(t *testing.T) {
	for _, st := range []InvitationStatus{InvitationAccepted, InvitationDeclined} {
		inv, user, contract := pendingInvitation(), uuid.New(), uuid.New()
		inv.Answer(st, user, t0, &contract)
		if inv.Status != st || inv.Sealed != nil || inv.AnsweredByID == nil || *inv.AnsweredByID != user ||
			inv.AnsweredAt == nil || !inv.AnsweredAt.Equal(t0) || inv.ContractID == nil {
			t.Errorf("%s: %+v", st, inv)
		}
	}
}

func TestRevokeFromOpenStatusesOnly(t *testing.T) {
	for _, st := range []InvitationStatus{InvitationPending, InvitationAwaitingApproval} {
		inv := pendingInvitation()
		inv.Status = st
		if err := inv.Revoke(t0); err != nil || inv.Status != InvitationRevoked || inv.Sealed != nil || inv.AnsweredAt == nil {
			t.Errorf("%s: err=%v %+v", st, err, inv)
		}
	}
	for _, st := range []InvitationStatus{InvitationAccepted, InvitationDeclined, InvitationRevoked} {
		inv := pendingInvitation()
		inv.Status = st
		if err := inv.Revoke(t0); !errors.Is(err, ErrInvitationNotPending) {
			t.Errorf("%s: err = %v", st, err)
		}
	}
}

func TestRenewOnlyWhileWaiting(t *testing.T) {
	inv := pendingInvitation()
	inv.ExpiresAt = t0.Add(-time.Hour) // expired but still pending: it can get a new link
	sealed := "new"
	if err := inv.Renew("hash2", &sealed, t0.Add(time.Hour)); err != nil || inv.TokenHash != "hash2" || *inv.Sealed != "new" || !inv.OpenAt(t0) {
		t.Errorf("renew: %v %+v", err, inv)
	}
	for _, st := range []InvitationStatus{InvitationAwaitingApproval, InvitationAccepted, InvitationDeclined, InvitationRevoked} {
		inv := pendingInvitation()
		inv.Status = st
		if err := inv.Renew("x", nil, t0); !errors.Is(err, ErrInvitationNotPending) {
			t.Errorf("%s: err = %v", st, err)
		}
	}
}

func TestAuthoredByTheLandlordOrTheManagerWhoDraftedIt(t *testing.T) {
	inv, manager := pendingInvitation(), uuid.New()
	inv.CreatedByID = &manager
	if !inv.AuthoredBy(inv.LandlordID) || !inv.AuthoredBy(manager) || inv.AuthoredBy(uuid.New()) {
		t.Error("only the landlord and the drafting manager wrote it")
	}
}

func TestValidLinkToken(t *testing.T) {
	good := strings.Repeat("aB3-_", 12) + "abcd"
	for token, want := range map[string]bool{
		good: true, good[:63]: false, good + "a": false, strings.Repeat("a", 63) + "+": false, strings.Repeat("a", 63) + "=": false, "": false,
	} {
		if got := ValidLinkToken(token); got != want {
			t.Errorf("ValidLinkToken(%q) = %v", token, got)
		}
	}
}

func TestValidOfferStatus(t *testing.T) {
	for st, want := range map[Status]bool{StatusPending: true, StatusActive: true, StatusEnded: false, StatusRejected: false, "": false} {
		if got := ValidOfferStatus(st); got != want {
			t.Errorf("ValidOfferStatus(%q) = %v", st, got)
		}
	}
}

func TestParseInvitationStatus(t *testing.T) {
	for _, st := range InvitationStatuses() {
		if got, ok := ParseInvitationStatus(string(st)); !ok || got != st {
			t.Errorf("ParseInvitationStatus(%q) = %q, %v", st, got, ok)
		}
	}
	if _, ok := ParseInvitationStatus("expired"); ok {
		t.Error("expired is computed, not stored")
	}
}

// The CHECK of the table lists the same statuses as the code (the database is the last defence, so the two must agree).
func TestInvitationStatusesMatchTheMigration(t *testing.T) {
	sql, err := os.ReadFile("../../../db/migrations/00009_contract_invitations.sql")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`status\s+text\s+NOT NULL DEFAULT 'pending'\s+CHECK \(status IN \(([^)]*)\)\)`).FindSubmatch(sql)
	if m == nil {
		t.Fatal("the status CHECK of contract_invitations was not found in the migration")
	}
	var inDB []string
	for _, v := range strings.Split(string(m[1]), ",") {
		inDB = append(inDB, strings.Trim(strings.TrimSpace(v), "'"))
	}
	var inCode []string
	for _, st := range InvitationStatuses() {
		inCode = append(inCode, string(st))
	}
	slices.Sort(inDB)
	slices.Sort(inCode)
	if !slices.Equal(inDB, inCode) {
		t.Errorf("migration %v, code %v", inDB, inCode)
	}
}
