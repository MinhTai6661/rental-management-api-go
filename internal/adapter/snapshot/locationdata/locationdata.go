// Package locationdata is the snapshot of the provinces and wards of Vietnam embedded in the binary (made by
// tools/fetchlocations) and loaded into the database by `rental-management seed`.
package locationdata

import (
	"compress/gzip"
	"embed"
	"encoding/json"
	"fmt"
	"io"

	domain "rental-management/internal/domain/location"
)

//go:embed locations.json.gz
var dataFS embed.FS

const fileName = "locations.json.gz"

// Source reads the embedded snapshot.
type Source struct{}

// Load reads the embedded snapshot and checks that it is usable.
func (Source) Load() (domain.Snapshot, error) {
	f, err := dataFS.Open(fileName)
	if err != nil {
		return domain.Snapshot{}, fmt.Errorf("open locations snapshot: %w", err)
	}
	defer func() { _ = f.Close() }()
	return decode(f)
}

// file is the content of locations.json.gz; the json tags are the format tools/fetchlocations writes.
type file struct {
	Provinces []fileProvince `json:"provinces"`
	Wards     []fileWard     `json:"wards"`
}

type fileProvince struct {
	Code                int32  `json:"code"`
	Name                string `json:"name"`
	AdministrativeLevel string `json:"administrative_level"`
	Slug                string `json:"slug"`
	AreaPhoneCode       int16  `json:"area_phone_code"`
}

type fileWard struct {
	Code                int32  `json:"code"`
	ProvinceCode        int32  `json:"province_code"`
	Name                string `json:"name"`
	AdministrativeLevel string `json:"administrative_level"`
	Slug                string `json:"slug"`
}

// decode reads a gzipped JSON snapshot and checks that it is usable.
func decode(r io.Reader) (domain.Snapshot, error) {
	zr, err := gzip.NewReader(r)
	if err != nil {
		return domain.Snapshot{}, fmt.Errorf("unzip locations snapshot: %w", err)
	}
	defer func() { _ = zr.Close() }()

	var raw file
	if err := json.NewDecoder(zr).Decode(&raw); err != nil {
		return domain.Snapshot{}, fmt.Errorf("decode locations snapshot: %w", err)
	}
	snap := domain.Snapshot{
		Provinces: make([]domain.Province, len(raw.Provinces)),
		Wards:     make([]domain.Ward, len(raw.Wards)),
	}
	for i, p := range raw.Provinces {
		snap.Provinces[i] = domain.Province(p)
	}
	for i, w := range raw.Wards {
		snap.Wards[i] = domain.Ward(w)
	}
	if err := snap.Validate(); err != nil {
		return domain.Snapshot{}, err
	}
	return snap, nil
}
