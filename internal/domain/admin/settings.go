package admin

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strings"
)

// ValueKind is the type of a setting.
type ValueKind string

// The types of a setting.
const (
	KindInt   ValueKind = "int"
	KindFloat ValueKind = "float"
)

// Definition is a setting a super admin may change: its key, type, the range of values it accepts and the value it
// has until somebody overrides it.
type Definition struct {
	Key      string
	Group    string
	Kind     ValueKind
	Min, Max float64
	Default  float64
}

// Definitions lists the settings that may be changed, sorted by key. Nothing else is ever stored.
func Definitions() []Definition {
	defs := []Definition{
		{"uploads.room_media.max_files", "uploads", KindInt, 1, 50, 10},
		{"uploads.room_media.max_size_kb", "uploads", KindInt, 100, 10240, 1024},
		{"uploads.room_media.max_dimension", "uploads", KindInt, 512, 8000, 2000},
		{"uploads.payment_proof.max_files", "uploads", KindInt, 1, 10, 3},
		{"uploads.payment_proof.max_size_kb", "uploads", KindInt, 100, 10240, 1024},
		{"uploads.payment_proof.max_dimension", "uploads", KindInt, 512, 8000, 2000},
		{"uploads.client_quality", "uploads", KindFloat, 0.5, 0.95, 0.85},
		{"uploads.avatar.max_size_kb", "uploads", KindInt, 100, 4096, 512},
		{"uploads.avatar.max_dimension", "uploads", KindInt, 128, 2048, 512},
		{"invoices.due_days", "invoices", KindInt, 1, 90, 7},
		{"invoices.reminder_days", "invoices", KindInt, 1, 30, 3},
	}
	slices.SortFunc(defs, func(a, b Definition) int { return strings.Compare(a.Key, b.Key) })
	return defs
}

// FindDefinition returns the definition of a key.
func FindDefinition(key string) (Definition, bool) {
	for _, d := range Definitions() {
		if d.Key == key {
			return d, true
		}
	}
	return Definition{}, false
}

// CheckValue checks that a value fits the setting: a number inside the range, a whole number for an int setting.
func (d Definition) CheckValue(value float64) (rule string, params map[string]string) {
	switch {
	case math.IsNaN(value) || math.IsInf(value, 0):
		return ruleInteger, nil
	case d.Kind == KindInt && value != math.Trunc(value):
		return ruleInteger, nil
	case value < d.Min:
		return ruleMinNumber, map[string]string{"min": formatNumber(d.Min)}
	case value > d.Max:
		return ruleMaxNumber, map[string]string{"max": formatNumber(d.Max)}
	}
	return "", nil
}

func formatNumber(f float64) string { return fmt.Sprintf("%g", f) }

// SettingValue is the effective value of one setting.
type SettingValue struct {
	Definition Definition
	Value      float64
	Overridden bool
}

// Version is a short fingerprint of every override: it changes whenever a setting is changed or reset, so a client
// that remembers it knows when to read the settings again.
func Version(overrides map[string]float64) string {
	keys := make([]string, 0, len(overrides))
	for k := range overrides {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	var b strings.Builder
	for _, k := range keys {
		raw, _ := json.Marshal(overrides[k])
		b.WriteString(k + "=" + string(raw) + "\n")
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])[:12]
}

// SettingChange is one entry of a settings update: a nil Value resets the setting to its default.
type SettingChange struct {
	Key   string
	Value *float64
}
