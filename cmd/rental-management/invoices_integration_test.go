//go:build integration

package main

import (
	"context"
	"log/slog"
	"testing"

	"rental-management/config"
)

// The invoice context is tested through the real composition code: the plan context decides who is billed
// automatically (the plan bridge), and the demo seed writes bills of every kind on the demo contracts.

const testDataKey = "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="

func (e *bridgeEnv) invoices() invoices {
	e.t.Helper()
	cfg := config.Config{DataEncryptionKey: testDataKey}
	sealer, err := newSealer(cfg)
	if err != nil {
		e.t.Fatal(err)
	}
	b, err := newInvoices(cfg, slog.New(slog.DiscardHandler), e.pool, e.plans, sealer)
	if err != nil {
		e.t.Fatal(err)
	}
	return b
}

func TestAutomaticInvoicingFollowsThePlanOfTheLandlord(t *testing.T) {
	e := newBridgeEnv(t)
	ctx := context.Background()
	plan := e.seedPlans()
	// Two landlords with a rented room (a blank contract runs on each), one on a plan with automatic invoicing.
	e.newRoom("R1", "rented")
	e.newRoomFor("landlord2", "R2", "rented")
	e.assign(e.landlord, plan("landlord_pro"))
	b := e.invoices()

	n, err := b.automation.SendAutomatic(ctx)
	if err != nil || n != 1 {
		t.Fatalf("only the landlord on a plan with the feature is billed: %d %v", n, err)
	}
	var billed int
	if err := e.pool.QueryRow(ctx, `SELECT count(*) FROM invoices i JOIN contracts c ON c.id = i.contract_id WHERE c.landlord_id = $1`, e.landlord2).Scan(&billed); err != nil || billed != 0 {
		t.Errorf("the free plan has no automatic bills: %d %v", billed, err)
	}

	// The free plan gets the feature when its plan says so, and the one on the paid plan loses it with the plan.
	e.assign(e.landlord2, plan("landlord_vip"))
	e.assign(e.landlord, plan("landlord_free"))
	if n, err := b.automation.SendAutomatic(ctx); err != nil || n != 1 {
		t.Fatalf("the plans were swapped: %d %v", n, err)
	}
	if n, err := b.automation.SendAutomatic(ctx); err != nil || n != 0 {
		t.Errorf("nothing left to bill: %d %v", n, err)
	}
}

func TestSeedDemoGivesTheDemoContractsBillsOfEveryKindAndIsSafeToRepeat(t *testing.T) {
	e := newBridgeEnv(t)
	ctx := context.Background()
	b := e.invoices()
	made := 0
	for i := 0; i < 2; i++ {
		if err := e.props.seeder.SeedDemo(ctx, e.landlord); err != nil {
			t.Fatal(err)
		}
		if _, err := e.offers.seeder.SeedDemo(ctx, e.landlord, &e.admin); err != nil {
			t.Fatal(err)
		}
		if err := e.rentals.seeder.SeedDemo(ctx, e.landlord, e.tenant); err != nil {
			t.Fatal(err)
		}
		n, err := b.seeder.SeedDemo(ctx, e.landlord)
		if err != nil {
			t.Fatal(err)
		}
		made += n
	}
	if made != 4 {
		t.Errorf("four bills the first time and none the second: %d", made)
	}
	count := func(where string) (n int) {
		if err := e.pool.QueryRow(ctx, `SELECT count(*) FROM invoices WHERE `+where).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if count("status = 'paid'") != 1 || count("status = 'sent'") != 2 || count("status = 'draft'") != 1 || count("delivery = 'link'") != 1 {
		t.Errorf("a paid, a sent, a sent by link and a draft")
	}
	if count("delivery = 'link' AND share_token_hash IS NOT NULL AND share_token_sealed IS NOT NULL") != 1 {
		t.Error("the link bill has its link")
	}
}
