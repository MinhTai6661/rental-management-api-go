// Package config reads the process environment into one validated struct.
// The binary never reads a .env file: the environment is set by compose or the shell.
//
// DATABASE_URL, REDIS_URL and JWT_SECRET are optional in the struct: when set they are validated, but nothing
// fails here because they are missing. The code that needs them checks when it starts: RequireDatabase for
// `rental-management migrate|seed`, RequireAPI (database, Redis and the signing key) for `rental-management api`.
package config

import (
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/caarlos0/env/v11"
)

// minJWTSecretBytes is the shortest HS256 signing key accepted.
const minJWTSecretBytes = 32

// minSeedPasswordBytes is the shortest demo password (the same minimum people get when they register).
const minSeedPasswordBytes = 8

// Config holds every setting of the process.
type Config struct {
	Env             string        `env:"APP_ENV" envDefault:"local"`
	HTTPAddr        string        `env:"HTTP_ADDR" envDefault:":8080"`
	RequestTimeout  time.Duration `env:"HTTP_REQUEST_TIMEOUT" envDefault:"30s"`
	ShutdownTimeout time.Duration `env:"SHUTDOWN_TIMEOUT" envDefault:"15s"`
	LogLevel        string        `env:"LOG_LEVEL" envDefault:"info"`
	LogFormat       string        `env:"LOG_FORMAT" envDefault:"json"`

	// CORSAllowedOrigins are the web origins that may call the API from a browser, e.g. http://localhost:2001.
	CORSAllowedOrigins []string `env:"CORS_ALLOWED_ORIGINS" envSeparator:"," envDefault:"http://localhost:2001"`
	// TermsVersion is the version of the terms of use people must accept.
	TermsVersion string `env:"TERMS_VERSION" envDefault:"2026-11-03"`

	// Postgres. The timeouts bound every query and lock wait so one slow request cannot pile up the pool.
	DatabaseURL                  string `env:"DATABASE_URL"`
	DBMaxConns                   int32  `env:"DB_MAX_CONNS" envDefault:"10"`
	DBStatementTimeoutMS         int    `env:"DB_STATEMENT_TIMEOUT_MS" envDefault:"30000"`
	DBLockTimeoutMS              int    `env:"DB_LOCK_TIMEOUT_MS" envDefault:"10000"`
	DBIdleInTransactionTimeoutMS int    `env:"DB_IDLE_IN_TRANSACTION_TIMEOUT_MS" envDefault:"60000"`

	// WebURL is the origin of the web app; links in emails (verify address...) point there.
	WebURL string `env:"WEB_URL" envDefault:"http://localhost:2001"`

	// SMTP sends the emails. Without SMTP_HOST emails are only written to the log (nothing is delivered).
	SMTPHost     string `env:"SMTP_HOST"`
	SMTPPort     int    `env:"SMTP_PORT" envDefault:"1025"`
	SMTPUsername string `env:"SMTP_USERNAME"`
	SMTPPassword string `env:"SMTP_PASSWORD"`
	SMTPFrom     string `env:"SMTP_FROM" envDefault:"Rental Management <no-reply@rental.test>"`

	// Redis: revoked tokens, rate limits.
	RedisURL       string `env:"REDIS_URL"`
	RedisKeyPrefix string `env:"REDIS_KEY_PREFIX" envDefault:"rental-management:"`

	// Tokens. The access token is a short-lived JWT; the refresh token is a random string rotated on every use.
	JWTSecret          string        `env:"JWT_SECRET"`
	JWTIssuer          string        `env:"JWT_ISSUER" envDefault:"rental-management"`
	JWTLeeway          time.Duration `env:"JWT_LEEWAY" envDefault:"5s"`
	AccessTokenTTL     time.Duration `env:"ACCESS_TOKEN_TTL" envDefault:"15m"`
	RefreshTokenTTL    time.Duration `env:"REFRESH_TOKEN_TTL" envDefault:"720h"`
	RefreshReuseGrace  time.Duration `env:"REFRESH_REUSE_GRACE" envDefault:"10s"`
	PermissionCacheTTL time.Duration `env:"PERMISSION_CACHE_TTL" envDefault:"60s"`

	// EmailVerificationTTL is how long a verification link works; EmailResendCooldown is the wait between two
	// verification emails to the same address.
	EmailVerificationTTL time.Duration `env:"EMAIL_VERIFICATION_TTL" envDefault:"24h"`
	EmailResendCooldown  time.Duration `env:"EMAIL_RESEND_COOLDOWN" envDefault:"60s"`

	// SeedDemoPassword is the password of the demo accounts `seed -demo` creates (development only).
	SeedDemoPassword string `env:"SEED_DEMO_PASSWORD"`
}

// HasDatabase reports whether DATABASE_URL is set.
func (c Config) HasDatabase() bool { return c.DatabaseURL != "" }

// RequireDatabase fails when DATABASE_URL is not set: the commands that touch the database need it.
func (c Config) RequireDatabase() error {
	if !c.HasDatabase() {
		return fmt.Errorf("DATABASE_URL is required")
	}
	return nil
}

// RequireAPI fails when a setting the API cannot run without is missing: the database, Redis (revoked tokens and
// rate limits) and the key that signs access tokens.
func (c Config) RequireAPI() error {
	var missing []string
	if !c.HasDatabase() {
		missing = append(missing, "DATABASE_URL")
	}
	if !c.HasRedis() {
		missing = append(missing, "REDIS_URL")
	}
	if !c.HasJWTSecret() {
		missing = append(missing, "JWT_SECRET")
	}
	// Without SMTP the verification links would only be written to the log, which in production means anybody who can
	// read the logs could verify (and take over) any address.
	if c.Env == "production" && !c.HasSMTP() {
		missing = append(missing, "SMTP_HOST")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing settings the API cannot run without: %s", strings.Join(missing, ", "))
	}
	return nil
}

// HasSMTP reports whether emails are delivered over SMTP.
func (c Config) HasSMTP() bool { return c.SMTPHost != "" }

// HasRedis reports whether REDIS_URL is set.
func (c Config) HasRedis() bool { return c.RedisURL != "" }

// HasJWTSecret reports whether JWT_SECRET is set.
func (c Config) HasJWTSecret() bool { return c.JWTSecret != "" }

// Load reads the real process environment.
func Load() (Config, error) {
	return parse(nil)
}

// parse reads the given variables, or the process environment when vars is nil.
func parse(vars map[string]string) (Config, error) {
	var cfg Config
	if err := env.ParseWithOptions(&cfg, env.Options{Environment: vars}); err != nil {
		return Config{}, fmt.Errorf("read environment: %w", err)
	}
	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) validate() error {
	switch c.Env {
	case "local", "testing", "production":
	default:
		return fmt.Errorf("APP_ENV must be local, testing or production, got %q", c.Env)
	}
	if c.HTTPAddr == "" {
		return fmt.Errorf("HTTP_ADDR must not be empty")
	}
	var level slog.Level
	if err := level.UnmarshalText([]byte(c.LogLevel)); err != nil {
		return fmt.Errorf("LOG_LEVEL must be debug, info, warn or error, got %q", c.LogLevel)
	}
	if c.LogFormat != "json" && c.LogFormat != "text" {
		return fmt.Errorf("LOG_FORMAT must be json or text, got %q", c.LogFormat)
	}
	if c.TermsVersion == "" {
		return fmt.Errorf("TERMS_VERSION must not be empty")
	}
	for _, origin := range c.CORSAllowedOrigins {
		if err := checkOrigin(origin); err != nil {
			return fmt.Errorf("CORS_ALLOWED_ORIGINS: %w", err)
		}
	}
	if err := c.validateDurations(); err != nil {
		return err
	}
	return c.validateBackends()
}

func (c Config) validateDurations() error {
	for name, d := range map[string]time.Duration{
		"HTTP_REQUEST_TIMEOUT":   c.RequestTimeout,
		"SHUTDOWN_TIMEOUT":       c.ShutdownTimeout,
		"ACCESS_TOKEN_TTL":       c.AccessTokenTTL,
		"REFRESH_TOKEN_TTL":      c.RefreshTokenTTL,
		"PERMISSION_CACHE_TTL":   c.PermissionCacheTTL,
		"EMAIL_VERIFICATION_TTL": c.EmailVerificationTTL,
		"EMAIL_RESEND_COOLDOWN":  c.EmailResendCooldown,
	} {
		if d <= 0 {
			return fmt.Errorf("%s must be positive, got %s", name, d)
		}
	}
	for name, d := range map[string]time.Duration{
		"JWT_LEEWAY":          c.JWTLeeway,
		"REFRESH_REUSE_GRACE": c.RefreshReuseGrace,
	} {
		if d < 0 {
			return fmt.Errorf("%s must not be negative, got %s", name, d)
		}
	}
	return nil
}

// validateBackends checks the database, Redis and token settings; the optional ones only when they are set.
func (c Config) validateBackends() error {
	if c.DBMaxConns < 1 {
		return fmt.Errorf("DB_MAX_CONNS must be at least 1, got %d", c.DBMaxConns)
	}
	for name, ms := range map[string]int{
		"DB_STATEMENT_TIMEOUT_MS":           c.DBStatementTimeoutMS,
		"DB_LOCK_TIMEOUT_MS":                c.DBLockTimeoutMS,
		"DB_IDLE_IN_TRANSACTION_TIMEOUT_MS": c.DBIdleInTransactionTimeoutMS,
	} {
		if ms <= 0 {
			return fmt.Errorf("%s must be positive, got %d", name, ms)
		}
	}
	if c.HasDatabase() {
		if err := checkURL(c.DatabaseURL, "postgres", "postgresql"); err != nil {
			return fmt.Errorf("DATABASE_URL: %w", err)
		}
	}
	if c.HasRedis() {
		if err := checkURL(c.RedisURL, "redis", "rediss"); err != nil {
			return fmt.Errorf("REDIS_URL: %w", err)
		}
	}
	if c.SMTPPort < 1 || c.SMTPPort > 65535 {
		return fmt.Errorf("SMTP_PORT must be between 1 and 65535, got %d", c.SMTPPort)
	}
	if err := checkOrigin(c.WebURL); err != nil {
		return fmt.Errorf("WEB_URL: %w", err)
	}
	if c.RedisKeyPrefix == "" {
		return fmt.Errorf("REDIS_KEY_PREFIX must not be empty")
	}
	if c.JWTIssuer == "" {
		return fmt.Errorf("JWT_ISSUER must not be empty")
	}
	if c.HasJWTSecret() && len(c.JWTSecret) < minJWTSecretBytes {
		return fmt.Errorf("JWT_SECRET must be at least %d bytes, got %d", minJWTSecretBytes, len(c.JWTSecret))
	}
	if c.SeedDemoPassword != "" && len(c.SeedDemoPassword) < minSeedPasswordBytes {
		return fmt.Errorf("SEED_DEMO_PASSWORD must be at least %d characters", minSeedPasswordBytes)
	}
	return nil
}

// checkURL requires a parseable URL with one of the schemes.
func checkURL(raw string, schemes ...string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("not a valid URL")
	}
	for _, s := range schemes {
		if u.Scheme == s {
			return nil
		}
	}
	return fmt.Errorf("scheme must be one of %v, got %q", schemes, u.Scheme)
}

// checkOrigin requires "scheme://host[:port]" with no path: that is what a browser sends in Origin.
func checkOrigin(origin string) error {
	u, err := url.Parse(origin)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || (u.Path != "" && u.Path != "/") || u.RawQuery != "" {
		return fmt.Errorf("%q is not an origin like http://localhost:2001", origin)
	}
	return nil
}
