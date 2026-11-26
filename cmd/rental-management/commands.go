package main

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

// runMigrate runs `rental-management migrate [up|down|status]` (up by default).
func runMigrate(ctx context.Context, args []string, out io.Writer) error {
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

// runSeed runs `rental-management seed [-demo]`: it loads the reference data (roles, permissions, plans, provinces,
// wards); with -demo also the demo accounts and their data.
func runSeed(ctx context.Context, args []string, out io.Writer) error {
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
	log, err := logger.New(io.Discard, cfg.LogLevel, cfg.LogFormat)
	if err != nil {
		return err
	}
	// The seed wires no notifier: its demo flows stay silent (the demo inbox is written by its own seeder).
	pricing := newPlans(log, pool, nil)
	if err := pricing.seeder.SeedPlans(ctx); err != nil {
		return err
	}
	_, _ = fmt.Fprintln(out, "seeded the service plans")
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

		// The seed stores no pictures, so it gets an in-memory store instead of touching the media directory.
		offers := newListings(log, pool, pricing, nil)
		sealer, err := newSealer(cfg)
		if err != nil {
			return err
		}
		invitees, err := newInvitees(cfg, log, pool)
		if err != nil {
			return err
		}
		// The seed never emails anybody: the invitation mailer keeps its no-op default.
		rentals := newContracts(cfg, log, pool, pricing, invitationPorts{invitees: invitees, sealer: sealer}, nil)
		properties := newProperty(cfg, log, pool, storage.NewMemory(cfg.MediaPublicBaseURL), rentals, offers, pricing)
		if err := properties.seeder.SeedDemo(ctx, ids["landlord@rental.test"]); err != nil {
			return err
		}
		_, _ = fmt.Fprintln(out, "seeded the demo property data")
		// The demo contracts register for rooms, which needs their listings approved: write the listings first.
		admin := ids["admin@rental.test"]
		listed, err := offers.seeder.SeedDemo(ctx, ids["landlord@rental.test"], &admin)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(out, "seeded the demo listings (%d new)\n", listed)
		if err := rentals.seeder.SeedDemo(ctx, ids["landlord@rental.test"], ids["tenant@rental.test"]); err != nil {
			return err
		}
		_, _ = fmt.Fprintln(out, "seeded the demo contracts")
		offered, err := rentals.seeder.SeedDemoInvitation(ctx, ids["landlord@rental.test"], ids["tenant@rental.test"])
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(out, "seeded the demo invitation (%d new)\n", offered)
		billing, err := newInvoices(cfg, log, pool, pricing, sealer, nil)
		if err != nil {
			return err
		}
		made, err := billing.seeder.SeedDemo(ctx, ids["landlord@rental.test"])
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(out, "seeded the demo invoices (%d new)\n", made)
		paying, err := newPayments(cfg, log, pool, storage.NewMemory(cfg.MediaPublicBaseURL), sealer, billing, nil)
		if err != nil {
			return err
		}
		paid, err := paying.seeder.SeedDemo(ctx, ids["landlord@rental.test"], ids["tenant@rental.test"])
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(out, "seeded the demo payments (%d new)\n", paid)
		given, err := pricing.seeder.SeedDemoSubscriptions(ctx, ids)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(out, "put the demo accounts on their plans (%d changed)\n", given)
		told, err := newNotifications(cfg, log, pool, nil, nil).seeder.SeedDemo(ctx, ids)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(out, "seeded the demo notifications (%d new)\n", told)
		reported, err := newReporting(log, pool, nil, nil).seeder.SeedDemo(ctx, ids["tenant@rental.test"], ids["landlord@rental.test"], ids["admin@rental.test"])
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(out, "seeded the demo reports (%d new)\n", reported)
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

// runExpireSubscriptions runs `rental-management subscriptions-expire`: it marks the subscriptions that ran past their
// end as expired and prints how many it did. It is safe to run again and to run twice at once, so a daily cron entry
// (or `rental-management worker`, which runs it daily) can call it as often as it likes.
func runExpireSubscriptions(ctx context.Context, out io.Writer) error {
	cfg, err := loadWithDatabase()
	if err != nil {
		return err
	}
	pool, err := openPool(ctx, cfg, 0)
	if err != nil {
		return err
	}
	defer pool.Close()
	log, err := logger.New(io.Discard, cfg.LogLevel, cfg.LogFormat)
	if err != nil {
		return err
	}
	n, err := newPlans(log, pool, newNotifications(cfg, log, pool, nil, nil).deliveries).subscriptions.ExpireDue(ctx)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "expired %d subscriptions\n", n)
	return nil
}

// runInvoiceCommand opens the database and the invoice use cases for the daily commands.
func runInvoiceCommand(ctx context.Context, out io.Writer, run func(context.Context, invoices) (string, error)) error {
	cfg, err := loadWithDatabase()
	if err != nil {
		return err
	}
	pool, err := openPool(ctx, cfg, 0)
	if err != nil {
		return err
	}
	defer pool.Close()
	log, err := logger.New(os.Stderr, cfg.LogLevel, cfg.LogFormat)
	if err != nil {
		return err
	}
	sealer, err := newSealer(cfg)
	if err != nil {
		return err
	}
	pusher, closePusher, err := openCommandPusher(ctx, cfg, log)
	if err != nil {
		return err
	}
	defer closePusher()
	inbox := newNotifications(cfg, log, pool, nil, pusher)
	billing, err := newInvoices(cfg, log, pool, newPlans(log, pool, inbox.deliveries), sealer, inbox.deliveries)
	if err != nil {
		return err
	}
	line, err := run(ctx, billing)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintln(out, line)
	return nil
}

// runSendInvoices runs `rental-management invoices-send-automatic`: it sends the bill of the current period of every
// running contract whose landlord's plan has automatic invoicing and that has no bill for it yet. A period that has a
// bill is left alone, so it is safe to run again and to run twice at once: a daily cron entry (or `rental-management
// worker`) can call it as often as it likes.
func runSendInvoices(ctx context.Context, out io.Writer) error {
	return runInvoiceCommand(ctx, out, func(ctx context.Context, b invoices) (string, error) {
		n, err := b.automation.SendAutomatic(ctx)
		return fmt.Sprintf("sent %d invoices", n), err
	})
}

// runSendReminders runs `rental-management invoices-send-reminders`: it reminds tenants of bills that fall due in
// INVOICE_REMINDER_DAYS days, and tenants and landlords of bills that fell due yesterday. Every bill gets each
// reminder once, so it is safe to run again the same day.
func runSendReminders(ctx context.Context, out io.Writer) error {
	return runInvoiceCommand(ctx, out, func(ctx context.Context, b invoices) (string, error) {
		n, err := b.automation.SendReminders(ctx)
		return fmt.Sprintf("sent %d reminders", n), err
	})
}

// runPruneNotifications runs `rental-management notifications-prune`: it deletes the messages older than
// NOTIFICATION_RETENTION_DAYS and prints how many it deleted. It is safe to run again and to run twice at once, so a
// daily cron entry (or `rental-management worker`) can call it as often as it likes.
func runPruneNotifications(ctx context.Context, out io.Writer) error {
	cfg, err := loadWithDatabase()
	if err != nil {
		return err
	}
	pool, err := openPool(ctx, cfg, 0)
	if err != nil {
		return err
	}
	defer pool.Close()
	log, err := logger.New(io.Discard, cfg.LogLevel, cfg.LogFormat)
	if err != nil {
		return err
	}
	n, err := newNotifications(cfg, log, pool, nil, nil).pruner.Prune(ctx)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "deleted %d notifications\n", n)
	return nil
}

// runDispatchAnnouncements runs `rental-management announcements-dispatch-due`: it sends the notifications of the
// published announcements whose start has come (a scheduled one waits for it) and retries the ones whose delivery
// failed. Every announcement is delivered once, so it is safe to run again and to run twice at once: a cron entry every
// minute (or `rental-management worker`) can call it.
func runDispatchAnnouncements(ctx context.Context, out io.Writer) error {
	cfg, err := loadWithDatabase()
	if err != nil {
		return err
	}
	pool, err := openPool(ctx, cfg, 0)
	if err != nil {
		return err
	}
	defer pool.Close()
	log, err := logger.New(os.Stderr, cfg.LogLevel, cfg.LogFormat)
	if err != nil {
		return err
	}
	pusher, closePusher, err := openCommandPusher(ctx, cfg, log)
	if err != nil {
		return err
	}
	defer closePusher()
	n, err := newNotifications(cfg, log, pool, nil, pusher).announcements.DispatchDue(ctx)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "delivered %d announcements\n", n)
	return nil
}
