package listing

import (
	"context"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/listing"
)

// Offers is the search of find rooms, for renters and for guests: the rooms that are vacant (available, nobody
// with an account holds a running contract, the landlord still holds the landlord role) and have an approved
// listing. What each audience may see of them is decided by the HTTP layer.
type Offers struct{ *core }

// NewOffers builds the search use cases.
func NewOffers(d Deps) *Offers { return &Offers{newCore(d)} }

// Search returns a page of offered rooms. Without an explicit sort the rooms of landlords whose plan has a higher
// search priority come first, newest first within a priority.
func (s *Offers) Search(ctx context.Context, f OfferFilter, limit, offset int) (OfferPage, error) {
	q := domain.OfferQuery{
		LandlordID: f.LandlordID, HouseLike: domain.SearchPattern(f.HouseName), WardCode: f.WardCode,
		ProvinceCode: f.ProvinceCode, PriceFrom: f.PriceFrom, PriceTo: f.PriceTo, AreaFrom: f.AreaFrom, AreaTo: f.AreaTo,
		Sort: f.Sort, Limit: limit, Offset: offset,
	}
	if q.Sort == "" {
		var err error
		if q.Priorities, err = s.priorities(ctx, q); err != nil {
			return OfferPage{}, err
		}
	}
	items, total, err := s.offers.Search(ctx, q)
	if err != nil {
		return OfferPage{}, err
	}
	return OfferPage{Items: items, Total: total}, nil
}

// priorities asks the plans how high each landlord with a matching room ranks; landlords at 0 are left out.
func (s *Offers) priorities(ctx context.Context, q domain.OfferQuery) ([]domain.Priority, error) {
	landlords, err := s.offers.Landlords(ctx, q)
	if err != nil || len(landlords) == 0 {
		return nil, err
	}
	ranks, err := s.priority.SearchPriorities(ctx, landlords)
	if err != nil {
		return nil, err
	}
	var out []domain.Priority
	for _, id := range landlords {
		if v := ranks[id]; v != 0 {
			out = append(out, domain.Priority{LandlordID: id, Value: v})
		}
	}
	return out, nil
}

// Detail returns an offered room with its costs and pictures; domain.ErrNotFound when the room is not offered (not
// vacant, no approved listing, or it does not exist).
func (s *Offers) Detail(ctx context.Context, roomID uuid.UUID) (domain.OfferDetail, error) {
	o, err := s.offers.Get(ctx, roomID)
	if err != nil {
		return domain.OfferDetail{}, notFound(err)
	}
	d := domain.OfferDetail{Offer: o}
	if d.Charges, d.Pictures, err = s.rooms.Extras(ctx, roomID); err != nil {
		return domain.OfferDetail{}, err
	}
	return d, nil
}

// Landlord returns a landlord as a renter or a guest may see them; domain.ErrNotFound for an account that is not an
// active landlord.
func (s *Offers) Landlord(ctx context.Context, id uuid.UUID) (domain.LandlordCard, error) {
	c, err := s.offers.Landlord(ctx, id)
	return c, notFound(err)
}
