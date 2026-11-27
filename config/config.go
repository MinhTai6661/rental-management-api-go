// Package config reads the process environment into one validated struct.
// The binary never reads a .env file: the environment is set by compose or the shell.
//
// DATABASE_URL, REDIS_URL and JWT_SECRET are optional in the struct: when set they are validated, but nothing
// fails here because they are missing. The code that needs them checks when it starts: RequireDatabase for
// `rental-management migrate|seed`, RequireAPI (database, Redis, the signing key and the data key) for `rental-management api`.
package config

import (
	"fmt"
	"log/slog"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/caarlos0/env/v11"

	"rental-management/pkg/secretbox"
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

	// Social login. A provider is on only when both its id and its secret are set; the redirect URL is the callback
	// of this API registered with the provider (http(s)://<api>/v1/auth/<provider>/callback). The secrets are never logged.
	GoogleClientID     string `env:"GOOGLE_CLIENT_ID"`
	GoogleClientSecret string `env:"GOOGLE_CLIENT_SECRET"`
	GoogleRedirectURL  string `env:"GOOGLE_REDIRECT_URL"`
	ZaloAppID          string `env:"ZALO_APP_ID"`
	ZaloAppSecret      string `env:"ZALO_APP_SECRET"`
	ZaloRedirectURL    string `env:"ZALO_REDIRECT_URL"`
	// SocialStateTTL is how long a round trip to a provider may take; SocialLoginCodeTTL the life of the one-time
	// code the web app trades for tokens after it.
	SocialStateTTL     time.Duration `env:"SOCIAL_STATE_TTL" envDefault:"10m"`
	SocialLoginCodeTTL time.Duration `env:"SOCIAL_LOGIN_CODE_TTL" envDefault:"60s"`

	// Media: pictures of rooms and buildings. MediaDir is where the files live (a mounted volume in the container),
	// MediaPublicBaseURL the absolute prefix of every picture URL the API returns.
	MediaDir                   string `env:"MEDIA_DIR" envDefault:"var/media"`
	MediaPublicBaseURL         string `env:"MEDIA_PUBLIC_BASE_URL" envDefault:"http://localhost:8090/v1/media"`
	MediaMaxUploadBytes        int    `env:"MEDIA_MAX_UPLOAD_BYTES" envDefault:"2097152"`
	MediaMaxDimension          int    `env:"MEDIA_MAX_DIMENSION" envDefault:"2048"`
	MediaThumbnailMaxSize      int    `env:"MEDIA_THUMBNAIL_MAX_SIZE" envDefault:"480"`
	MediaMaxPerOwner           int    `env:"MEDIA_MAX_PER_OWNER" envDefault:"10"`
	ImageProcessingConcurrency int    `env:"IMAGE_PROCESSING_CONCURRENCY" envDefault:"2"`

	// StorageDriver chooses where the picture files live: "local" (MediaDir) or "s3" (an S3-compatible bucket:
	// Cloudflare R2, AWS S3, SeaweedFS...). The S3_* settings are only read, and required, when it is "s3".
	StorageDriver     string `env:"STORAGE_DRIVER" envDefault:"local"`
	S3Endpoint        string `env:"S3_ENDPOINT"`
	S3Bucket          string `env:"S3_BUCKET"`
	S3AccessKeyID     string `env:"S3_ACCESS_KEY_ID"`
	S3SecretAccessKey string `env:"S3_SECRET_ACCESS_KEY"`
	S3Region          string `env:"S3_REGION" envDefault:"auto"`
	S3PathStyle       bool   `env:"S3_PATH_STYLE" envDefault:"true"`

	// DataEncryptionKey encrypts the secrets that must be read back later (the link of a bill, so the landlord can copy
	// it again): 32 random bytes in standard base64, e.g. `openssl rand -base64 32`. Losing it makes the links already
	// made unreadable (new ones are made when asked for); changing it does the same.
	DataEncryptionKey string `env:"DATA_ENCRYPTION_KEY"`

	// Bills: how many days after today a bill falls due when the landlord gives no date, how long the link of a bill
	// works, and how many days before the due date the reminder goes out.
	InvoiceDueDays      int `env:"INVOICE_DUE_DAYS" envDefault:"7"`
	InvoiceShareTTLDays int `env:"INVOICE_SHARE_TTL_DAYS" envDefault:"90"`
	InvoiceReminderDays int `env:"INVOICE_REMINDER_DAYS" envDefault:"3"`

	// Payments: how many proof pictures (screenshots of the transfer) a payment may carry, how large each may be after
	// the web app has shrunk it (KB), how long the signed link to a proof works (minutes), and the public address of the
	// API the links of proofs point at.
	PaymentProofMaxFiles      int    `env:"PAYMENT_PROOF_MAX_FILES" envDefault:"3"`
	PaymentProofMaxSizeKB     int    `env:"PAYMENT_PROOF_MAX_SIZE_KB" envDefault:"1024"`
	PaymentProofURLTTLMinutes int    `env:"PAYMENT_PROOF_URL_TTL_MINUTES" envDefault:"120"`
	APIPublicURL              string `env:"API_PUBLIC_URL" envDefault:"http://localhost:8090"`

	// InvitationTTLDays is how long an invitation (a room offered by link) can be answered; 1 to 90.
	InvitationTTLDays int `env:"INVITATION_TTL_DAYS" envDefault:"7"`

	// NotificationRetentionDays is how long a notification is kept before `notifications-prune` deletes it; 1 to 3650.
	NotificationRetentionDays int `env:"NOTIFICATION_RETENTION_DAYS" envDefault:"90"`

	// AuditImportantRetentionMonths and AuditRoutineRetentionDays are how long the two tiers of the audit trail are kept
	// (`audit-maintain` drops the monthly partitions past that); AuditPartitionsAhead is how many months after the
	// current one get their partition made in advance.
	AuditImportantRetentionMonths int `env:"AUDIT_IMPORTANT_RETENTION_MONTHS" envDefault:"24"`
	AuditRoutineRetentionDays     int `env:"AUDIT_ROUTINE_RETENTION_DAYS" envDefault:"90"`
	AuditPartitionsAhead          int `env:"AUDIT_PARTITIONS_AHEAD" envDefault:"3"`

	// StatsCacheSeconds is how long a landlord's figures are kept in the cache before they are worked out again; 0 turns
	// the cache off, at most 86400. A change shows only when the answer expires.
	StatsCacheSeconds int `env:"STATS_CACHE_SECONDS" envDefault:"900"`

	// AnnouncementImageMaxPlatform and AnnouncementImageMaxLandlord are how many pictures a popup of the platform's admins
	// and of a landlord may have; 1 to 20.
	AnnouncementImageMaxPlatform int `env:"ANNOUNCEMENT_IMAGE_MAX_PLATFORM" envDefault:"10"`
	AnnouncementImageMaxLandlord int `env:"ANNOUNCEMENT_IMAGE_MAX_LANDLORD" envDefault:"3"`

	// Realtime (server-sent events): how long a stream ticket works, how often an idle stream gets a ping, how long one
	// stream lasts before the client must reconnect, and how many streams one user may hold open on one instance.
	RealtimeTicketTTL         time.Duration `env:"REALTIME_TICKET_TTL" envDefault:"60s"`
	RealtimeHeartbeat         time.Duration `env:"REALTIME_HEARTBEAT" envDefault:"25s"`
	RealtimeStreamMaxAge      time.Duration `env:"REALTIME_STREAM_MAX_AGE" envDefault:"15m"`
	RealtimeMaxStreamsPerUser int           `env:"REALTIME_MAX_STREAMS_PER_USER" envDefault:"5"`

	// Report conversations (WebSocket, GET /v1/reports/{id}/ws): the largest frame a client may send, how many frames it
	// may send in a minute, how many sockets one user and one report may hold open on one instance, and how long a write may
	// take. The heartbeat, the ticket and the lifetime of a socket are the REALTIME_* ones above.
	ReportWSMaxMessageBytes     int           `env:"REPORT_WS_MAX_MESSAGE_BYTES" envDefault:"8192"`
	ReportWSMessagesPerMinute   int           `env:"REPORT_WS_MESSAGES_PER_MINUTE" envDefault:"60"`
	ReportWSMaxSocketsPerUser   int           `env:"REPORT_WS_MAX_SOCKETS_PER_USER" envDefault:"10"`
	ReportWSMaxSocketsPerReport int           `env:"REPORT_WS_MAX_SOCKETS_PER_REPORT" envDefault:"10"`
	ReportWSWriteTimeout        time.Duration `env:"REPORT_WS_WRITE_TIMEOUT" envDefault:"10s"`

	// The worker (`rental-management worker`): how often it looks at its jobs, how often it dispatches due announcements,
	// and (when above zero) an interval that replaces the daily time of day of the daily jobs, for tests and demos.
	WorkerTick             time.Duration `env:"WORKER_TICK" envDefault:"30s"`
	WorkerDispatchInterval time.Duration `env:"WORKER_DISPATCH_INTERVAL" envDefault:"1m"`
	WorkerDailyInterval    time.Duration `env:"WORKER_DAILY_INTERVAL" envDefault:"0s"`

	// SeedDemoPassword is the password of the demo accounts `seed -demo` creates (development only).
	SeedDemoPassword string `env:"SEED_DEMO_PASSWORD"`
	// SeedSamplePassword, when set, makes `seed -demo` also create the sample accounts (`@rental.com`) with it. Empty
	// by default: their addresses are written down in the project docs, so they only exist where somebody asks.
	SeedSamplePassword string `env:"SEED_SAMPLE_PASSWORD"`
}

// maxInvitationTTLDays is the longest an invitation may stay open.
const maxInvitationTTLDays = 90

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
	if !c.HasDataKey() {
		missing = append(missing, "DATA_ENCRYPTION_KEY")
	}
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

// HasDataKey reports whether DATA_ENCRYPTION_KEY is set.
func (c Config) HasDataKey() bool { return c.DataEncryptionKey != "" }

// DataKey returns the decoded DATA_ENCRYPTION_KEY (validated when the configuration was read).
func (c Config) DataKey() ([]byte, error) {
	if !c.HasDataKey() {
		return nil, fmt.Errorf("DATA_ENCRYPTION_KEY is required")
	}
	return secretbox.ParseKey(c.DataEncryptionKey)
}

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
	if err := c.validateSocial(); err != nil {
		return err
	}
	return c.validateBackends()
}

func (c Config) validateDurations() error {
	for name, d := range map[string]time.Duration{
		"HTTP_REQUEST_TIMEOUT":     c.RequestTimeout,
		"SHUTDOWN_TIMEOUT":         c.ShutdownTimeout,
		"ACCESS_TOKEN_TTL":         c.AccessTokenTTL,
		"REFRESH_TOKEN_TTL":        c.RefreshTokenTTL,
		"PERMISSION_CACHE_TTL":     c.PermissionCacheTTL,
		"EMAIL_VERIFICATION_TTL":   c.EmailVerificationTTL,
		"EMAIL_RESEND_COOLDOWN":    c.EmailResendCooldown,
		"SOCIAL_STATE_TTL":         c.SocialStateTTL,
		"SOCIAL_LOGIN_CODE_TTL":    c.SocialLoginCodeTTL,
		"REALTIME_TICKET_TTL":      c.RealtimeTicketTTL,
		"REALTIME_HEARTBEAT":       c.RealtimeHeartbeat,
		"REALTIME_STREAM_MAX_AGE":  c.RealtimeStreamMaxAge,
		"WORKER_TICK":              c.WorkerTick,
		"WORKER_DISPATCH_INTERVAL": c.WorkerDispatchInterval,
	} {
		if d <= 0 {
			return fmt.Errorf("%s must be positive, got %s", name, d)
		}
	}
	for name, d := range map[string]time.Duration{
		"JWT_LEEWAY":            c.JWTLeeway,
		"REFRESH_REUSE_GRACE":   c.RefreshReuseGrace,
		"WORKER_DAILY_INTERVAL": c.WorkerDailyInterval,
	} {
		if d < 0 {
			return fmt.Errorf("%s must not be negative, got %s", name, d)
		}
	}
	return nil
}

// GoogleEnabled reports whether signing in with Google is configured.
func (c Config) GoogleEnabled() bool { return c.GoogleClientID != "" && c.GoogleClientSecret != "" }

// ZaloEnabled reports whether signing in with Zalo is configured.
func (c Config) ZaloEnabled() bool { return c.ZaloAppID != "" && c.ZaloAppSecret != "" }

// validateSocial checks the providers: one with only half of its credentials is a mistake that would silently switch
// it off, and one that is on needs the callback URL registered with the provider.
func (c Config) validateSocial() error {
	for _, p := range []struct{ name, id, secret, redirect string }{
		{"GOOGLE", c.GoogleClientID, c.GoogleClientSecret, c.GoogleRedirectURL},
		{"ZALO", c.ZaloAppID, c.ZaloAppSecret, c.ZaloRedirectURL},
	} {
		if (p.id == "") != (p.secret == "") {
			return fmt.Errorf("%s: set both the id and the secret, or neither", p.name)
		}
		if p.id == "" {
			continue
		}
		if err := checkURL(p.redirect, "http", "https"); err != nil {
			return fmt.Errorf("%s_REDIRECT_URL: %w", p.name, err)
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
	if err := c.validateMedia(); err != nil {
		return err
	}
	if c.HasDataKey() {
		if _, err := secretbox.ParseKey(c.DataEncryptionKey); err != nil {
			return fmt.Errorf("DATA_ENCRYPTION_KEY %w", err)
		}
	}
	for name, n := range map[string]int{
		"INVOICE_DUE_DAYS": c.InvoiceDueDays, "INVOICE_SHARE_TTL_DAYS": c.InvoiceShareTTLDays, "INVOICE_REMINDER_DAYS": c.InvoiceReminderDays,
	} {
		if n < 1 || n > 3650 {
			return fmt.Errorf("%s must be between 1 and 3650, got %d", name, n)
		}
	}
	if err := c.validatePayments(); err != nil {
		return err
	}
	if c.InvitationTTLDays < 1 || c.InvitationTTLDays > maxInvitationTTLDays {
		return fmt.Errorf("INVITATION_TTL_DAYS must be between 1 and %d, got %d", maxInvitationTTLDays, c.InvitationTTLDays)
	}
	if c.AuditImportantRetentionMonths < 1 || c.AuditImportantRetentionMonths > 240 {
		return fmt.Errorf("AUDIT_IMPORTANT_RETENTION_MONTHS must be between 1 and 240, got %d", c.AuditImportantRetentionMonths)
	}
	if c.AuditRoutineRetentionDays < 1 || c.AuditRoutineRetentionDays > 3650 {
		return fmt.Errorf("AUDIT_ROUTINE_RETENTION_DAYS must be between 1 and 3650, got %d", c.AuditRoutineRetentionDays)
	}
	if c.AuditPartitionsAhead < 1 || c.AuditPartitionsAhead > 24 {
		return fmt.Errorf("AUDIT_PARTITIONS_AHEAD must be between 1 and 24, got %d", c.AuditPartitionsAhead)
	}
	if c.StatsCacheSeconds < 0 || c.StatsCacheSeconds > 86400 {
		return fmt.Errorf("STATS_CACHE_SECONDS must be between 0 and 86400, got %d", c.StatsCacheSeconds)
	}
	if c.NotificationRetentionDays < 1 || c.NotificationRetentionDays > 3650 {
		return fmt.Errorf("NOTIFICATION_RETENTION_DAYS must be between 1 and 3650, got %d", c.NotificationRetentionDays)
	}
	for name, n := range map[string]int{
		"ANNOUNCEMENT_IMAGE_MAX_PLATFORM": c.AnnouncementImageMaxPlatform, "ANNOUNCEMENT_IMAGE_MAX_LANDLORD": c.AnnouncementImageMaxLandlord,
	} {
		if n < 1 || n > 20 {
			return fmt.Errorf("%s must be between 1 and 20, got %d", name, n)
		}
	}
	if c.RealtimeMaxStreamsPerUser < 1 || c.RealtimeMaxStreamsPerUser > 100 {
		return fmt.Errorf("REALTIME_MAX_STREAMS_PER_USER must be between 1 and 100, got %d", c.RealtimeMaxStreamsPerUser)
	}
	for name, bounds := range map[string][3]int{
		"REPORT_WS_MAX_MESSAGE_BYTES":      {c.ReportWSMaxMessageBytes, 512, 1 << 20},
		"REPORT_WS_MESSAGES_PER_MINUTE":    {c.ReportWSMessagesPerMinute, 1, 6000},
		"REPORT_WS_MAX_SOCKETS_PER_USER":   {c.ReportWSMaxSocketsPerUser, 1, 100},
		"REPORT_WS_MAX_SOCKETS_PER_REPORT": {c.ReportWSMaxSocketsPerReport, 1, 100},
	} {
		if bounds[0] < bounds[1] || bounds[0] > bounds[2] {
			return fmt.Errorf("%s must be between %d and %d, got %d", name, bounds[1], bounds[2], bounds[0])
		}
	}
	if c.ReportWSWriteTimeout < time.Second || c.ReportWSWriteTimeout > time.Minute {
		return fmt.Errorf("REPORT_WS_WRITE_TIMEOUT must be between 1s and 1m, got %s", c.ReportWSWriteTimeout)
	}
	if c.SeedDemoPassword != "" && len(c.SeedDemoPassword) < minSeedPasswordBytes {
		return fmt.Errorf("SEED_DEMO_PASSWORD must be at least %d characters", minSeedPasswordBytes)
	}
	if c.SeedSamplePassword != "" && len(c.SeedSamplePassword) < minSeedPasswordBytes {
		return fmt.Errorf("SEED_SAMPLE_PASSWORD must be at least %d characters", minSeedPasswordBytes)
	}
	return nil
}

// validatePayments checks the settings of the payment proofs.
func (c Config) validatePayments() error {
	if c.PaymentProofMaxFiles < 1 || c.PaymentProofMaxFiles > 10 {
		return fmt.Errorf("PAYMENT_PROOF_MAX_FILES must be between 1 and 10, got %d", c.PaymentProofMaxFiles)
	}
	if c.PaymentProofMaxSizeKB < 16 || c.PaymentProofMaxSizeKB > 10240 {
		return fmt.Errorf("PAYMENT_PROOF_MAX_SIZE_KB must be between 16 and 10240, got %d", c.PaymentProofMaxSizeKB)
	}
	if c.PaymentProofURLTTLMinutes < 1 || c.PaymentProofURLTTLMinutes > 1440 {
		return fmt.Errorf("PAYMENT_PROOF_URL_TTL_MINUTES must be between 1 and 1440, got %d", c.PaymentProofURLTTLMinutes)
	}
	if err := checkBaseURL(c.APIPublicURL); err != nil {
		return fmt.Errorf("API_PUBLIC_URL: %w", err)
	}
	return nil
}

// validateMedia checks the picture settings.
func (c Config) validateMedia() error {
	if strings.TrimSpace(c.MediaDir) == "" {
		return fmt.Errorf("MEDIA_DIR must not be empty")
	}
	if err := checkBaseURL(c.MediaPublicBaseURL); err != nil {
		return fmt.Errorf("MEDIA_PUBLIC_BASE_URL: %w", err)
	}
	for name, n := range map[string]int{
		"MEDIA_MAX_UPLOAD_BYTES":       c.MediaMaxUploadBytes,
		"MEDIA_MAX_DIMENSION":          c.MediaMaxDimension,
		"MEDIA_THUMBNAIL_MAX_SIZE":     c.MediaThumbnailMaxSize,
		"MEDIA_MAX_PER_OWNER":          c.MediaMaxPerOwner,
		"IMAGE_PROCESSING_CONCURRENCY": c.ImageProcessingConcurrency,
	} {
		if n <= 0 {
			return fmt.Errorf("%s must be positive, got %d", name, n)
		}
	}
	return c.validateStorage()
}

// validateStorage checks the storage driver; the S3 settings are required only for the s3 driver. The messages
// name variables, never values, so the secret key cannot end up in a log.
func (c Config) validateStorage() error {
	switch c.StorageDriver {
	case "local":
		return nil
	case "s3":
	default:
		return fmt.Errorf("STORAGE_DRIVER must be local or s3, got %q", c.StorageDriver)
	}
	var missing []string
	for name, value := range map[string]string{
		"S3_ENDPOINT":          c.S3Endpoint,
		"S3_BUCKET":            c.S3Bucket,
		"S3_ACCESS_KEY_ID":     c.S3AccessKeyID,
		"S3_SECRET_ACCESS_KEY": c.S3SecretAccessKey,
		"S3_REGION":            c.S3Region,
	} {
		if strings.TrimSpace(value) == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("STORAGE_DRIVER=s3 needs %s", strings.Join(missing, ", "))
	}
	if u, err := url.Parse(c.S3Endpoint); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" ||
		(u.Path != "" && u.Path != "/") || u.RawQuery != "" {
		return fmt.Errorf("S3_ENDPOINT is not an http(s) address like http://localhost:8333 or https://<account>.r2.cloudflarestorage.com")
	}
	return nil
}

// checkBaseURL requires an absolute http(s) URL with no query and no trailing slash; a path is allowed.
func checkBaseURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("%q is not an absolute http(s) URL like http://localhost:8090/v1/media", raw)
	}
	if strings.HasSuffix(raw, "/") {
		return fmt.Errorf("%q must not end with a slash", raw)
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
