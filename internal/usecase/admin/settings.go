package admin

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/admin"
)

// Settings is the super admin's control of the system settings.
type Settings struct{ *core }

// NewSettings builds the use cases.
func NewSettings(d Deps, o Options) *Settings { return &Settings{newCore(d, o)} }

// SettingList is every setting with its effective value, and the version of the overrides.
type SettingList struct {
	Items   []domain.SettingValue
	Version string
}

// List returns every setting that may be changed with the value in force.
func (s *Settings) List(ctx context.Context) (SettingList, error) {
	overrides, err := s.settings.Overrides(ctx)
	if err != nil {
		return SettingList{}, err
	}
	defs := domain.Definitions()
	items := make([]domain.SettingValue, len(defs))
	for i, d := range defs {
		value, overridden := overrides[d.Key]
		if !overridden {
			value = s.defaultOf(d)
		}
		items[i] = domain.SettingValue{Definition: d, Value: value, Overridden: overridden}
	}
	return SettingList{Items: items, Version: domain.Version(overrides)}, nil
}

// Update applies the changes: a value sets the setting, no value puts it back to its default. A key that is not
// listed or a value outside the range refuses the whole request; a value that does not differ is left alone.
func (s *Settings) Update(ctx context.Context, actor Actor, changes []domain.SettingChange) (SettingList, error) {
	var bad []domain.Violation
	for i, c := range changes {
		def, ok := domain.FindDefinition(c.Key)
		if !ok {
			bad = append(bad, domain.Violation{Name: fmt.Sprintf("settings.%d.key", i), Rule: domain.RuleOneOf})
			continue
		}
		if c.Value == nil {
			continue
		}
		if rule, params := def.CheckValue(*c.Value); rule != "" {
			bad = append(bad, domain.Violation{Name: fmt.Sprintf("settings.%d.value", i), Rule: rule, Params: params})
		}
	}
	if len(bad) > 0 {
		return SettingList{}, &domain.ValidationError{Violations: bad}
	}
	err := s.tx.WithinTx(ctx, func(ctx context.Context) error {
		overrides, err := s.settings.Overrides(ctx)
		if err != nil {
			return err
		}
		for _, c := range changes {
			if err := s.applyChange(ctx, actor, overrides, c); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return SettingList{}, err
	}
	return s.List(ctx)
}

func (s *Settings) applyChange(ctx context.Context, actor Actor, overrides map[string]float64, c domain.SettingChange) error {
	def, _ := domain.FindDefinition(c.Key)
	before, wasSet := overrides[c.Key]
	if !wasSet {
		before = s.defaultOf(def)
	}
	var after float64
	switch {
	case c.Value != nil:
		after = *c.Value
		if wasSet && before == after {
			return nil
		}
		if err := s.settings.Set(ctx, c.Key, after, actor.ID, s.clock.Now()); err != nil {
			return err
		}
		overrides[c.Key] = after
	case wasSet:
		after = s.defaultOf(def)
		if err := s.settings.Reset(ctx, c.Key); err != nil {
			return err
		}
		delete(overrides, c.Key)
	default:
		return nil
	}
	return s.audit.Record(ctx, AuditEvent{
		Action: AuditSettingChanged, ActorID: actor.ID,
		Meta: map[string]any{"key": c.Key, "from": before, "to": after},
	})
}

// defaultOf is the value of a setting nobody has overridden.
func (c *core) defaultOf(d domain.Definition) float64 {
	if v, ok := c.opts.Defaults[d.Key]; ok {
		return v
	}
	return d.Default
}

// DefaultOf is the value of a setting nobody has overridden.
func (s *Settings) DefaultOf(d domain.Definition) float64 { return s.defaultOf(d) }

// SettingHistoryItem is one change of a setting: which, from what value to what value, by whom and when.
type SettingHistoryItem struct {
	Key       string
	From, To  *float64
	At        time.Time
	ActorID   *uuid.UUID
	ActorName string
}

// SettingHistoryPage is a page of the history and whether another follows.
type SettingHistoryPage struct {
	Items   []SettingHistoryItem
	HasMore bool
}

// History lists the changes of the settings, newest first. It reads the audit trail, so the history is as long as the
// trail keeps the entries.
func (s *Settings) History(ctx context.Context, page, perPage int) (SettingHistoryPage, error) {
	if s.history == nil {
		return SettingHistoryPage{Items: []SettingHistoryItem{}}, nil
	}
	changes, more, err := s.history.Changes(ctx, page, perPage)
	if err != nil {
		return SettingHistoryPage{}, err
	}
	items := make([]SettingHistoryItem, 0, len(changes))
	for _, c := range changes {
		key, _ := c.Meta["key"].(string)
		items = append(items, SettingHistoryItem{
			Key: key, From: number(c.Meta["from"]), To: number(c.Meta["to"]), At: c.At, ActorID: c.ActorID, ActorName: c.ActorName,
		})
	}
	return SettingHistoryPage{Items: items, HasMore: more}, nil
}

// number reads a JSON number back out of an audit payload.
func number(v any) *float64 {
	f, ok := v.(float64)
	if !ok {
		return nil
	}
	return &f
}
