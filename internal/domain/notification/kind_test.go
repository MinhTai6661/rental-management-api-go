package notification

import (
	"errors"
	"slices"
	"testing"
)

func TestKindsAreSortedUniqueAndParse(t *testing.T) {
	kinds := Kinds()
	if len(kinds) != 42 {
		t.Fatalf("want 42 kinds, got %d", len(kinds))
	}
	if !slices.IsSorted(kinds) {
		t.Error("Kinds must be sorted")
	}
	if len(slices.Compact(slices.Clone(kinds))) != len(kinds) {
		t.Error("Kinds must be unique")
	}
	for _, k := range kinds {
		got, err := ParseKind(string(k))
		if err != nil || got != k {
			t.Errorf("ParseKind(%q) = %q, %v", k, got, err)
		}
	}
}

func TestParseKindRefusesUnknown(t *testing.T) {
	for _, s := range []string{"", "nope", "Invoice_Sent", " invoice_sent"} {
		if _, err := ParseKind(s); !errors.Is(err, ErrUnknownKind) {
			t.Errorf("ParseKind(%q) = %v, want ErrUnknownKind", s, err)
		}
	}
}

func TestDefaultAudience(t *testing.T) {
	cases := map[Kind]Audience{
		InvoiceSent:                     AudienceTenant,
		LandlordRevoked:                 AudienceTenant,
		PaymentSubmitted:                AudienceLandlord,
		ListingApproved:                 AudienceLandlord,
		ContractInvitationNeedsApproval: AudienceLandlord,
		ContractEnded:                   AudienceNone,
		ListingSubmitted:                AudienceNone,
		SubscriptionActivated:           AudienceNone,
		KindAnnouncement:                AudienceNone,
		ManagerRevoked:                  AudienceNone,
	}
	for k, want := range cases {
		if got := k.DefaultAudience(); got != want {
			t.Errorf("%s: audience %q, want %q", k, got, want)
		}
	}
}

func TestEveryKindMatchesTheDatabaseRule(t *testing.T) {
	// The CHECK of the notifications table: lowercase letters and underscores, 1 to 50 characters.
	for _, k := range Kinds() {
		if len(k) < 1 || len(k) > 50 {
			t.Errorf("%s has a bad length", k)
		}
		for _, r := range k {
			if (r < 'a' || r > 'z') && r != '_' {
				t.Errorf("%s has the character %q", k, r)
			}
		}
	}
}
