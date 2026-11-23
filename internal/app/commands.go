package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"rental-management/internal/config"
	"rental-management/internal/db"
	"rental-management/internal/identity"
	"rental-management/internal/location"
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

	if err := identity.Seed(ctx, pool); err != nil {
		return err
	}
	_, _ = fmt.Fprintln(out, "seeded roles and permissions")
	if err := location.Seed(ctx, pool); err != nil {
		return err
	}
	_, _ = fmt.Fprintln(out, "seeded provinces and wards")
	if *demo {
		if err := identity.SeedDemo(ctx, pool, cfg.SeedDemoPassword, cfg.TermsVersion, time.Now()); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(out, "seeded the demo accounts (%d); the password is SEED_DEMO_PASSWORD\n", len(identity.DemoAccounts()))
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
