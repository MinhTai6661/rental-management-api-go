// Command fetchlocations downloads the Vietnamese provinces and wards once and writes them, gzipped, to
// internal/adapter/snapshot/locationdata/locations.json.gz. The file is committed and embedded in the binary, so seeding never
// needs the network. Run it only when the administrative divisions change:
//
//	go run ./tools/fetchlocations
package main

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"sort"
	"time"
)

const sourceURL = "https://provinces.open-api.vn/api/v2"

// province and ward mirror the snapshot format read by internal/adapter/snapshot/locationdata.
type province struct {
	Code                int    `json:"code"`
	Name                string `json:"name"`
	AdministrativeLevel string `json:"administrative_level"`
	Slug                string `json:"slug"`
	AreaPhoneCode       *int   `json:"area_phone_code"`
}

type ward struct {
	Code                int    `json:"code"`
	ProvinceCode        int    `json:"province_code"`
	Name                string `json:"name"`
	AdministrativeLevel string `json:"administrative_level"`
	Slug                string `json:"slug"`
}

type snapshot struct {
	Source    string     `json:"source"`
	FetchedAt time.Time  `json:"fetched_at"`
	Provinces []province `json:"provinces"`
	Wards     []ward     `json:"wards"`
}

// sourceRow is one row of the source API; the two endpoints share most fields.
type sourceRow struct {
	Code         int    `json:"code"`
	Name         string `json:"name"`
	DivisionType string `json:"division_type"`
	Codename     string `json:"codename"`
	PhoneCode    *int   `json:"phone_code"`
	ProvinceCode int    `json:"province_code"`
}

func main() {
	out := flag.String("out", "internal/adapter/snapshot/locationdata/locations.json.gz", "output file")
	flag.Parse()
	if err := run(*out); err != nil {
		fmt.Fprintln(os.Stderr, "fetchlocations:", err)
		os.Exit(1)
	}
}

func run(out string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	provinces, err := fetch(ctx, "/p/")
	if err != nil {
		return err
	}
	wards, err := fetch(ctx, "/w/")
	if err != nil {
		return err
	}

	snap := snapshot{Source: sourceURL, FetchedAt: time.Now().UTC().Truncate(time.Second)}
	for _, p := range provinces {
		snap.Provinces = append(snap.Provinces, province{
			Code: p.Code, Name: p.Name, AdministrativeLevel: p.DivisionType, Slug: p.Codename, AreaPhoneCode: p.PhoneCode,
		})
	}
	for _, w := range wards {
		snap.Wards = append(snap.Wards, ward{
			Code: w.Code, ProvinceCode: w.ProvinceCode, Name: w.Name, AdministrativeLevel: w.DivisionType, Slug: w.Codename,
		})
	}
	// A stable order keeps the committed file's diff small when the source changes.
	sort.Slice(snap.Provinces, func(i, j int) bool { return snap.Provinces[i].Code < snap.Provinces[j].Code })
	sort.Slice(snap.Wards, func(i, j int) bool { return snap.Wards[i].Code < snap.Wards[j].Code })

	f, err := os.Create(out) //nolint:gosec // the path is a command-line flag of a developer tool
	if err != nil {
		return err
	}
	zw, err := gzip.NewWriterLevel(f, gzip.BestCompression)
	if err != nil {
		_ = f.Close()
		return err
	}
	if err := json.NewEncoder(zw).Encode(snap); err != nil {
		_ = f.Close()
		return err
	}
	if err := zw.Close(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	fmt.Printf("wrote %s: %d provinces, %d wards\n", out, len(snap.Provinces), len(snap.Wards))
	return nil
}

func fetch(ctx context.Context, path string) ([]sourceRow, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL+path, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("get %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("get %s: status %d", path, resp.StatusCode)
	}
	var rows []sourceRow
	if err := json.NewDecoder(resp.Body).Decode(&rows); err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}
	return rows, nil
}
