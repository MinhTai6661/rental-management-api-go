package location

import (
	"context"
	"fmt"
)

// Seeder loads the reference data into the tables.
type Seeder struct {
	tx     TxManager
	repo   LocationRepository
	source SnapshotSource
}

// NewSeeder returns a seeder.
func NewSeeder(tx TxManager, repo LocationRepository, source SnapshotSource) *Seeder {
	return &Seeder{tx: tx, repo: repo, source: source}
}

// Seed makes the tables match the snapshot: it inserts what is missing and updates what changed. It runs in one
// transaction (provinces first, the wards refer to them) and is safe to repeat.
func (s *Seeder) Seed(ctx context.Context) error {
	snap, err := s.source.Load()
	if err != nil {
		return err
	}
	return s.tx.WithinTx(ctx, func(ctx context.Context) error {
		if err := s.repo.UpsertProvinces(ctx, snap.Provinces); err != nil {
			return fmt.Errorf("seed provinces: %w", err)
		}
		if err := s.repo.UpsertWards(ctx, snap.Wards); err != nil {
			return fmt.Errorf("seed wards: %w", err)
		}
		return nil
	})
}
