package main

import (
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"rental-management/config"
	statspg "rental-management/internal/adapter/persistence/postgres/stats"
	statsuc "rental-management/internal/usecase/stats"
)

// numbers is the use cases of the statistics, on Postgres.
type numbers struct {
	property *statsuc.Property
	platform *statsuc.Platform
}

// newStats builds the statistics. The landlord's figures go through cache (nil: no cache); the platform's are never
// cached. The statistics only read, so they need no other context.
func newStats(cfg config.Config, log *slog.Logger, pool *pgxpool.Pool, cache statsuc.Cache) numbers {
	repo := statspg.NewRepository(pool)
	clock := statsuc.ClockFunc(time.Now)
	return numbers{
		property: statsuc.NewProperty(repo, cache, clock, log, time.Duration(cfg.StatsCacheSeconds)*time.Second),
		platform: statsuc.NewPlatform(repo, clock),
	}
}
