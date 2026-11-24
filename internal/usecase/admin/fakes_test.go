package admin

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/admin"
	"rental-management/internal/domain/shared"
)

// world is an in-memory store behind every port, so a test sets the scene and reads what happened. Ports whose
// methods share a name (Get, Lock, List, Insert...) are small wrapper types over the same world.
type world struct {
	now      time.Time
	accounts map[uuid.UUID]*domain.Account
	apps     map[uuid.UUID]*domain.Application
	profiles map[uuid.UUID]domain.Profile
	impact   domain.Impact
	settings map[string]float64
	docs     []domain.Document
	accepted map[uuid.UUID]*time.Time
	facts    domain.ErasureFacts
	hash     *string
	password string
	// duplicateVersions makes the next n document inserts fail as a version taken.
	duplicateVersions int

	// what happened
	notes    []Notification
	events   []AuditEvent
	firsts   []FirstBuilding
	revoked  []string
	deleted  []uuid.UUID
	erased   []uuid.UUID
	scrubbed []uuid.UUID
}

func newWorld() *world {
	return &world{
		now: time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC), accounts: map[uuid.UUID]*domain.Account{},
		apps: map[uuid.UUID]*domain.Application{}, profiles: map[uuid.UUID]domain.Profile{}, settings: map[string]float64{},
		accepted: map[uuid.UUID]*time.Time{},
	}
}

func (w *world) account(roles ...string) *domain.Account {
	id := uuid.Must(uuid.NewV7())
	email := id.String()[:8] + "@x.test"
	a := &domain.Account{ID: id, FullName: "User " + id.String()[:4], Email: &email, Status: "active", Roles: roles}
	w.accounts[id] = a
	return a
}

func (w *world) deps() Deps {
	return Deps{
		Tx: w, Users: users{w}, Applications: apps{w}, Profiles: profiles{w}, Settings: w, Legal: legal{w}, Personal: w,
		Buildings: w, Hasher: w, Revocations: revocations{w}, Notifier: w, Audit: w, Clock: w,
	}
}

func (w *world) actions() []string {
	out := make([]string, len(w.events))
	for i, e := range w.events {
		out[i] = e.Action
	}
	return out
}

func (w *world) kinds() []string {
	out := make([]string, len(w.notes))
	for i, n := range w.notes {
		out[i] = n.Kind
	}
	return out
}

// TxManager, Clock, Notifier, Auditor, FirstBuildings, PasswordVerifier
func (w *world) WithinTx(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) }
func (w *world) Now() time.Time                                                     { return w.now }
func (w *world) Notify(_ context.Context, n Notification) error {
	w.notes = append(w.notes, n)
	return nil
}
func (w *world) Record(_ context.Context, e AuditEvent) error {
	w.events = append(w.events, e)
	return nil
}
func (w *world) ScrubPersonalData(_ context.Context, id uuid.UUID) error {
	w.scrubbed = append(w.scrubbed, id)
	return nil
}
func (w *world) EnsureFirstBuilding(_ context.Context, _ uuid.UUID, f FirstBuilding) error {
	w.firsts = append(w.firsts, f)
	return nil
}
func (w *world) Verify(password, _ string) (bool, error) { return password == w.password, nil }

// SettingRepository and PersonalDataRepository
func (w *world) Overrides(context.Context) (map[string]float64, error) {
	out := make(map[string]float64, len(w.settings))
	for k, v := range w.settings {
		out[k] = v
	}
	return out, nil
}
func (w *world) Set(_ context.Context, k string, v float64, _ uuid.UUID, _ time.Time) error {
	w.settings[k] = v
	return nil
}
func (w *world) Reset(_ context.Context, k string) error { delete(w.settings, k); return nil }
func (w *world) Facts(context.Context, uuid.UUID) (domain.ErasureFacts, error) {
	return w.facts, nil
}
func (w *world) Export(_ context.Context, id uuid.UUID) (domain.Export, error) {
	return domain.Export{Account: domain.ExportAccount{ID: id.String()}}, nil
}
func (w *world) Erase(_ context.Context, id uuid.UUID, _ time.Time) error {
	w.erased = append(w.erased, id)
	return nil
}

// revocations is the RevocationStore.
type revocations struct{ w *world }

func (r revocations) RevokeSessions(_ context.Context, sids []string, _ time.Duration) error {
	r.w.revoked = append(r.w.revoked, sids...)
	return nil
}

// users is the UserRepository.
type users struct{ w *world }

func (u users) List(_ context.Context, f domain.UserFilter, limit, offset int) ([]domain.Account, int, error) {
	var all []domain.Account
	for _, a := range u.w.accounts {
		if a.ID != f.ViewerID && (f.IncludeStaff || !domain.HasStaffRole(a.Roles)) {
			all = append(all, *a)
		}
	}
	slices.SortFunc(all, func(a, b domain.Account) int { return strings.Compare(a.ID.String(), b.ID.String()) })
	if offset > len(all) {
		offset = len(all)
	}
	return all[offset:min(offset+limit, len(all))], len(all), nil
}
func (u users) Lock(ctx context.Context, id uuid.UUID) (domain.Account, error) { return u.Get(ctx, id) }
func (u users) Get(_ context.Context, id uuid.UUID) (domain.Account, error) {
	a, ok := u.w.accounts[id]
	if !ok {
		return domain.Account{}, shared.ErrNotFound
	}
	return *a, nil
}
func (u users) SoftDelete(_ context.Context, id uuid.UUID, _ time.Time) error {
	u.w.deleted = append(u.w.deleted, id)
	return nil
}
func (u users) ReplaceRoles(_ context.Context, id uuid.UUID, roles []string, _ time.Time) error {
	u.w.accounts[id].Roles = roles
	return nil
}
func (u users) AddRole(_ context.Context, id uuid.UUID, role string, _ time.Time) error {
	if !slices.Contains(u.w.accounts[id].Roles, role) {
		u.w.accounts[id].Roles = append(u.w.accounts[id].Roles, role)
	}
	return nil
}
func (u users) SetLastActiveRole(_ context.Context, id uuid.UUID, role *string, _ time.Time) error {
	u.w.accounts[id].LastActiveRole = role
	return nil
}

// sessionOfEveryone is the one session every account has in the tests.
var sessionOfEveryone = uuid.MustParse("00000000-0000-7000-8000-0000000000aa")

func (u users) RevokeSessions(context.Context, uuid.UUID, string, time.Time) ([]uuid.UUID, error) {
	return []uuid.UUID{sessionOfEveryone}, nil
}
func (u users) PasswordHash(context.Context, uuid.UUID) (*string, error) { return u.w.hash, nil }

// apps is the ApplicationRepository.
type apps struct{ w *world }

func (a apps) open(user uuid.UUID) *domain.Application {
	for _, app := range a.w.apps {
		if app.UserID == user && app.Status == domain.ApplicationPending {
			return app
		}
	}
	return nil
}
func (a apps) Insert(_ context.Context, app domain.Application) error {
	a.w.apps[app.ID] = &app
	return nil
}
func (a apps) Get(_ context.Context, id uuid.UUID) (domain.Application, error) {
	app, ok := a.w.apps[id]
	if !ok {
		return domain.Application{}, shared.ErrNotFound
	}
	return *app, nil
}
func (a apps) Lock(ctx context.Context, id uuid.UUID) (domain.Application, error) {
	return a.Get(ctx, id)
}
func (a apps) LatestOfUser(_ context.Context, u uuid.UUID) (domain.Application, error) {
	for _, app := range a.w.apps {
		if app.UserID == u {
			return *app, nil
		}
	}
	return domain.Application{}, shared.ErrNotFound
}
func (a apps) HasOpen(_ context.Context, u uuid.UUID) (bool, error) { return a.open(u) != nil, nil }
func (a apps) DeleteOpen(_ context.Context, u uuid.UUID) (bool, error) {
	if app := a.open(u); app != nil {
		delete(a.w.apps, app.ID)
		return true, nil
	}
	return false, nil
}
func (a apps) List(_ context.Context, q domain.ApplicationQuery, limit, offset int) ([]domain.Application, int, error) {
	var all []domain.Application
	for _, app := range a.w.apps {
		if app.Status == q.Status {
			all = append(all, *app)
		}
	}
	return all[min(offset, len(all)):min(offset+limit, len(all))], len(all), nil
}
func (a apps) CountsByStatus(context.Context) (map[domain.ApplicationStatus]int, error) {
	out := map[domain.ApplicationStatus]int{}
	for _, app := range a.w.apps {
		out[app.Status]++
	}
	return out, nil
}
func (a apps) History(context.Context, uuid.UUID, uuid.UUID) ([]domain.Application, error) {
	return nil, nil
}
func (a apps) Review(_ context.Context, id uuid.UUID, st domain.ApplicationStatus, by uuid.UUID, note string, now time.Time) error {
	app := a.w.apps[id]
	app.Status, app.ReviewedByID, app.ReviewNote, app.ReviewedAt = st, &by, &note, &now
	return nil
}

// profiles is the ProfileRepository.
type profiles struct{ w *world }

func (p profiles) Get(_ context.Context, id uuid.UUID) (domain.Profile, error) {
	profile, ok := p.w.profiles[id]
	if !ok {
		return domain.Profile{}, shared.ErrNotFound
	}
	return profile, nil
}
func (p profiles) Upsert(_ context.Context, id uuid.UUID, in domain.ProfileInput, _ time.Time) error {
	p.w.profiles[id] = domain.Profile{
		UserID: id, BusinessName: in.BusinessName, Phone: in.Phone, WardCode: in.WardCode,
		AddressDetail: in.AddressDetail, Description: in.Description,
	}
	return nil
}
func (p profiles) InsertIfAbsent(ctx context.Context, id uuid.UUID, in domain.ProfileInput, now time.Time) error {
	if _, ok := p.w.profiles[id]; ok {
		return nil
	}
	return p.Upsert(ctx, id, in, now)
}
func (p profiles) Impact(context.Context, uuid.UUID) (domain.Impact, error) { return p.w.impact, nil }

// legal is the LegalRepository.
type legal struct{ w *world }

func (l legal) Insert(_ context.Context, d domain.Document, _ time.Time) error {
	if l.w.duplicateVersions > 0 {
		l.w.duplicateVersions--
		return &shared.ConstraintError{Kind: shared.Unique, Constraint: "legal_documents_kind_version_key"}
	}
	l.w.docs = append(l.w.docs, d)
	return nil
}
func (l legal) Latest(_ context.Context, k domain.LegalKind) (domain.Document, error) {
	for i := len(l.w.docs) - 1; i >= 0; i-- {
		if l.w.docs[i].Kind == k {
			return l.w.docs[i], nil
		}
	}
	return domain.Document{}, shared.ErrNotFound
}
func (l legal) LatestVersion(context.Context) (string, error) {
	if len(l.w.docs) == 0 {
		return "", shared.ErrNotFound
	}
	return l.w.docs[len(l.w.docs)-1].Version, nil
}
func (l legal) Get(_ context.Context, k domain.LegalKind, id uuid.UUID) (domain.Document, error) {
	for _, d := range l.w.docs {
		if d.ID == id && d.Kind == k {
			return d, nil
		}
	}
	return domain.Document{}, shared.ErrNotFound
}
func (l legal) List(_ context.Context, k domain.LegalKind, limit, offset int) ([]domain.Document, int, error) {
	var all []domain.Document
	for _, d := range l.w.docs {
		if d.Kind == k {
			all = append(all, d)
		}
	}
	return all[min(offset, len(all)):min(offset+limit, len(all))], len(all), nil
}
func (l legal) After(_ context.Context, since time.Time, limit int) ([]domain.Document, error) {
	var out []domain.Document
	for i := len(l.w.docs) - 1; i >= 0 && len(out) < limit; i-- {
		if l.w.docs[i].PublishedAt.After(since) {
			out = append(out, l.w.docs[i])
		}
	}
	return out, nil
}
func (l legal) NewestPerKind(ctx context.Context) ([]domain.Document, error) {
	var out []domain.Document
	for _, k := range []domain.LegalKind{domain.LegalTerms, domain.LegalPrivacy} {
		if d, err := l.Latest(ctx, k); err == nil {
			out = append(out, d)
		}
	}
	return out, nil
}
func (l legal) AcceptedAt(_ context.Context, id uuid.UUID) (*time.Time, error) {
	return l.w.accepted[id], nil
}
func (l legal) SetAccepted(_ context.Context, id uuid.UUID, _ string, now time.Time) error {
	l.w.accepted[id] = &now
	return nil
}
