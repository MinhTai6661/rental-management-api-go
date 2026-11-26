//go:build integration

package redisstore

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/identity"
	"rental-management/internal/domain/shared"
)

func TestSocialStateWorksOnceAndKeepsEverythingItWasGiven(t *testing.T) {
	ctx := context.Background()
	store := NewSocialStateStore(rds.Client(t), "test:")
	userID := uuid.New()
	want := domain.SocialState{
		Provider: "zalo", Intent: domain.IntentLink, UserID: &userID, ActorRole: "tenant", ReturnPath: "profile", CodeVerifier: "verifier",
	}
	key, err := store.Put(ctx, want, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	got, err := store.Take(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	if got.Provider != want.Provider || got.Intent != want.Intent || got.UserID == nil || *got.UserID != userID ||
		got.ActorRole != want.ActorRole || got.ReturnPath != want.ReturnPath || got.CodeVerifier != want.CodeVerifier {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	if _, err := store.Take(ctx, key); !errors.Is(err, shared.ErrNotFound) {
		t.Fatalf("a state must work once, got %v", err)
	}
}

func TestSocialStateOfALoginHasNoUserAndRejectsForgedKeys(t *testing.T) {
	ctx := context.Background()
	store := NewSocialStateStore(rds.Client(t), "test:")
	key, _ := store.Put(ctx, domain.SocialState{Provider: "google", Intent: domain.IntentLogin, ReturnPath: "dashboard", CodeVerifier: "v"}, time.Minute)
	got, err := store.Take(ctx, key)
	if err != nil || got.UserID != nil || got.Intent != domain.IntentLogin {
		t.Fatalf("got %+v, %v", got, err)
	}
	for _, forged := range []string{"", "short", "!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"} {
		if _, err := store.Take(ctx, forged); !errors.Is(err, shared.ErrNotFound) {
			t.Errorf("%q: %v", forged, err)
		}
	}
}

func TestSocialStateExpires(t *testing.T) {
	ctx := context.Background()
	store := NewSocialStateStore(rds.Client(t), "test:")
	key, _ := store.Put(ctx, domain.SocialState{Provider: "google", Intent: domain.IntentLogin, CodeVerifier: "v"}, 200*time.Millisecond)
	time.Sleep(400 * time.Millisecond)
	if _, err := store.Take(ctx, key); !errors.Is(err, shared.ErrNotFound) {
		t.Fatalf("an expired state must be gone, got %v", err)
	}
}

func TestLoginCodeWorksOnceAndStoresNoSecretInTheKey(t *testing.T) {
	ctx := context.Background()
	client := rds.Client(t)
	store := NewLoginCodeStore(client, "test:")
	userID := uuid.New()
	code, err := store.Put(ctx, domain.SocialLoginGrant{UserID: userID, Via: "google"}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	keys, _ := client.Keys(ctx, "test:social:code:*").Result()
	for _, k := range keys {
		if k == "test:social:code:"+code {
			t.Fatal("the code itself must not be a Redis key: only its hash is")
		}
	}
	grant, err := store.Take(ctx, code)
	if err != nil || grant.UserID != userID || grant.Via != "google" {
		t.Fatalf("got %+v, %v", grant, err)
	}
	if _, err := store.Take(ctx, code); !errors.Is(err, shared.ErrNotFound) {
		t.Fatalf("a code must work once, got %v", err)
	}
}

func TestSocialStoresKeepTheirKeysApart(t *testing.T) {
	ctx := context.Background()
	client := rds.Client(t)
	states, codes := NewSocialStateStore(client, "test:"), NewLoginCodeStore(client, "test:")
	code, _ := codes.Put(ctx, domain.SocialLoginGrant{UserID: uuid.New(), Via: "zalo"}, time.Minute)
	if _, err := states.Take(ctx, code); !errors.Is(err, shared.ErrNotFound) {
		t.Fatalf("a login code must not open a state: %v", err)
	}
}
