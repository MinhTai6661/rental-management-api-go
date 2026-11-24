package admin

import (
	"context"
	"errors"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/admin"
	"rental-management/internal/domain/shared"
	"rental-management/pkg/authz"
)

// Applications are the landlord applications: what a tenant sends and what an admin answers.
type Applications struct{ *core }

// NewApplications builds the use cases.
func NewApplications(d Deps, o Options) *Applications { return &Applications{newCore(d, o)} }

// Submit sends the application of a tenant account; it waits for an admin, who is told right away.
func (a *Applications) Submit(ctx context.Context, actor Actor, in domain.ApplicationInput) (domain.Application, error) {
	in, err := in.Normalize()
	if err != nil {
		return domain.Application{}, err
	}
	app := domain.Application{
		ID: uuid.Must(uuid.NewV7()), UserID: actor.ID, BusinessName: in.BusinessName, Phone: in.Phone, WardCode: in.WardCode,
		AddressDetail: in.AddressDetail, Note: in.Note, Status: domain.ApplicationPending,
	}
	err = a.tx.WithinTx(ctx, func(ctx context.Context) error {
		// The row lock makes two applications sent at once wait for each other.
		user, err := a.users.Lock(ctx, actor.ID)
		if err != nil {
			return err
		}
		open, err := a.applications.HasOpen(ctx, actor.ID)
		if err != nil {
			return err
		}
		if err := domain.CheckCanApply(actor.Role, user.Roles, open); err != nil {
			return err
		}
		app.CreatedAt = a.clock.Now()
		app.ApplicantName = user.FullName
		app.ApplicantEmail = user.Email
		if err := a.applications.Insert(ctx, app); err != nil {
			if shared.IsUnique(err) {
				return domain.ErrAlreadyPending
			}
			return err
		}
		if err := a.audit.Record(ctx, AuditEvent{
			Action: AuditApplicationSubmitted, ActorID: actor.ID, Meta: map[string]any{"application_id": app.ID.String()},
		}); err != nil {
			return err
		}
		return a.notifier.Notify(ctx, Notification{
			ToAdmins: true, Kind: NotifyApplicationReceived, URL: "/admin/landlord-applications",
			Params: map[string]string{"applicant": user.FullName, "business": app.BusinessName},
		})
	})
	return app, err
}

// Withdraw takes back the application that still waits; it does nothing when there is none.
func (a *Applications) Withdraw(ctx context.Context, actor Actor) error {
	return a.tx.WithinTx(ctx, func(ctx context.Context) error {
		removed, err := a.applications.DeleteOpen(ctx, actor.ID)
		if err != nil || !removed {
			return err
		}
		return a.audit.Record(ctx, AuditEvent{Action: AuditApplicationWithdrawn, ActorID: actor.ID})
	})
}

// Mine returns the newest application of the account, or nil when it never applied.
func (a *Applications) Mine(ctx context.Context, actor Actor) (*domain.Application, error) {
	app, err := a.applications.LatestOfUser(ctx, actor.ID)
	if errors.Is(err, shared.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &app, nil
}

// ApplicationList is a page of applications and how many there are in each status.
type ApplicationList struct {
	Items  []domain.Application
	Total  int
	Counts map[domain.ApplicationStatus]int
}

// ListInput is what the list of applications is narrowed by, as the client wrote it.
type ListInput struct{ Status, Search, Sort string }

// List returns the applications in a status (the ones waiting by default).
func (a *Applications) List(ctx context.Context, in ListInput, limit, offset int) (ApplicationList, error) {
	q := domain.ApplicationQuery{Status: domain.ApplicationPending, Search: domain.EscapeLike(in.Search)}
	if in.Status != "" {
		st, ok := domain.ParseApplicationStatus(in.Status)
		if !ok {
			return ApplicationList{}, domain.Invalid("status", domain.RuleOneOf, nil)
		}
		q.Status = st
	}
	sort, ok := domain.ParseApplicationSort(in.Sort)
	if !ok {
		return ApplicationList{}, domain.Invalid("sort_by", domain.RuleOneOf, nil)
	}
	q.Sort = sort
	items, total, err := a.applications.List(ctx, q, limit, offset)
	if err != nil {
		return ApplicationList{}, err
	}
	counts, err := a.applications.CountsByStatus(ctx)
	if err != nil {
		return ApplicationList{}, err
	}
	return ApplicationList{Items: items, Total: total, Counts: counts}, nil
}

// Detail is an application with the earlier ones of the same person.
type Detail struct {
	Application domain.Application
	History     []domain.Application
}

// Get returns an application and the other applications of the same person.
func (a *Applications) Get(ctx context.Context, id uuid.UUID) (Detail, error) {
	app, err := a.applications.Get(ctx, id)
	if err != nil {
		return Detail{}, err
	}
	history, err := a.applications.History(ctx, app.UserID, id)
	return Detail{Application: app, History: history}, err
}

// Approve accepts an application: the applicant becomes a landlord with a profile and a first building and is told.
func (a *Applications) Approve(ctx context.Context, actor Actor, id uuid.UUID, note string) error {
	note, err := domain.NormalizeReview(note, false)
	if err != nil {
		return err
	}
	return a.tx.WithinTx(ctx, func(ctx context.Context) error {
		app, err := a.applications.Lock(ctx, id)
		if err != nil {
			return err
		}
		if err := app.CheckReviewable(); err != nil {
			return err
		}
		now := a.clock.Now()
		if err := a.applications.Review(ctx, id, domain.ApplicationApproved, actor.ID, note, now); err != nil {
			return err
		}
		if err := a.users.AddRole(ctx, app.UserID, authz.RoleLandlord, now); err != nil {
			return err
		}
		if err := a.setUpLandlord(ctx, app.UserID, &app); err != nil {
			return err
		}
		if err := a.audit.Record(ctx, AuditEvent{
			Action: AuditApplicationApproved, ActorID: actor.ID, TargetUserID: &app.UserID,
			Meta: map[string]any{"application_id": id.String()},
		}); err != nil {
			return err
		}
		return a.notifier.Notify(ctx, Notification{UserID: app.UserID, Kind: NotifyApplicationApproved, URL: "/become-landlord"})
	})
}

// Reject refuses an application with a reason; the applicant is told and may apply again.
func (a *Applications) Reject(ctx context.Context, actor Actor, id uuid.UUID, note string) error {
	note, err := domain.NormalizeReview(note, true)
	if err != nil {
		return err
	}
	return a.tx.WithinTx(ctx, func(ctx context.Context) error {
		app, err := a.applications.Lock(ctx, id)
		if err != nil {
			return err
		}
		if err := app.CheckReviewable(); err != nil {
			return err
		}
		if err := a.applications.Review(ctx, id, domain.ApplicationRejected, actor.ID, note, a.clock.Now()); err != nil {
			return err
		}
		if err := a.audit.Record(ctx, AuditEvent{
			Action: AuditApplicationRejected, ActorID: actor.ID, TargetUserID: &app.UserID,
			Meta: map[string]any{"application_id": id.String(), "note": note},
		}); err != nil {
			return err
		}
		return a.notifier.Notify(ctx, Notification{
			UserID: app.UserID, Kind: NotifyApplicationRejected, URL: "/become-landlord", Params: map[string]string{"reason": note},
		})
	})
}

// Bulk approves or rejects many applications; each runs on its own and one that cannot be answered is reported while
// the rest still run.
func (a *Applications) Bulk(ctx context.Context, actor Actor, reject bool, ids []uuid.UUID, note string) (domain.BulkResult, error) {
	if err := domain.CheckBulkIDs(len(ids)); err != nil {
		return domain.BulkResult{}, err
	}
	if _, err := domain.NormalizeReview(note, reject); err != nil {
		return domain.BulkResult{}, err
	}
	result := domain.BulkResult{Done: []uuid.UUID{}, Failed: []domain.BulkFailure{}}
	seen := make(map[uuid.UUID]struct{}, len(ids))
	for _, id := range ids {
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		var err error
		if reject {
			err = a.Reject(ctx, actor, id, note)
		} else {
			err = a.Approve(ctx, actor, id, note)
		}
		switch {
		case err == nil:
			result.Done = append(result.Done, id)
		case errors.Is(err, domain.ErrNotReviewable), errors.Is(err, shared.ErrNotFound):
			name := ""
			if app, getErr := a.applications.Get(ctx, id); getErr == nil {
				name = app.BusinessName
			}
			result.Failed = append(result.Failed, domain.BulkFailure{ID: id, Name: name, Reason: err})
		default:
			return result, err
		}
	}
	return result, nil
}

// setUpLandlord gives a new landlord a business profile (from the application, or from their account when none) and, if
// they never had one, a first building named after the business. It does nothing for a building without an address.
func (c *core) setUpLandlord(ctx context.Context, userID uuid.UUID, app *domain.Application) error {
	now := c.clock.Now()
	contact, err := c.users.Get(ctx, userID)
	if err != nil {
		return err
	}
	profile := domain.ProfileInput{
		BusinessName: contact.FullName, Phone: contact.PhoneNumber, WardCode: contact.WardCode, AddressDetail: contact.AddressDetail,
	}
	if app != nil {
		profile = domain.ProfileInput{BusinessName: app.BusinessName, Phone: &app.Phone, WardCode: app.WardCode, AddressDetail: app.AddressDetail}
		if profile.WardCode == nil {
			profile.WardCode = contact.WardCode
		}
		if profile.AddressDetail == nil {
			profile.AddressDetail = contact.AddressDetail
		}
	}
	if err := c.profiles.InsertIfAbsent(ctx, userID, profile, now); err != nil {
		return err
	}
	if profile.AddressDetail == nil || trim(*profile.AddressDetail) == "" {
		return nil
	}
	return c.buildings.EnsureFirstBuilding(ctx, userID, FirstBuilding{
		Name: domain.FirstBuildingName(profile.BusinessName), WardCode: profile.WardCode, AddressDetail: *profile.AddressDetail,
	})
}

// Profiles is the landlord's own business profile.
type Profiles struct{ *core }

// NewProfiles builds the use cases.
func NewProfiles(d Deps, o Options) *Profiles { return &Profiles{newCore(d, o)} }

// Get returns the profile of the landlord, or nil when none was written yet.
func (p *Profiles) Get(ctx context.Context, actor Actor) (*domain.Profile, error) {
	profile, err := p.profiles.Get(ctx, actor.ID)
	if errors.Is(err, shared.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &profile, nil
}

// Put writes the profile (it creates it when the landlord has none) and returns the saved one.
func (p *Profiles) Put(ctx context.Context, actor Actor, in domain.ProfileInput) (domain.Profile, error) {
	in, err := in.Normalize()
	if err != nil {
		return domain.Profile{}, err
	}
	err = p.tx.WithinTx(ctx, func(ctx context.Context) error {
		old, err := p.profiles.Get(ctx, actor.ID)
		if err != nil && !errors.Is(err, shared.ErrNotFound) {
			return err
		}
		changed := old.ChangedFields(in)
		if err := p.profiles.Upsert(ctx, actor.ID, in, p.clock.Now()); err != nil {
			return err
		}
		return p.audit.Record(ctx, AuditEvent{Action: AuditLandlordProfileSaved, ActorID: actor.ID, Meta: map[string]any{"fields": changed}})
	})
	if err != nil {
		return domain.Profile{}, err
	}
	return p.profiles.Get(ctx, actor.ID)
}
