package payment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/payment"
	"rental-management/internal/domain/shared"
)

// Methods is the use cases of how a landlord wants to be paid: one entry for each payment method the platform offers,
// with the settings the landlord saved for it. The settings (where the money goes) are kept encrypted and never written
// to a log; the audit trail holds only masked account numbers.
type Methods struct{ *core }

// NewMethods builds the payment method use cases.
func NewMethods(d Deps, o Options) *Methods { return &Methods{newCore(d, o)} }

// openConfig decrypts the settings of a saved method.
func (c *core) openConfig(m domain.StoredMethod) (domain.Config, error) {
	plain, err := c.sealer.Open(m.ConfigSealed)
	if err != nil {
		return nil, err
	}
	var cfg domain.Config
	if err := json.Unmarshal([]byte(plain), &cfg); err != nil {
		return nil, fmt.Errorf("read the settings of a payment method: %w", err)
	}
	return cfg, nil
}

// List returns every method the platform offers, in order, with what the landlord saved for it (no settings and not
// switched on for a method they never saved). A method whose settings cannot be decrypted (the key was changed) shows
// as not saved, so the landlord sets it up again.
func (m *Methods) List(ctx context.Context, landlordID uuid.UUID) ([]domain.MethodSetting, error) {
	saved, err := m.methods.ListOfLandlord(ctx, landlordID)
	if err != nil {
		return nil, err
	}
	byCode := make(map[string]domain.StoredMethod, len(saved))
	for _, s := range saved {
		byCode[s.Method] = s
	}
	out := make([]domain.MethodSetting, 0, len(m.drivers.Codes()))
	for _, code := range m.drivers.Codes() {
		setting := domain.MethodSetting{Method: code}
		if s, ok := byCode[code]; ok {
			cfg, err := m.openConfig(s)
			if err != nil {
				m.log.Warn("open the settings of a payment method", "error", err, "method", code, "landlord_id", landlordID)
			} else {
				setting.Config, setting.IsActive = cfg, s.IsActive
			}
		}
		out = append(out, setting)
	}
	return out, nil
}

// Save sets up one method: the driver checks the settings and keeps only the fields it knows, they are encrypted, and
// the method is switched on unless active says otherwise (nil: on). A broken rule is a domain.Violations; a code no
// driver has is domain.ErrUnknownMethod. Where the money goes is the most sensitive setting a landlord has, so every
// change is on record (with masked numbers).
func (m *Methods) Save(ctx context.Context, landlordID uuid.UUID, code string, cfg domain.Config, active *bool) (domain.MethodSetting, error) {
	drv, err := m.drivers.Get(code)
	if err != nil {
		return domain.MethodSetting{}, err
	}
	clean, err := drv.CheckConfig(cfg)
	if err != nil {
		return domain.MethodSetting{}, err
	}
	raw, err := json.Marshal(clean)
	if err != nil {
		return domain.MethodSetting{}, fmt.Errorf("write the settings of a payment method: %w", err)
	}
	sealed, err := m.sealer.Seal(string(raw))
	if err != nil {
		return domain.MethodSetting{}, err
	}
	on := active == nil || *active
	err = m.tx.WithinTx(ctx, func(ctx context.Context) error {
		var before domain.Config
		beforeActive := false
		old, err := m.methods.Get(ctx, landlordID, code)
		switch {
		case errors.Is(err, shared.ErrNotFound):
		case err != nil:
			return err
		default:
			if before, err = m.openConfig(old); err != nil {
				before = nil // unreadable: the change is shown as from nothing
			}
			beforeActive = old.IsActive
		}
		created, err := m.methods.Upsert(ctx, landlordID, domain.StoredMethod{Method: code, ConfigSealed: sealed, IsActive: on})
		if err != nil {
			return err
		}
		return m.audit.Record(ctx, AuditEvent{
			Action: AuditMethodSaved, ActorID: landlordID, OwnerID: landlordID,
			Meta: map[string]any{"method": code, "created": created, "changes": configChanges(before, beforeActive, clean, on)},
		})
	})
	if err != nil {
		return domain.MethodSetting{}, err
	}
	return domain.MethodSetting{Method: code, Config: clean, IsActive: on}, nil
}

// configChanges lists what changed between two versions of a method's settings, field by field, with the numbers a
// person could pay into masked.
func configChanges(before domain.Config, beforeActive bool, after domain.Config, afterActive bool) map[string]map[string]any {
	changes := map[string]map[string]any{}
	if beforeActive != afterActive {
		changes["is_active"] = map[string]any{"from": beforeActive, "to": afterActive}
	}
	fields := slices.Collect(maps.Keys(after))
	for f := range before {
		if !slices.Contains(fields, f) {
			fields = append(fields, f)
		}
	}
	for _, f := range fields {
		if before[f] == after[f] {
			continue
		}
		changes[f] = map[string]any{"from": mask(f, before[f]), "to": mask(f, after[f])}
	}
	return changes
}

// mask hides the account number and the phone of a method (where money goes) down to their last four characters.
func mask(field, value string) string {
	if field == domain.ConfigAccountNumber || field == domain.ConfigPhone {
		return domain.MaskAccount(value)
	}
	return value
}

// Remove deletes a saved method (nothing happens when the landlord never saved it). A code no driver has is
// domain.ErrUnknownMethod.
func (m *Methods) Remove(ctx context.Context, landlordID uuid.UUID, code string) error {
	if _, err := m.drivers.Get(code); err != nil {
		return err
	}
	return m.tx.WithinTx(ctx, func(ctx context.Context) error {
		removed, err := m.methods.Delete(ctx, landlordID, code)
		if err != nil || !removed {
			return err
		}
		return m.audit.Record(ctx, AuditEvent{
			Action: AuditMethodRemoved, ActorID: landlordID, OwnerID: landlordID, Meta: map[string]any{"method": code},
		})
	})
}
