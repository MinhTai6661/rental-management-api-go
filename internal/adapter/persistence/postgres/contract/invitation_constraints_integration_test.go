//go:build integration

package contractpg

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// The database refuses the rows an invitation must never be in; each case is one CHECK or unique constraint of
// 00009_contract_invitations.sql, written as raw SQL so the application code cannot hide a hole.
func TestInvitationConstraints(t *testing.T) {
	ctx := context.Background()
	r := newRepos(t)

	hash := func() string { return strings.ReplaceAll(uuid.NewString()+uuid.NewString(), "-", "")[:64] }

	try := func(t *testing.T, extraCols, extraVals string) error {
		t.Helper()
		args := []any{r.room, r.landlord, hash()}
		q := `INSERT INTO contract_invitations (room_id, landlord_id, token_hash, expires_at, price_vnd, starts_on` + extraCols + `)
		      VALUES ($1, $2, $3, now() + interval '7 days', 1000000, '2026-12-01'` + extraVals + `)`
		_, err := r.pool.Exec(ctx, q, args...)
		return err
	}

	if err := try(t, "", ""); err != nil {
		t.Fatalf("a plain pending invitation is fine: %v", err)
	}
	cases := map[string]struct{ cols, vals string }{
		"revoked with a sealed link":      {`, status, link_token_sealed, answered_at`, `, 'revoked', 'x', now()`},
		"accepted without a contract":     {`, status, answered_at`, `, 'accepted', now()`},
		"pending with an answer time":     {`, answered_at`, `, now()`},
		"end on the start day":            {`, ends_on`, `, '2026-12-01'`},
		"upper case recipient email":      {`, recipient_email`, `, 'A@Example.vn'`},
		"contract status ended":           {`, contract_status`, `, 'ended'`},
		"unknown status":                  {`, status`, `, 'expired'`},
		"too many occupants":              {`, occupant_count`, `, 51`},
		"negative deposit":                {`, deposit_vnd`, `, -1`},
		"answered without an answer time": {`, status`, `, 'declined'`},
	}
	for name, tc := range cases {
		if err := try(t, tc.cols, tc.vals); err == nil {
			t.Errorf("%s: the database accepted it", name)
		}
	}

	dup := hash()
	args := []any{r.room, r.landlord, dup}
	const q = `INSERT INTO contract_invitations (room_id, landlord_id, token_hash, expires_at, price_vnd, starts_on)
	           VALUES ($1, $2, $3, now() + interval '7 days', 1000000, '2026-12-01')`
	if _, err := r.pool.Exec(ctx, q, args...); err != nil {
		t.Fatal(err)
	}
	if _, err := r.pool.Exec(ctx, q, args...); err == nil {
		t.Error("a second invitation with the same token hash was accepted")
	}
	if _, err := r.pool.Exec(ctx, `INSERT INTO invitation_email_blocks (email_hash) VALUES (repeat('a', 63))`); err == nil {
		t.Error("an email hash of 63 characters was accepted")
	}
	if _, err := r.pool.Exec(ctx, `INSERT INTO contract_invitations (room_id, landlord_id, token_hash, expires_at, price_vnd, starts_on)
	                               VALUES ($1, $2, 'short', now(), 1, '2026-12-01')`, r.room, r.landlord); err == nil {
		t.Error("a token hash that is not 64 characters was accepted")
	}
}
