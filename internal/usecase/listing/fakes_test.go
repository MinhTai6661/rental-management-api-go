package listing

import (
	"context"
	"sort"
	"time"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/listing"
	"rental-management/internal/domain/shared"
)

// store is an in-memory stand-in for the database: it implements every port that reads or writes rows.
type store struct {
	now       time.Time
	rooms     map[uuid.UUID]*domain.RoomFacts
	listings  map[uuid.UUID]*domain.Listing
	order     []uuid.UUID
	landlords map[uuid.UUID]string
	offers    map[uuid.UUID]domain.Offer
	extras    int // how many times Extras was read
	searched  []domain.OfferQuery
}

func newStore(now time.Time) *store {
	return &store{
		now: now, rooms: map[uuid.UUID]*domain.RoomFacts{}, listings: map[uuid.UUID]*domain.Listing{},
		landlords: map[uuid.UUID]string{}, offers: map[uuid.UUID]domain.Offer{},
	}
}

func (s *store) landlord(name string) uuid.UUID {
	id := uuid.New()
	s.landlords[id] = name
	return id
}

func (s *store) room(landlord uuid.UUID, name string) *domain.RoomFacts {
	r := &domain.RoomFacts{ID: uuid.New(), LandlordID: landlord, Name: name, Live: true}
	s.rooms[r.ID] = r
	return r
}

// add stores a listing in the given status for the room.
func (s *store) add(room *domain.RoomFacts, status domain.Status) *domain.Listing {
	l := &domain.Listing{
		ID: uuid.New(), RoomID: room.ID, LandlordID: room.LandlordID, Title: "Listing of " + room.Name, Status: status,
		CreatedAt: s.now,
	}
	s.listings[l.ID] = l
	s.order = append(s.order, l.ID)
	return l
}

func (s *store) detailOf(l *domain.Listing) domain.Detail {
	r := s.rooms[l.RoomID]
	return domain.Detail{
		Listing: *l, Room: domain.RoomRef{ID: r.ID, Name: r.Name, Live: r.Live},
		Landlord: domain.Person{ID: l.LandlordID, FullName: s.landlords[l.LandlordID]},
	}
}

func (s *store) inScope(l *domain.Listing, landlord *uuid.UUID) bool {
	return landlord == nil || l.LandlordID == *landlord
}

// --- ListingRepository

func (s *store) Search(_ context.Context, q domain.Query) ([]domain.Detail, int, error) {
	var out []domain.Detail
	for i := len(s.order) - 1; i >= 0; i-- { // newest first
		l := s.listings[s.order[i]]
		switch {
		case !s.inScope(l, q.LandlordID),
			q.Status != nil && l.Status != *q.Status,
			q.ExcludeDrafts && l.Status == domain.StatusDraft:
			continue
		}
		out = append(out, s.detailOf(l))
	}
	total := len(out)
	if q.Offset < len(out) {
		out = out[q.Offset:]
	} else {
		out = nil
	}
	if q.Limit > 0 && len(out) > q.Limit {
		out = out[:q.Limit]
	}
	return out, total, nil
}

func (s *store) Counts(_ context.Context, landlord *uuid.UUID, excludeDrafts bool) (domain.StatusCounts, error) {
	out := domain.StatusCounts{}
	for _, l := range s.listings {
		if s.inScope(l, landlord) && (!excludeDrafts || l.Status != domain.StatusDraft) {
			out[l.Status]++
		}
	}
	return out, nil
}

func (s *store) Get(_ context.Context, landlord *uuid.UUID, id uuid.UUID) (domain.Detail, error) {
	l, ok := s.listings[id]
	if !ok || !s.inScope(l, landlord) {
		return domain.Detail{}, shared.ErrNotFound
	}
	return s.detailOf(l), nil
}

func (s *store) ByIDs(_ context.Context, ids []uuid.UUID) ([]domain.Listing, error) {
	var out []domain.Listing
	for _, id := range ids {
		if l, ok := s.listings[id]; ok {
			out = append(out, *l)
		}
	}
	return out, nil
}

func (s *store) ForUpdate(_ context.Context, id uuid.UUID) (domain.Listing, error) {
	l, ok := s.listings[id]
	if !ok {
		return domain.Listing{}, shared.ErrNotFound
	}
	return *l, nil
}

func (s *store) Insert(_ context.Context, l domain.Listing) (uuid.UUID, error) {
	l.ID = uuid.New()
	s.listings[l.ID] = &l
	s.order = append(s.order, l.ID)
	return l.ID, nil
}

func (s *store) Save(_ context.Context, l domain.Listing) error {
	cp := l
	s.listings[l.ID] = &cp
	return nil
}

func (s *store) Delete(_ context.Context, id uuid.UUID) error {
	delete(s.listings, id)
	return nil
}

func (s *store) OpenOfRoom(_ context.Context, roomID uuid.UUID, except *uuid.UUID) (bool, error) {
	for _, l := range s.listings {
		if l.RoomID == roomID && l.Status.IsOpen() && (except == nil || l.ID != *except) {
			return true, nil
		}
	}
	return false, nil
}

func (s *store) OpenRooms(_ context.Context, roomIDs []uuid.UUID) ([]uuid.UUID, error) {
	var out []uuid.UUID
	for _, id := range roomIDs {
		if open, _ := s.OpenOfRoom(context.Background(), id, nil); open {
			out = append(out, id)
		}
	}
	return out, nil
}

func (s *store) LockOpenOfRooms(_ context.Context, roomIDs []uuid.UUID) ([]domain.Listing, error) {
	var out []domain.Listing
	for _, id := range s.order {
		l := s.listings[id]
		if l == nil || !l.Status.IsOpen() {
			continue
		}
		for _, r := range roomIDs {
			if l.RoomID == r {
				out = append(out, *l)
			}
		}
	}
	return out, nil
}

func (s *store) CountOpen(_ context.Context, landlord uuid.UUID) (int, error) {
	n := 0
	for _, l := range s.listings {
		if l.LandlordID == landlord && l.Status.IsOpen() {
			n++
		}
	}
	return n, nil
}

func (s *store) LockLandlord(context.Context, uuid.UUID) error { return nil }

// --- RoomReader

func (s *store) Lock(_ context.Context, id uuid.UUID) (domain.RoomFacts, error) {
	r, ok := s.rooms[id]
	if !ok {
		return domain.RoomFacts{}, shared.ErrNotFound
	}
	return *r, nil
}

func (s *store) Extras(context.Context, uuid.UUID) ([]domain.ChargeLine, []domain.Picture, error) {
	s.extras++
	return []domain.ChargeLine{{Name: "Điện"}}, []domain.Picture{{Key: "k"}}, nil
}

// --- OfferRepository

func (s *store) Landlords(_ context.Context, _ domain.OfferQuery) ([]uuid.UUID, error) {
	var out []uuid.UUID
	for id := range s.landlords {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out, nil
}

func (s *store) searchOffers(q domain.OfferQuery) ([]domain.Offer, int, error) {
	s.searched = append(s.searched, q)
	var out []domain.Offer
	for _, o := range s.offers {
		out = append(out, o)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, len(out), nil
}

func (s *store) Get2(id uuid.UUID) (domain.Offer, error) {
	o, ok := s.offers[id]
	if !ok {
		return domain.Offer{}, shared.ErrNotFound
	}
	return o, nil
}

// offerRepo adapts the store to OfferRepository (its Get clashes with ListingRepository.Get).
type offerRepo struct{ *store }

func (o offerRepo) Search(_ context.Context, q domain.OfferQuery) ([]domain.Offer, int, error) {
	return o.searchOffers(q)
}

func (o offerRepo) Get(_ context.Context, id uuid.UUID) (domain.Offer, error) { return o.Get2(id) }

func (o offerRepo) Landlord(_ context.Context, id uuid.UUID) (domain.LandlordCard, error) {
	name, ok := o.landlords[id]
	if !ok {
		return domain.LandlordCard{}, shared.ErrNotFound
	}
	return domain.LandlordCard{ID: id, FullName: name, Houses: []string{"Nhà trọ"}, VacantRooms: 2}, nil
}

// --- SeedRooms

func (s *store) WithoutListing(_ context.Context, landlord uuid.UUID) ([]domain.SeedRoom, error) {
	var out []domain.SeedRoom
	for _, r := range s.rooms {
		has := false
		for _, l := range s.listings {
			has = has || l.RoomID == r.ID
		}
		if r.LandlordID == landlord && !has {
			out = append(out, domain.SeedRoom{ID: r.ID, Name: r.Name})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// --- the rest of the ports

type fakeTx struct{}

func (fakeTx) WithinTx(ctx context.Context, fn func(ctx context.Context) error) error { return fn(ctx) }

type notices struct{ sent []Notification }

func (n *notices) Notify(_ context.Context, m Notification) error {
	n.sent = append(n.sent, m)
	return nil
}

type audits struct{ events []AuditEvent }

func (a *audits) Record(_ context.Context, e AuditEvent) error {
	a.events = append(a.events, e)
	return nil
}

func (a *audits) actions() []string {
	out := make([]string, len(a.events))
	for i, e := range a.events {
		out[i] = e.Action
	}
	return out
}

type fixedLimit struct{ limit *int }

func (f fixedLimit) MaxActiveListings(context.Context, uuid.UUID) (*int, error) { return f.limit, nil }

// ranks answers the priority port and remembers whom it was asked about.
type ranks struct {
	values map[uuid.UUID]int
	asked  [][]uuid.UUID
}

func (r *ranks) SearchPriorities(_ context.Context, ids []uuid.UUID) (map[uuid.UUID]int, error) {
	r.asked = append(r.asked, ids)
	return r.values, nil
}
