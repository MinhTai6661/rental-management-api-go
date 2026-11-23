// Package location serves the provinces and wards of Vietnam. The data is a snapshot embedded in the binary
// (made by tools/fetchlocations) and loaded into the database by `rental-management seed`.
package location

import (
	"compress/gzip"
	"embed"
	"encoding/json"
	"fmt"
	"io"
)

//go:embed data/locations.json.gz
var dataFS embed.FS

// batchSize is how many wards one INSERT carries.
const batchSize = 1000

// Snapshot is the content of data/locations.json.gz.
type Snapshot struct {
	Provinces []SnapshotProvince `json:"provinces"`
	Wards     []SnapshotWard     `json:"wards"`
}

// SnapshotProvince is one province of the snapshot.
type SnapshotProvince struct {
	Code                int    `json:"code"`
	Name                string `json:"name"`
	AdministrativeLevel string `json:"administrative_level"`
	Slug                string `json:"slug"`
	AreaPhoneCode       int    `json:"area_phone_code"`
}

// SnapshotWard is one ward of the snapshot.
type SnapshotWard struct {
	Code                int    `json:"code"`
	ProvinceCode        int    `json:"province_code"`
	Name                string `json:"name"`
	AdministrativeLevel string `json:"administrative_level"`
	Slug                string `json:"slug"`
}

// LoadSnapshot reads the embedded snapshot.
func LoadSnapshot() (Snapshot, error) {
	f, err := dataFS.Open("data/locations.json.gz")
	if err != nil {
		return Snapshot{}, fmt.Errorf("open locations snapshot: %w", err)
	}
	defer func() { _ = f.Close() }()
	return decodeSnapshot(f)
}

// decodeSnapshot reads a gzipped JSON snapshot and checks that it is usable.
func decodeSnapshot(r io.Reader) (Snapshot, error) {
	zr, err := gzip.NewReader(r)
	if err != nil {
		return Snapshot{}, fmt.Errorf("unzip locations snapshot: %w", err)
	}
	defer func() { _ = zr.Close() }()

	var snap Snapshot
	if err := json.NewDecoder(zr).Decode(&snap); err != nil {
		return Snapshot{}, fmt.Errorf("decode locations snapshot: %w", err)
	}
	if err := snap.validate(); err != nil {
		return Snapshot{}, err
	}
	return snap, nil
}

// validate checks what the database would otherwise reject half way through a seed: empty data, missing names or
// slugs, and wards of a province that is not in the snapshot.
func (s Snapshot) validate() error {
	if len(s.Provinces) == 0 || len(s.Wards) == 0 {
		return fmt.Errorf("locations snapshot is empty")
	}
	provinces := make(map[int]bool, len(s.Provinces))
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

// wardBatches splits the wards into slices of at most size, keeping their order.
func wardBatches(wards []SnapshotWard, size int) [][]SnapshotWard {
	var batches [][]SnapshotWard
	for len(wards) > size {
		batches = append(batches, wards[:size])
		wards = wards[size:]
	}
	if len(wards) > 0 {
		batches = append(batches, wards)
	}
	return batches
}
