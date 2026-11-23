package location

import (
	"context"
	"errors"
	"testing"

	domain "rental-management/internal/domain/location"
)

// These tests run the location use cases against hand-written fakes of the ports: no database. The call log of
// the fakes tells the order of the calls.

type fakeTx struct{ log *[]string }

func (f fakeTx) WithinTx(ctx context.Context, fn func(ctx context.Context) error) error {
	*f.log = append(*f.log, "begin")
	err := fn(ctx)
	if err != nil {
		*f.log = append(*f.log, "rollback")
		return err
	}
	*f.log = append(*f.log, "commit")
	return nil
}

type fakeRepo struct {
	log       *[]string
	provinces []domain.Division
	wards     map[int32][]domain.Division
	err       error
	upsertErr map[string]error
	gotWards  []domain.Ward
	gotProvs  []domain.Province
}

func (f *fakeRepo) Provinces(context.Context) ([]domain.Division, error) { return f.provinces, f.err }

func (f *fakeRepo) ProvinceExists(_ context.Context, code int32) (bool, error) {
	_, ok := f.wards[code]
	return ok, f.err
}

func (f *fakeRepo) WardsOfProvince(_ context.Context, code int32) ([]domain.Division, error) {
	*f.log = append(*f.log, "wards")
	return f.wards[code], nil
}

func (f *fakeRepo) UpsertProvinces(_ context.Context, p []domain.Province) error {
	*f.log = append(*f.log, "upsert provinces")
	f.gotProvs = p
	return f.upsertErr["provinces"]
}

func (f *fakeRepo) UpsertWards(_ context.Context, w []domain.Ward) error {
	*f.log = append(*f.log, "upsert wards")
	f.gotWards = w
	return f.upsertErr["wards"]
}

type fakeSource struct {
	snap domain.Snapshot
	err  error
}

func (f fakeSource) Load() (domain.Snapshot, error) { return f.snap, f.err }

func newRepo() (*fakeRepo, *[]string) {
	log := &[]string{}
	return &fakeRepo{
		log:       log,
		provinces: []domain.Division{{Code: 1, Name: "Thành phố Hà Nội"}},
		wards:     map[int32][]domain.Division{1: {{Code: 4, Name: "Phường Ba Đình"}}, 79: {}},
	}, log
}

func TestProvinces(t *testing.T) {
	repo, _ := newRepo()
	got, err := NewLocations(repo).Provinces(context.Background())
	if err != nil || len(got) != 1 || got[0].Code != 1 {
		t.Fatalf("got %v, %v", got, err)
	}
	repo.err = errors.New("boom")
	if _, err := NewLocations(repo).Provinces(context.Background()); !errors.Is(err, repo.err) {
		t.Errorf("a repository error must come back unchanged, got %v", err)
	}
}

func TestWards(t *testing.T) {
	repo, log := newRepo()
	uc := NewLocations(repo)

	got, err := uc.Wards(context.Background(), 1)
	if err != nil || len(got) != 1 || got[0].Name != "Phường Ba Đình" {
		t.Fatalf("found: got %v, %v", got, err)
	}
	if got, err := uc.Wards(context.Background(), 79); err != nil || len(got) != 0 {
		t.Errorf("a province without wards: got %v, %v", got, err)
	}

	*log = nil
	if _, err := uc.Wards(context.Background(), 999); !errors.Is(err, domain.ErrProvinceNotFound) {
		t.Errorf("unknown province: err = %v", err)
	}
	if len(*log) != 0 {
		t.Errorf("wards must not be read for an unknown province, calls: %v", *log)
	}

	repo.err = errors.New("boom")
	if _, err := uc.Wards(context.Background(), 1); !errors.Is(err, repo.err) || errors.Is(err, domain.ErrProvinceNotFound) {
		t.Errorf("a repository error must come back unchanged, got %v", err)
	}
}

func snapshot() domain.Snapshot {
	return domain.Snapshot{
		Provinces: []domain.Province{{Code: 1, Name: "Hà Nội", Slug: "ha_noi"}},
		Wards:     []domain.Ward{{Code: 10, ProvinceCode: 1, Name: "Phường A", Slug: "phuong_a"}},
	}
}

func TestSeedWritesProvincesBeforeWardsInOneTransaction(t *testing.T) {
	repo, log := newRepo()
	seeder := NewSeeder(fakeTx{log}, repo, fakeSource{snap: snapshot()})

	// Seeding twice is the idempotence the repository guarantees with upserts: the same calls, each time.
	for i := 0; i < 2; i++ {
		*log = nil
		if err := seeder.Seed(context.Background()); err != nil {
			t.Fatalf("seed %d: %v", i+1, err)
		}
		want := []string{"begin", "upsert provinces", "upsert wards", "commit"}
		if len(*log) != len(want) {
			t.Fatalf("seed %d: calls %v, want %v", i+1, *log, want)
		}
		for j := range want {
			if (*log)[j] != want[j] {
				t.Fatalf("seed %d: calls %v, want %v", i+1, *log, want)
			}
		}
	}
	if len(repo.gotProvs) != 1 || len(repo.gotWards) != 1 {
		t.Errorf("the snapshot must be written as it is: %d provinces, %d wards", len(repo.gotProvs), len(repo.gotWards))
	}
}

func TestSeedStopsOnErrors(t *testing.T) {
	boom := errors.New("boom")

	repo, log := newRepo()
	if err := NewSeeder(fakeTx{log}, repo, fakeSource{err: boom}).Seed(context.Background()); !errors.Is(err, boom) {
		t.Errorf("a snapshot that cannot be loaded: err = %v", err)
	}
	if len(*log) != 0 {
		t.Errorf("no transaction must open without a snapshot, calls: %v", *log)
	}

	repo, log = newRepo()
	repo.upsertErr = map[string]error{"provinces": boom}
	if err := NewSeeder(fakeTx{log}, repo, fakeSource{snap: snapshot()}).Seed(context.Background()); !errors.Is(err, boom) {
		t.Errorf("provinces failing: err = %v", err)
	}
	for _, call := range *log {
		if call == "upsert wards" {
			t.Error("wards must not be written after the provinces failed")
		}
	}

	repo, log = newRepo()
	repo.upsertErr = map[string]error{"wards": boom}
	if err := NewSeeder(fakeTx{log}, repo, fakeSource{snap: snapshot()}).Seed(context.Background()); !errors.Is(err, boom) {
		t.Errorf("wards failing: err = %v", err)
	}
	if last := (*log)[len(*log)-1]; last != "rollback" {
		t.Errorf("a failed seed must roll back, last call %q", last)
	}
}
