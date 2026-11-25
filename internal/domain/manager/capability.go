// Package manager holds the pure rules of handing rooms over to managers: what a manager may be given (the
// capabilities, which never change meaning), who can be asked, how an invitation and a hand-over live, and what a
// manager may do on a room (policy.go). It imports nothing but the standard library, uuid and the shared kernels.
package manager

import (
	"errors"
	"slices"
)

// Capability is something a landlord may give a manager of one room on top of the basics (seeing the room and its
// confirmed contracts, writing and sending bills, deleting the drafts they wrote). The codes are stored: a code never
// changes meaning and is never reused, and a code this version does not know is ignored when read.
type Capability string

// The capabilities.
const (
	// PaymentsReview: confirm or reject payments and mark bills paid or unpaid.
	PaymentsReview Capability = "payments.review"
	// ReviewRegistrations: approve or reject the registrations of tenants for the room.
	ReviewRegistrations Capability = "contracts.review_registrations"
	// ContractsPropose: write an offer of the room; the landlord must approve it before it goes out.
	ContractsPropose Capability = "contracts.propose"
	// ContractsCreate: send an offer of the room without approval. Needs ContractsPropose.
	ContractsCreate Capability = "contracts.create"
)

// Risk says how much harm a capability can do in the wrong hands.
type Risk string

// The risks.
const (
	RiskLow    Risk = "low"
	RiskMedium Risk = "medium"
	RiskHigh   Risk = "high"
)

// CapabilityInfo describes one capability for the screens that offer them.
type CapabilityInfo struct {
	Code     Capability
	Group    string
	Risk     Risk
	Requires []Capability
}

var catalogue = []CapabilityInfo{
	{Code: PaymentsReview, Group: "payments", Risk: RiskMedium},
	{Code: ReviewRegistrations, Group: "contracts", Risk: RiskMedium},
	{Code: ContractsPropose, Group: "contracts", Risk: RiskLow},
	{Code: ContractsCreate, Group: "contracts", Risk: RiskHigh, Requires: []Capability{ContractsPropose}},
}

// Catalogue lists every capability.
func Catalogue() []CapabilityInfo {
	out := make([]CapabilityInfo, len(catalogue))
	for i, c := range catalogue {
		c.Requires = slices.Clone(c.Requires)
		out[i] = c
	}
	return out
}

// ErrUnknownCapability is returned for a code that is not in the catalogue.
var ErrUnknownCapability = errors.New("unknown capability")

// ParseCapability turns a code into a Capability.
func ParseCapability(s string) (Capability, error) {
	for _, c := range catalogue {
		if string(c.Code) == s {
			return c.Code, nil
		}
	}
	return "", ErrUnknownCapability
}

// ParseCapabilities turns codes into capabilities, refusing any that is not in the catalogue.
func ParseCapabilities(codes []string) ([]Capability, error) {
	out := make([]Capability, 0, len(codes))
	for _, s := range codes {
		c, err := ParseCapability(s)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

// KnownCapabilities keeps the codes this version knows and drops the rest (what is read from the database).
func KnownCapabilities(codes []string) []Capability {
	out := make([]Capability, 0, len(codes))
	for _, s := range codes {
		if c, err := ParseCapability(s); err == nil {
			out = append(out, c)
		}
	}
	return Normalize(out)
}

func requiresOf(c Capability) []Capability {
	for _, info := range catalogue {
		if info.Code == c {
			return info.Requires
		}
	}
	return nil
}

// Normalize sorts a set of capabilities and removes the duplicates.
func Normalize(in []Capability) []Capability {
	out := slices.Clone(in)
	slices.Sort(out)
	return slices.Compact(out)
}

// Change applies a grant and a revoke to a set: the result is the current set plus the grants (each with what it
// requires), minus the revokes, and any capability that lost what it requires goes too (a chain, so revoking
// ContractsPropose takes ContractsCreate with it). The result is sorted and free of duplicates.
func Change(current, grant, revoke []Capability) []Capability {
	set := map[Capability]bool{}
	for _, c := range current {
		set[c] = true
	}
	for _, c := range grant {
		set[c] = true
		for _, r := range requiresOf(c) {
			set[r] = true
		}
	}
	for _, c := range revoke {
		delete(set, c)
	}
	for changed := true; changed; {
		changed = false
		for c := range set {
			for _, r := range requiresOf(c) {
				if !set[r] {
					delete(set, c)
					changed = true
					break
				}
			}
		}
	}
	out := make([]Capability, 0, len(set))
	for c := range set {
		out = append(out, c)
	}
	slices.Sort(out)
	return out
}

// Set is the capabilities a landlord picks, completed with what they require.
func Set(codes []Capability) []Capability { return Change(nil, codes, nil) }

// Strings turns capabilities into their codes.
func Strings(in []Capability) []string {
	out := make([]string, len(in))
	for i, c := range in {
		out[i] = string(c)
	}
	return out
}

// Has says whether the set holds the capability.
func Has(set []Capability, c Capability) bool { return slices.Contains(set, c) }
