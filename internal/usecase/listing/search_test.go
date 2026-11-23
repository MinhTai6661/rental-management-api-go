package listing

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/listing"
)

func TestSearchOrdersByPlanPriorityWithoutASort(t *testing.T) {
	ctx := context.Background()
	w := newWorld(nil)
	vip, free := w.landlord, w.other
	rk := &ranks{values: map[uuid.UUID]int{vip: 30}}
	w.deps.Priority = rk
	offers := NewOffers(w.deps)

	_, err := offers.Search(ctx, OfferFilter{HouseName: " Sao_Mai "}, 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	q := w.store.searched[0]
	if len(rk.asked) != 1 || len(rk.asked[0]) != 2 {
		t.Fatalf("the port is asked once, about every landlord that matches: %v", rk.asked)
	}
	if len(q.Priorities) != 1 || q.Priorities[0].LandlordID != vip || q.Priorities[0].Value != 30 {
		t.Errorf("only landlords above 0 are passed on: %+v (free landlord %s)", q.Priorities, free)
	}
	if q.HouseLike == nil || *q.HouseLike != `sao\_mai` {
		t.Errorf("the house name is a lower case, escaped pattern: %v", q.HouseLike)
	}

	t.Run("an explicit sort ignores the plans", func(t *testing.T) {
		rk.asked, w.store.searched = nil, nil
		if _, err := offers.Search(ctx, OfferFilter{Sort: "price"}, 20, 0); err != nil {
			t.Fatal(err)
		}
		if len(rk.asked) != 0 || len(w.store.searched[0].Priorities) != 0 || w.store.searched[0].Sort != "price" {
			t.Errorf("asked %v, query %+v", rk.asked, w.store.searched[0])
		}
	})
	t.Run("with no plan context every landlord ranks the same", func(t *testing.T) {
		w.deps.Priority = nil
		w.store.searched = nil
		if _, err := NewOffers(w.deps).Search(ctx, OfferFilter{}, 20, 0); err != nil {
			t.Fatal(err)
		}
		if len(w.store.searched[0].Priorities) != 0 {
			t.Errorf("neutral: %+v", w.store.searched[0].Priorities)
		}
	})
}

func TestOfferDetailAndLandlord(t *testing.T) {
	ctx := context.Background()
	w := newWorld(nil)
	offers := NewOffers(w.deps)
	roomID := uuid.New()
	w.store.offers[roomID] = domain.Offer{RoomID: roomID, Name: "P101", HouseName: "Nhà trọ"}

	d, err := offers.Detail(ctx, roomID)
	if err != nil || d.Name != "P101" || len(d.Charges) != 1 || len(d.Pictures) != 1 {
		t.Fatalf("detail: %+v %v", d, err)
	}
	if _, err := offers.Detail(ctx, uuid.New()); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("a room that is not offered: %v", err)
	}
	c, err := offers.Landlord(ctx, w.landlord)
	if err != nil || c.FullName != "Chu Hoa" {
		t.Errorf("landlord: %+v %v", c, err)
	}
	if _, err := offers.Landlord(ctx, uuid.New()); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("not a landlord: %v", err)
	}
}
