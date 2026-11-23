package invitation

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/invitation"
	"rental-management/internal/domain/shared"
	"rental-management/pkg/authz"
)

type fakePeople struct {
	people []domain.Person
}

func (f *fakePeople) ByID(_ context.Context, id uuid.UUID) (domain.Person, error) {
	for _, p := range f.people {
		if p.ID == id {
			return p, nil
		}
	}
	return domain.Person{}, shared.ErrNotFound
}

func (f *fakePeople) ByEmail(_ context.Context, normalized string) (domain.Person, error) {
	for _, p := range f.people {
		if p.Email != nil && *p.Email == normalized {
			return p, nil
		}
	}
	return domain.Person{}, shared.ErrNotFound
}

func (f *fakePeople) OptedOut(_ context.Context, normalized string) (bool, error) {
	for _, p := range f.people {
		if p.Email != nil && *p.Email == normalized && !p.AcceptsInvitations {
			return true, nil
		}
	}
	return false, nil
}

type fakeBlocks struct{ hashes map[string]bool }

func (f *fakeBlocks) Add(_ context.Context, hash string) error {
	f.hashes[hash] = true
	return nil
}

func (f *fakeBlocks) Exists(_ context.Context, hash string) (bool, error) { return f.hashes[hash], nil }

// fakeSigner signs with a readable prefix.
type fakeSigner struct{}

func (fakeSigner) Sign(email string) string { return "sig:" + email }
func (fakeSigner) Verify(email, token string) bool {
	return token == "sig:"+email
}

type fakeAudit struct{ events []AuditEvent }

func (f *fakeAudit) Record(_ context.Context, e AuditEvent) error {
	f.events = append(f.events, e)
	return nil
}

type fixture struct {
	uc       *Invitees
	people   *fakePeople
	blocks   *fakeBlocks
	audit    *fakeAudit
	landlord domain.Person
}

func ptr[T any](v T) *T { return &v }

func person(email string, mutate func(*domain.Person)) domain.Person {
	p := domain.Person{
		ID: uuid.New(), FullName: "Renter", Email: &email, EmailVerified: true, Active: true, AcceptsInvitations: true,
		Roles: []string{authz.RoleTenant},
	}
	if mutate != nil {
		mutate(&p)
	}
	return p
}

func newFixture(t *testing.T, signer OptOutSigner) *fixture {
	t.Helper()
	landlord := person("landlord@example.vn", func(p *domain.Person) { p.Roles = []string{authz.RoleLandlord, authz.RoleTenant} })
	f := &fixture{people: &fakePeople{people: []domain.Person{landlord}}, blocks: &fakeBlocks{hashes: map[string]bool{}}, audit: &fakeAudit{}, landlord: landlord}
	f.uc = NewInvitees(Deps{People: f.people, Blocks: f.blocks, Signer: signer, Audit: f.audit}, Options{WebURL: "http://web.test/"})
	return f
}

func (f *fixture) add(p domain.Person) domain.Person {
	f.people.people = append(f.people.people, p)
	return p
}

// noValueInMeta fails when any meta value is the typed string.
func noValueInMeta(t *testing.T, events []AuditEvent, typed string) {
	t.Helper()
	for _, e := range events {
		for k, v := range e.Meta {
			if s, ok := v.(string); ok && strings.Contains(strings.ToLower(s), strings.ToLower(typed)) {
				t.Errorf("meta %q holds the typed value %q", k, typed)
			}
		}
	}
}

var errBoom = errors.New("boom")
