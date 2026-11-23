package locationpg

import (
	"testing"

	domain "rental-management/internal/domain/location"
)

func TestWardBatches(t *testing.T) {
	wards := make([]domain.Ward, 2500)
	got := wardBatches(wards, 1000)
	if len(got) != 3 || len(got[0]) != 1000 || len(got[1]) != 1000 || len(got[2]) != 500 {
		t.Errorf("batch sizes wrong: %d batches", len(got))
	}
	if len(wardBatches(nil, 1000)) != 0 {
		t.Error("no wards must give no batches")
	}
	if got := wardBatches(wards[:1000], 1000); len(got) != 1 {
		t.Errorf("exactly one batch expected, got %d", len(got))
	}
}
