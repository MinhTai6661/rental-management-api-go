package location

import "testing"

func TestSnapshotValidate(t *testing.T) {
	province := Province{Code: 1, Name: "Hà Nội", Slug: "ha_noi"}
	ward := Ward{Code: 10, ProvinceCode: 1, Name: "Phường A", Slug: "phuong_a"}
	cases := map[string]struct {
		snap Snapshot
		ok   bool
	}{
		"good":             {Snapshot{[]Province{province}, []Ward{ward}}, true},
		"empty":            {Snapshot{}, false},
		"no wards":         {Snapshot{Provinces: []Province{province}}, false},
		"province no name": {Snapshot{[]Province{{Code: 1, Slug: "ha_noi"}}, []Ward{ward}}, false},
		"province no slug": {Snapshot{[]Province{{Code: 1, Name: "Hà Nội"}}, []Ward{ward}}, false},
		"ward no name":     {Snapshot{[]Province{province}, []Ward{{Code: 10, ProvinceCode: 1, Slug: "x"}}}, false},
		"ward no slug":     {Snapshot{[]Province{province}, []Ward{{Code: 10, ProvinceCode: 1, Name: "X"}}}, false},
		"orphan ward":      {Snapshot{[]Province{province}, []Ward{{Code: 10, ProvinceCode: 99, Name: "A", Slug: "a"}}}, false},
	}
	for name, tc := range cases {
		if err := tc.snap.Validate(); (err == nil) != tc.ok {
			t.Errorf("%s: err = %v, want ok = %v", name, err, tc.ok)
		}
	}
}
