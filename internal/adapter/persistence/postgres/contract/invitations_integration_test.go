//go:build integration

package contractpg

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/contract"
	"rental-management/internal/domain/shared"
)

func (r *repos) invite(t *testing.T, room, landlord uuid.UUID, hash string, recipient *uuid.UUID) uuid.UUID {
	t.Helper()
	sealed, deposit, ends := "sealed-"+hash, int64(1_000_000), day("2027-06-01")
	id, err := NewInvitationRepository(r.pool).Insert(context.Background(), domain.Invitation{
		RoomID: room, LandlordID: landlord, Status: domain.InvitationPending, TokenHash: hash, Sealed: &sealed,
		ExpiresAt: time.Now().Add(7 * 24 * time.Hour).Truncate(time.Microsecond),
		Terms: domain.InvitationTerms{
			PriceVND: 2_400_000, DepositVND: &deposit, OccupantCount: 2, StartsOn: day("2026-12-01"), EndsOn: &ends,
			ContractStatus: domain.StatusActive, Note: ptr("Tầng 2"),
		},
		RecipientID: recipient,
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func hash64(c string) string {
	out := ""
	for len(out) < 64 {
		out += c
	}
	return out[:64]
}

func TestInvitationInsertGetAndScope(t *testing.T) {
	ctx := context.Background()
	r := newRepos(t)
	repo := NewInvitationRepository(r.pool)
	id := r.invite(t, r.room, r.landlord, hash64("a"), &r.tenant)

	got, err := repo.Get(ctx, r.landlord, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.RoomName != "P101" || got.LandlordName != "Landlord" || got.BuildingName != nil || got.Status != domain.InvitationPending ||
		got.Terms.PriceVND != 2_400_000 || *got.Terms.DepositVND != 1_000_000 || got.Terms.OccupantCount != 2 ||
		*got.Terms.Note != "Tầng 2" || !got.Terms.StartsOn.Equal(day("2026-12-01")) || got.Sealed == nil || got.TokenHash != hash64("a") {
		t.Errorf("invitation = %+v", got)
	}
	if got.Recipient == nil || got.Recipient.ID != r.tenant || got.Recipient.FullName != "Tenant" || got.AnsweredBy != nil {
		t.Errorf("people = %+v / %+v", got.Recipient, got.AnsweredBy)
	}
	if _, err := repo.Get(ctx, r.other, id); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("another landlord: %v", err)
	}
	if _, err := repo.Get(ctx, r.landlord, uuid.New()); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("unknown id: %v", err)
	}
}

func TestInvitationDuplicateHashIsAUniqueViolation(t *testing.T) {
	r := newRepos(t)
	r.invite(t, r.room, r.landlord, hash64("b"), nil)
	_, err := NewInvitationRepository(r.pool).Insert(context.Background(), domain.Invitation{
		RoomID: r.room, LandlordID: r.landlord, Status: domain.InvitationPending, TokenHash: hash64("b"), ExpiresAt: time.Now().Add(time.Hour),
		Terms: domain.InvitationTerms{PriceVND: 1, OccupantCount: 1, StartsOn: day("2026-12-01"), ContractStatus: domain.StatusActive},
	})
	if !shared.IsUnique(err) {
		t.Errorf("error = %v", err)
	}
}

func TestInvitationListOrdersOpenFirstFiltersAndCounts(t *testing.T) {
	ctx := context.Background()
	r := newRepos(t)
	repo := NewInvitationRepository(r.pool)
	revoked := r.invite(t, r.room, r.landlord, hash64("c"), nil)
	open1 := r.invite(t, r.room, r.landlord, hash64("d"), nil)
	open2 := r.invite(t, r.room, r.landlord, hash64("e"), nil)
	r.invite(t, r.newRoom(t, r.other, "Z1"), r.other, hash64("f"), nil) // somebody else's

	inv, err := repo.ForUpdate(ctx, revoked)
	if err != nil {
		t.Fatal(err)
	}
	if err := inv.Revoke(time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(ctx, inv); err != nil {
		t.Fatal(err)
	}

	items, total, err := repo.List(ctx, r.landlord, nil, 10, 0)
	if err != nil || total != 3 || len(items) != 3 {
		t.Fatalf("list = %d of %d, %v", len(items), total, err)
	}
	if items[0].ID != open2 || items[1].ID != open1 || items[2].ID != revoked {
		t.Errorf("open ones first, newest first: %v %v %v", items[0].ID, items[1].ID, items[2].ID)
	}
	pending := domain.InvitationPending
	if items, total, _ := repo.List(ctx, r.landlord, &pending, 1, 1); total != 2 || len(items) != 1 || items[0].ID != open1 {
		t.Errorf("pending, second page of one: %d of %d", len(items), total)
	}
	rv := domain.InvitationRevoked
	if items, total, _ := repo.List(ctx, r.landlord, &rv, 10, 0); total != 1 || len(items) != 1 || items[0].ID != revoked {
		t.Errorf("revoked: %d of %d", len(items), total)
	}
}

func TestInvitationSaveRoundTripWipesTheSecret(t *testing.T) {
	ctx := context.Background()
	r := newRepos(t)
	repo := NewInvitationRepository(r.pool)
	id := r.invite(t, r.room, r.landlord, hash64("g"), nil)

	inv, err := repo.ForUpdate(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	sealed := "new-sealed"
	if err := inv.Renew(hash64("h"), &sealed, time.Now().Add(48*time.Hour).Truncate(time.Microsecond)); err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(ctx, inv); err != nil {
		t.Fatal(err)
	}
	got, _ := repo.Get(ctx, r.landlord, id)
	if got.TokenHash != hash64("h") || *got.Sealed != sealed || !got.ExpiresAt.Equal(inv.ExpiresAt) {
		t.Errorf("renewed = %+v", got)
	}

	contract := r.insert(t, r.room, r.landlord, &r.tenant, domain.StatusActive, 1)
	inv.Answer(domain.InvitationAccepted, r.tenant, time.Now().Truncate(time.Microsecond), &contract)
	if err := repo.Save(ctx, inv); err != nil {
		t.Fatal(err)
	}
	got, _ = repo.Get(ctx, r.landlord, id)
	if got.Status != domain.InvitationAccepted || got.Sealed != nil || got.ContractID == nil || *got.ContractID != contract ||
		got.AnsweredBy == nil || got.AnsweredBy.ID != r.tenant {
		t.Errorf("accepted = %+v", got)
	}
}

func TestInvitationFindByHashAndForRecipient(t *testing.T) {
	ctx := context.Background()
	r := newRepos(t)
	repo := NewInvitationRepository(r.pool)
	named := r.invite(t, r.room, r.landlord, hash64("i"), &r.tenant)
	public := r.invite(t, r.room, r.landlord, hash64("j"), nil)

	got, err := repo.FindByTokenHash(ctx, hash64("i"))
	if err != nil || got.ID != named || got.RecipientID == nil || *got.RecipientID != r.tenant {
		t.Errorf("by hash = %+v, %v", got, err)
	}
	if _, err := repo.FindByTokenHash(ctx, hash64("z")); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("unknown hash: %v", err)
	}
	if got, err := repo.FindForRecipient(ctx, named, r.tenant); err != nil || got.ID != named {
		t.Errorf("for recipient = %+v, %v", got, err)
	}
	for name, args := range map[string][2]uuid.UUID{
		"another account": {named, r.tenant2}, "an offer to anybody": {public, r.tenant}, "unknown id": {uuid.New(), r.tenant},
	} {
		if _, err := repo.FindForRecipient(ctx, args[0], args[1]); !errors.Is(err, shared.ErrNotFound) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestInvitationOfferAndInbox(t *testing.T) {
	ctx := context.Background()
	r := newRepos(t)
	repo := NewInvitationRepository(r.pool)
	now := time.Now()
	open := r.invite(t, r.room, r.landlord, hash64("k"), &r.tenant)
	answered := r.invite(t, r.room, r.landlord, hash64("l"), &r.tenant)
	expired := r.invite(t, r.room, r.landlord, hash64("m"), &r.tenant)
	r.invite(t, r.room, r.landlord, hash64("n"), &r.tenant2) // somebody else's
	r.invite(t, r.room, r.landlord, hash64("o"), nil)        // to anybody
	if _, err := r.pool.Exec(ctx, `UPDATE contract_invitations SET status = 'declined', link_token_sealed = NULL, answered_at = now() WHERE id = $1`, answered); err != nil {
		t.Fatal(err)
	}
	if _, err := r.pool.Exec(ctx, `UPDATE contract_invitations SET expires_at = now() - interval '1 minute' WHERE id = $1`, expired); err != nil {
		t.Fatal(err)
	}

	offer, err := repo.Offer(ctx, open)
	if err != nil {
		t.Fatal(err)
	}
	if offer.Room.Name != "P101" || offer.Landlord.ID != r.landlord || offer.Landlord.FullName != "Landlord" || offer.Terms.PriceVND != 2_400_000 ||
		offer.Room.AddressDetail == "" || offer.Room.Ward == nil || offer.Room.Cover == nil || offer.HouseName != nil {
		t.Errorf("offer = %+v", offer)
	}
	if _, err := repo.Offer(ctx, uuid.New()); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("unknown offer: %v", err)
	}

	items, total, err := repo.Inbox(ctx, r.tenant, now, 10, 0)
	if err != nil || total != 1 || len(items) != 1 || items[0].ID != open {
		t.Errorf("inbox = %d of %d, %v", len(items), total, err)
	}
	if items, total, _ := repo.Inbox(ctx, r.tenant, now, 10, 5); total != 1 || len(items) != 0 {
		t.Errorf("a page past the end: %d of %d", len(items), total)
	}
	if _, total, _ := repo.Inbox(ctx, r.tenant, now.Add(-time.Hour), 10, 0); total != 2 {
		t.Errorf("an hour earlier the late one is still open: %d", total)
	}
}

func TestInvitationRevokeOpenOfRooms(t *testing.T) {
	ctx := context.Background()
	r := newRepos(t)
	repo := NewInvitationRepository(r.pool)
	other := r.newRoom(t, r.landlord, "P102")
	open := r.invite(t, r.room, r.landlord, hash64("p"), nil)
	done := r.invite(t, r.room, r.landlord, hash64("q"), nil)
	kept := r.invite(t, other, r.landlord, hash64("r"), nil)
	if _, err := r.pool.Exec(ctx, `UPDATE contract_invitations SET status = 'declined', link_token_sealed = NULL, answered_at = now() WHERE id = $1`, done); err != nil {
		t.Fatal(err)
	}

	at := time.Now().Truncate(time.Microsecond)
	revoked, err := repo.RevokeOpenOfRooms(ctx, []uuid.UUID{r.room}, at)
	if err != nil || len(revoked) != 1 || revoked[0].ID != open || revoked[0].Status != domain.InvitationRevoked || revoked[0].Sealed != nil {
		t.Fatalf("revoked = %+v, %v", revoked, err)
	}
	if got, _ := repo.Get(ctx, r.landlord, open); got.Status != domain.InvitationRevoked || got.AnsweredAt == nil || got.Sealed != nil {
		t.Errorf("stored = %+v", got)
	}
	if got, _ := repo.Get(ctx, r.landlord, done); got.Status != domain.InvitationDeclined {
		t.Errorf("answered ones are untouched: %s", got.Status)
	}
	if got, _ := repo.Get(ctx, r.landlord, kept); got.Status != domain.InvitationPending {
		t.Errorf("another room's offer is untouched: %s", got.Status)
	}
	if none, err := repo.RevokeOpenOfRooms(ctx, nil, at); err != nil || len(none) != 0 {
		t.Errorf("no rooms: %v, %v", none, err)
	}
}
