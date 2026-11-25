package main

import (
	"context"
	"testing"

	"github.com/google/uuid"

	notificationdomain "rental-management/internal/domain/notification"
	adminuc "rental-management/internal/usecase/admin"
	contractuc "rental-management/internal/usecase/contract"
	invoiceuc "rental-management/internal/usecase/invoice"
	listinguc "rental-management/internal/usecase/listing"
	manageruc "rental-management/internal/usecase/manager"
	notificationuc "rental-management/internal/usecase/notification"
	paymentuc "rental-management/internal/usecase/payment"
	planuc "rental-management/internal/usecase/plan"
)

// TestEveryKindAContextSendsIsInTheCatalog is the guard of the one catalog: a context that starts sending a kind the
// notification domain does not know would have its messages refused (and only logged), so each Notify* constant is
// listed here. A new constant must be added to this list and to notification.Kinds.
func TestEveryKindAContextSendsIsInTheCatalog(t *testing.T) {
	kinds := []string{
		contractuc.NotifyRegistrationConfirmed, contractuc.NotifyRegistrationApproved, contractuc.NotifyRegistrationRejected,
		contractuc.NotifyCancellationRequested, contractuc.NotifyCancellationDeclined, contractuc.NotifyContractEnded,
		contractuc.NotifyInvitationReceived, contractuc.NotifyInvitationAccepted, contractuc.NotifyInvitationDeclined,
		contractuc.NotifyInvitationNeedsApproval, contractuc.NotifyInvitationApproved, contractuc.NotifyInvitationRejected,
		manageruc.NotifyInvitationReceived, manageruc.NotifyInvitationWithdrawn, manageruc.NotifyAccepted, manageruc.NotifyDeclined, manageruc.NotifyRevoked,
		planuc.NotifySubscriptionRequested, planuc.NotifySubscriptionActivated,
		listinguc.NotifySubmitted, listinguc.NotifyApproved, listinguc.NotifyRejected, listinguc.NotifyTakenDown,
		invoiceuc.NotifyInvoiceSent, invoiceuc.NotifyInvoiceUpdated, invoiceuc.NotifyInvoiceDueSoon, invoiceuc.NotifyInvoiceOverdue,
		invoiceuc.NotifyTenantInvoiceOverdue, invoiceuc.NotifyManagerBillSent, invoiceuc.NotifyManagerBillEdited,
		adminuc.NotifyApplicationReceived, adminuc.NotifyApplicationApproved, adminuc.NotifyApplicationRejected, adminuc.NotifyLandlordRevoked,
		paymentuc.NotifyPaymentSubmitted, paymentuc.NotifyPaymentConfirmed, paymentuc.NotifyPaymentRejected,
	}
	for _, k := range kinds {
		if _, err := notificationdomain.ParseKind(k); err != nil {
			t.Errorf("%q is sent by a context but is not in the notification catalog", k)
		}
	}
}

// messengerFake records what the adapters hand to the notification use cases.
type messengerFake struct {
	sent, toAdmins []notificationuc.Message
}

func (f *messengerFake) Send(_ context.Context, m notificationuc.Message) error {
	f.sent = append(f.sent, m)
	return nil
}

func (f *messengerFake) SendToAdmins(_ context.Context, m notificationuc.Message) error {
	f.toAdmins = append(f.toAdmins, m)
	return nil
}

func TestNotifierAdaptersMapEveryField(t *testing.T) {
	user := uuid.New()
	params := map[string]string{"room": "P101"}
	want := notificationuc.Message{UserIDs: []uuid.UUID{user}, Kind: "x_kind", Params: params, URL: "/u", Audience: "tenant"}
	ctx := context.Background()

	for name, notify := range map[string]func(m messenger) error{
		"contract": func(m messenger) error {
			return contractNotifier{m}.Notify(ctx, contractuc.Notification{UserID: user, Kind: "x_kind", Params: params, URL: "/u", Audience: "tenant"})
		},
		"invoice": func(m messenger) error {
			return invoiceNotifier{m}.Notify(ctx, invoiceuc.Notification{UserID: user, Kind: "x_kind", Params: params, URL: "/u", Audience: "tenant"})
		},
		"payment": func(m messenger) error {
			return paymentNotifier{m}.Notify(ctx, paymentuc.Notification{UserID: user, Kind: "x_kind", Params: params, URL: "/u", Audience: "tenant"})
		},
		"plan": func(m messenger) error {
			return planNotifier{m}.Notify(ctx, planuc.Notification{UserID: user, Kind: "x_kind", Params: params, URL: "/u", Audience: "tenant"})
		},
		"manager": func(m messenger) error {
			return managerNotifier{m}.Notify(ctx, manageruc.Notification{UserID: user, Kind: "x_kind", Params: params, URL: "/u", Audience: "tenant"})
		},
		"listing": func(m messenger) error {
			return listingNotifier{m}.Notify(ctx, listinguc.Notification{UserID: user, Kind: "x_kind", Params: params, URL: "/u", Audience: "tenant"})
		},
	} {
		f := &messengerFake{}
		if err := notify(f); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(f.sent) != 1 || len(f.toAdmins) != 0 {
			t.Fatalf("%s: one message to the user expected: %+v", name, f)
		}
		got := f.sent[0]
		if got.Kind != want.Kind || got.URL != want.URL || got.Audience != want.Audience || got.Params["room"] != "P101" ||
			len(got.UserIDs) != 1 || got.UserIDs[0] != user {
			t.Errorf("%s: %+v, want %+v", name, got, want)
		}
	}
}

func TestPlanAndListingNotifiersSendToAdmins(t *testing.T) {
	ctx := context.Background()
	f := &messengerFake{}
	if err := (planNotifier{f}).Notify(ctx, planuc.Notification{ToAdmins: true, Kind: "subscription_requested", URL: "/a"}); err != nil {
		t.Fatal(err)
	}
	if err := (listingNotifier{f}).Notify(ctx, listinguc.Notification{ToAdmins: true, Kind: "listing_submitted", URL: "/b"}); err != nil {
		t.Fatal(err)
	}
	if len(f.sent) != 0 || len(f.toAdmins) != 2 || f.toAdmins[0].Kind != "subscription_requested" || f.toAdmins[1].URL != "/b" {
		t.Errorf("both must go to the admins: %+v", f)
	}
}
