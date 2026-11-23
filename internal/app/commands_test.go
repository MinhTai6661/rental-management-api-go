package app

import (
	"strings"
	"testing"

	"rental-management/config"
)

func TestSeedDemoIsRefusedInProductionAndWithoutAPassword(t *testing.T) {
	cases := map[string]struct {
		cfg     config.Config
		wantErr string
	}{
		"production":          {config.Config{Env: "production", SeedDemoPassword: "demo-password-123"}, "production"},
		"no password":         {config.Config{Env: "local"}, "SEED_DEMO_PASSWORD"},
		"local with password": {config.Config{Env: "local", SeedDemoPassword: "demo-password-123"}, ""},
	}
	for name, tc := range cases {
		err := checkDemoAllowed(tc.cfg)
		switch {
		case tc.wantErr == "" && err != nil:
			t.Errorf("%s: unexpected error %v", name, err)
		case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
			t.Errorf("%s: want an error naming %q, got %v", name, tc.wantErr, err)
		}
	}
}

// The command itself refuses in production before it touches the database: no connection is made.
func TestRunSeedDemoRefusesInProductionBeforeConnecting(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("DATABASE_URL", "postgres://nobody:nothing@127.0.0.1:1/none?sslmode=disable")
	t.Setenv("SEED_DEMO_PASSWORD", "demo-password-123")

	err := RunSeed(t.Context(), []string{"-demo"}, &strings.Builder{})
	if err == nil || !strings.Contains(err.Error(), "production") {
		t.Fatalf("seed -demo must be refused in production: %v", err)
	}
}
