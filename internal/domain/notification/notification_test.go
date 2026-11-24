package notification

import "testing"

func TestClampLatest(t *testing.T) {
	cases := map[int]int{-5: DefaultLatest, 0: DefaultLatest, 1: 1, 8: 8, 20: 20, 21: MaxLatest, 50: MaxLatest}
	for in, want := range cases {
		if got := ClampLatest(in); got != want {
			t.Errorf("ClampLatest(%d) = %d, want %d", in, got, want)
		}
	}
}

func TestParseEnums(t *testing.T) {
	if s, ok := ParseDeleteScope("read"); !ok || s != DeleteRead {
		t.Error("read scope")
	}
	if _, ok := ParseDeleteScope("some"); ok {
		t.Error("unknown scope accepted")
	}
	if b, ok := ParseBulkAction("delete"); !ok || b != BulkDelete {
		t.Error("delete action")
	}
	if _, ok := ParseBulkAction(""); ok {
		t.Error("empty action accepted")
	}
}
