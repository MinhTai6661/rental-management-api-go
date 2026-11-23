package config

import (
	"strings"
	"testing"
	"time"
)

// secret is a 32-byte signing key.
const secret = "0123456789abcdef0123456789abcdef"

func TestParseDefaultsNeedNoBackends(t *testing.T) {
	cfg, err := parse(map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTPAddr != ":8080" || cfg.RequestTimeout != 30*time.Second || cfg.ShutdownTimeout != 15*time.Second {
		t.Errorf("unexpected HTTP defaults: %+v", cfg)
	}
	if cfg.Env != "local" || cfg.LogLevel != "info" || cfg.LogFormat != "json" {
		t.Errorf("unexpected defaults: %+v", cfg)
	}
	if cfg.HasDatabase() || cfg.HasRedis() || cfg.HasJWTSecret() {
		t.Errorf("backends must be optional: %+v", cfg)
	}
	if len(cfg.CORSAllowedOrigins) != 1 || cfg.CORSAllowedOrigins[0] != "http://localhost:2001" || cfg.TermsVersion != "2026-11-03" {
		t.Errorf("unexpected web defaults: %+v", cfg)
	}
	if cfg.AccessTokenTTL != 15*time.Minute || cfg.RefreshTokenTTL != 720*time.Hour || cfg.RefreshReuseGrace != 10*time.Second ||
		cfg.JWTLeeway != 5*time.Second || cfg.JWTIssuer != "rental-management" || cfg.PermissionCacheTTL != 60*time.Second {
		t.Errorf("unexpected token defaults: %+v", cfg)
	}
	if cfg.DBMaxConns != 10 || cfg.DBStatementTimeoutMS != 30000 || cfg.DBLockTimeoutMS != 10000 ||
		cfg.DBIdleInTransactionTimeoutMS != 60000 || cfg.RedisKeyPrefix != "rental-management:" {
		t.Errorf("unexpected backend defaults: %+v", cfg)
	}
}

func TestParseOverrides(t *testing.T) {
	cfg, err := parse(map[string]string{
		"APP_ENV":              "production",
		"HTTP_ADDR":            ":9000",
		"HTTP_REQUEST_TIMEOUT": "5s",
		"LOG_LEVEL":            "debug",
		"LOG_FORMAT":           "text",
		"CORS_ALLOWED_ORIGINS": "https://app.example.vn,http://localhost:2001",
		"DATABASE_URL":         "postgres://u@localhost:5442/app?sslmode=disable",
		"REDIS_URL":            "redis://localhost:6389/0",
		"JWT_SECRET":           secret,
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Env != "production" || cfg.HTTPAddr != ":9000" || cfg.RequestTimeout != 5*time.Second {
		t.Errorf("overrides not applied: %+v", cfg)
	}
	if len(cfg.CORSAllowedOrigins) != 2 || !cfg.HasDatabase() || !cfg.HasRedis() || !cfg.HasJWTSecret() {
		t.Errorf("overrides not applied: %+v", cfg)
	}
}

func TestParseRejectsInvalidValues(t *testing.T) {
	cases := map[string]map[string]string{
		"unknown env":        {"APP_ENV": "staging"},
		"bad timeout":        {"HTTP_REQUEST_TIMEOUT": "soon"},
		"zero timeout":       {"HTTP_REQUEST_TIMEOUT": "0s"},
		"negative stop":      {"SHUTDOWN_TIMEOUT": "-1s"},
		"bad log level":      {"LOG_LEVEL": "loud"},
		"bad log format":     {"LOG_FORMAT": "xml"},
		"zero conns":         {"DB_MAX_CONNS": "0"},
		"zero timeout ms":    {"DB_STATEMENT_TIMEOUT_MS": "0"},
		"short jwt secret":   {"JWT_SECRET": "short"},
		"negative leeway":    {"JWT_LEEWAY": "-1s"},
		"zero access ttl":    {"ACCESS_TOKEN_TTL": "0s"},
		"zero refresh ttl":   {"REFRESH_TOKEN_TTL": "0s"},
		"negative grace":     {"REFRESH_REUSE_GRACE": "-1s"},
		"mysql url":          {"DATABASE_URL": "mysql://u@localhost/app"},
		"http redis url":     {"REDIS_URL": "http://localhost:6379"},
		"wildcard origin":    {"CORS_ALLOWED_ORIGINS": "*"},
		"origin with a path": {"CORS_ALLOWED_ORIGINS": "http://localhost:2001/app"},
	}
	for name, vars := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := parse(vars)
			if err == nil {
				t.Errorf("expected an error for %v", vars)
			}
		})
	}
}

func TestParseErrorNamesTheVariable(t *testing.T) {
	_, err := parse(map[string]string{"JWT_SECRET": "short"})
	if err == nil || !strings.Contains(err.Error(), "JWT_SECRET") {
		t.Errorf("error should name JWT_SECRET: %v", err)
	}
}

func TestParseEmptyValueFallsBackToDefault(t *testing.T) {
	cfg, err := parse(map[string]string{"HTTP_ADDR": ""})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTPAddr != ":8080" {
		t.Errorf("HTTPAddr = %q, want the default", cfg.HTTPAddr)
	}
}

func TestRequireAPINeedsDatabaseRedisAndSigningKey(t *testing.T) {
	cfg, err := parse(map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	err = cfg.RequireAPI()
	if err == nil {
		t.Fatal("the API cannot run with nothing configured")
	}
	for _, name := range []string{"DATABASE_URL", "REDIS_URL", "JWT_SECRET"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("the error should name %s: %v", name, err)
		}
	}

	full := map[string]string{
		"DATABASE_URL": "postgres://u@localhost:5442/app?sslmode=disable",
		"REDIS_URL":    "redis://localhost:6389/0",
		"JWT_SECRET":   secret,
	}
	cfg, err = parse(full)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.RequireAPI(); err != nil {
		t.Errorf("everything is set: %v", err)
	}

	// In production emails must really be sent: without SMTP the verification links would only be logged.
	full["APP_ENV"] = "production"
	cfg, err = parse(full)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.RequireAPI(); err == nil || !strings.Contains(err.Error(), "SMTP_HOST") {
		t.Errorf("production without SMTP must be refused: %v", err)
	}
	full["SMTP_HOST"] = "smtp.example.vn"
	cfg, _ = parse(full)
	if err := cfg.RequireAPI(); err != nil {
		t.Errorf("production with SMTP: %v", err)
	}
}

func TestEmailAndSeedSettings(t *testing.T) {
	cfg, err := parse(map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.EmailVerificationTTL != 24*time.Hour || cfg.EmailResendCooldown != 60*time.Second || cfg.SeedDemoPassword != "" {
		t.Errorf("unexpected defaults: %+v", cfg)
	}
	for name, vars := range map[string]map[string]string{
		"zero verification ttl": {"EMAIL_VERIFICATION_TTL": "0s"},
		"zero resend cooldown":  {"EMAIL_RESEND_COOLDOWN": "0s"},
		"short demo password":   {"SEED_DEMO_PASSWORD": "short"},
	} {
		if _, err := parse(vars); err == nil {
			t.Errorf("%s must be refused", name)
		}
	}
}
