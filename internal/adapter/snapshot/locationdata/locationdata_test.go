package locationdata

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"strings"
	"testing"
)

func TestEmbeddedSnapshotIsUsable(t *testing.T) {
	snap, err := Source{}.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Provinces) != 34 || len(snap.Wards) < 3000 {
		t.Errorf("unexpected snapshot size: %d provinces, %d wards", len(snap.Provinces), len(snap.Wards))
	}
	codes := map[int32]bool{}
	for _, w := range snap.Wards {
		if codes[w.Code] {
			t.Fatalf("ward code %d appears twice", w.Code)
		}
		codes[w.Code] = true
	}
}

func gzipped(t *testing.T, v any) *bytes.Reader {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if err := json.NewEncoder(zw).Encode(v); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return bytes.NewReader(buf.Bytes())
}

func TestDecodeRejectsBrokenData(t *testing.T) {
	good := file{
		Provinces: []fileProvince{{Code: 1, Name: "Hà Nội", Slug: "ha_noi", AreaPhoneCode: 24}},
		Wards:     []fileWard{{Code: 10, ProvinceCode: 1, Name: "Phường A", Slug: "phuong_a"}},
	}
	snap, err := decode(gzipped(t, good))
	if err != nil {
		t.Fatalf("good snapshot refused: %v", err)
	}
	if snap.Provinces[0].AreaPhoneCode != 24 || snap.Wards[0].ProvinceCode != 1 {
		t.Errorf("fields not carried over: %+v", snap)
	}

	orphan := good
	orphan.Wards = []fileWard{{Code: 10, ProvinceCode: 99, Name: "Phường A", Slug: "phuong_a"}}
	nameless := good
	nameless.Provinces = []fileProvince{{Code: 1, Slug: "ha_noi"}}
	for name, bad := range map[string]file{"orphan ward": orphan, "province without name": nameless, "empty": {}} {
		if _, err := decode(gzipped(t, bad)); err == nil {
			t.Errorf("%s must be refused", name)
		}
	}
	if _, err := decode(strings.NewReader("not gzip")); err == nil {
		t.Error("data that is not gzip must be refused")
	}
}
