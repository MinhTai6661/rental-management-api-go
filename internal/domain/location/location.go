// Package location is the domain of the administrative divisions of Vietnam: provinces and the wards in them.
package location

import (
	"errors"
	"fmt"
)

// ErrProvinceNotFound: there is no province with the code asked for.
var ErrProvinceNotFound = errors.New("province does not exist")

// Division is a province or a ward as it is listed: a code and a name. One type serves both because a listing
// needs nothing else; the reference data below (Province, Ward) carries what only the seed needs.
type Division struct {
	Code int32
	Name string
}

// Province is a province of the reference data.
type Province struct {
	Code                int32
	Name                string
	AdministrativeLevel string
	Slug                string
	AreaPhoneCode       int16
}

// Ward is a ward of the reference data.
type Ward struct {
	Code                int32
	ProvinceCode        int32
	Name                string
	AdministrativeLevel string
	Slug                string
}

// Snapshot is the reference data a seed loads: every province and every ward.
type Snapshot struct {
	Provinces []Province
	Wards     []Ward
}

// Validate checks what the database would otherwise reject half way through a seed: empty data, missing names or
// slugs, and wards of a province that is not in the snapshot.
func (s Snapshot) Validate() error {
	if len(s.Provinces) == 0 || len(s.Wards) == 0 {
		return errors.New("locations snapshot is empty")
	}
	provinces := make(map[int32]bool, len(s.Provinces))
	for _, p := range s.Provinces {
		if p.Name == "" || p.Slug == "" {
			return fmt.Errorf("province %d has no name or slug", p.Code)
		}
		provinces[p.Code] = true
	}
	for _, w := range s.Wards {
		if w.Name == "" || w.Slug == "" {
			return fmt.Errorf("ward %d has no name or slug", w.Code)
		}
		if !provinces[w.ProvinceCode] {
			return fmt.Errorf("ward %d belongs to unknown province %d", w.Code, w.ProvinceCode)
		}
	}
	return nil
}
