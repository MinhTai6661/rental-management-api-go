package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"rental-management/config"
	"rental-management/internal/adapter/persistence/postgres"
	identitypg "rental-management/internal/adapter/persistence/postgres/identity"
	locationpg "rental-management/internal/adapter/persistence/postgres/location"
	"rental-management/internal/adapter/security"
	"rental-management/internal/adapter/snapshot/locationdata"
	domain "rental-management/internal/domain/identity"
	usecase "rental-management/internal/usecase/identity"
	locationuc "rental-management/internal/usecase/location"
	"rental-management/pkg/db"
	"rental-management/pkg/logger"
	"rental-management/pkg/storage"
)

// RunMigrate runs `rental-management migrate [up|down|status]` (up by default).
func RunMigrate(ctx context.Context, args []string, out io.Writer) error {
	command := "up"
	if len(args) > 0 {
		command = args[0]
	}
	cfg, err := loadWithDatabase()
	if err != nil {
		return err
	}
	pool, err := openPool(ctx, cfg, 0) // a migration may legitimately run longer than a request
	if err != nil {
		return err
	}
	defer pool.Close()
	return db.Migrate(ctx, pool, command, out)
}

// RunSeed runs `rental-management seed [-demo]`: it loads the reference data (roles, permissions, provinces, wards).
func RunSeed(ctx context.Context, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("seed", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	demo := fs.Bool("demo", false, "also create the demo accounts")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("seed: %w (usage: rental-management seed [-demo])", err)
	}

	cfg, err := loadWithDatabase()
	if err != nil {
		return err
	}
	if *demo {
		if err := checkDemoAllowed(cfg); err != nil {
			return err
		}
	}

	pool, err := openPool(ctx, cfg, 0)
	if err != nil {
		return err
	}
	defer pool.Close()

	tx := postgres.NewTxManager(pool)
	users := identitypg.NewUserRepository(pool)
	seeder := usecase.NewSeeder(tx, identitypg.NewPermissionRepository(pool), users, security.PasswordHasher{})
	locations := locationuc.NewSeeder(tx, locationpg.NewLocationRepository(pool), locationdata.Source{})
	if err := seeder.SeedCatalog(ctx); err != nil {
		return err
	}
	_, _ = fmt.Fprintln(out, "seeded roles and permissions")
	if err := locations.Seed(ctx); err != nil {
		return err
	}
	_, _ = fmt.Fprintln(out, "seeded provinces and wards")
	if *demo {
		ids, err := seeder.SeedDemo(ctx, cfg.SeedDemoPassword, cfg.TermsVersion, time.Now())
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(out, "seeded the demo accounts (%d); the password is SEED_DEMO_PASSWORD\n", len(domain.DemoAccounts()))

		log, err := logger.New(io.Discard, cfg.LogLevel, cfg.LogFormat)
		if err != nil {
			return err
		}
		// The seed stores no pictures, so it gets an in-memory store instead of touching the media directory.
		properties := newProperty(cfg, log, pool, storage.NewMemory(cfg.MediaPublicBaseURL))
		if err := properties.seeder.SeedDemo(ctx, ids["landlord@rental.test"]); err != nil {
			return err
		}
		_, _ = fmt.Fprintln(out, "seeded the demo property data")
	}
	return nil
}

// checkDemoAllowed refuses to create demo accounts where they would be a hole: in production, or without a password
// of the operator's choosing (there is no default, so no account with a known password can appear by accident).
func checkDemoAllowed(cfg config.Config) error {
	if cfg.Env == "production" {
		return errors.New("seed -demo is refused when APP_ENV=production")
	}
	if cfg.SeedDemoPassword == "" {
		return errors.New("seed -demo needs SEED_DEMO_PASSWORD (at least 8 characters)")
	}
	return nil
}

func loadWithDatabase() (config.Config, error) {
	cfg, err := config.Load()
	if err != nil {
		return config.Config{}, err
	}
	return cfg, cfg.RequireDatabase()
}

// Stdout is where the commands print their progress.
func Stdout() io.Writer { return os.Stdout }
