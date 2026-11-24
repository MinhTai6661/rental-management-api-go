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

func TestMediaDefaults(t *testing.T) {
	cfg, err := parse(map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MediaDir != "var/media" || cfg.MediaPublicBaseURL != "http://localhost:8090/v1/media" ||
		cfg.MediaMaxUploadBytes != 2097152 || cfg.MediaMaxDimension != 2048 || cfg.MediaThumbnailMaxSize != 480 ||
		cfg.MediaMaxPerOwner != 10 || cfg.ImageProcessingConcurrency != 2 {
		t.Errorf("unexpected media defaults: %+v", cfg)
	}
}

func TestMediaRejectsInvalidValues(t *testing.T) {
	cases := map[string]map[string]string{
		"relative base url":   {"MEDIA_PUBLIC_BASE_URL": "/v1/media"},
		"ftp base url":        {"MEDIA_PUBLIC_BASE_URL": "ftp://cdn.example/media"},
		"trailing slash":      {"MEDIA_PUBLIC_BASE_URL": "http://localhost:8090/v1/media/"},
		"base url with query": {"MEDIA_PUBLIC_BASE_URL": "http://localhost:8090/v1/media?x=1"},
		"empty dir":           {"MEDIA_DIR": ""},
		"zero upload":         {"MEDIA_MAX_UPLOAD_BYTES": "0"},
		"zero dimension":      {"MEDIA_MAX_DIMENSION": "0"},
		"negative thumbnail":  {"MEDIA_THUMBNAIL_MAX_SIZE": "-1"},
		"zero per owner":      {"MEDIA_MAX_PER_OWNER": "0"},
		"zero concurrency":    {"IMAGE_PROCESSING_CONCURRENCY": "0"},
	}
	for name, vars := range cases {
		t.Run(name, func(t *testing.T) {
			// An empty value falls back to the default, so the empty-dir case needs the dir to be a blank string.
			if name == "empty dir" {
				vars = map[string]string{"MEDIA_DIR": " "}
			}
			if _, err := parse(vars); err == nil {
				t.Errorf("expected an error for %v", vars)
			}
		})
	}
	if _, err := parse(map[string]string{"MEDIA_PUBLIC_BASE_URL": "https://cdn.example.com/media"}); err != nil {
		t.Errorf("a valid https base url was refused: %v", err)
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
	for _, name := range []string{"DATABASE_URL", "REDIS_URL", "JWT_SECRET", "DATA_ENCRYPTION_KEY"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("the error should name %s: %v", name, err)
		}
	}

	full := map[string]string{
		"DATABASE_URL": "postgres://u@localhost:5442/app?sslmode=disable",
		"REDIS_URL":    "redis://localhost:6389/0",
		"JWT_SECRET":   secret,

		"DATA_ENCRYPTION_KEY": dataKey,
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

func TestStorageDefaultsToLocalDisk(t *testing.T) {
	cfg, err := parse(map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.StorageDriver != "local" || cfg.S3Region != "auto" || !cfg.S3PathStyle {
		t.Errorf("unexpected storage defaults: %+v", cfg)
	}
}

func TestS3SettingsAreRequiredOnlyForTheS3Driver(t *testing.T) {
	full := map[string]string{
		"STORAGE_DRIVER":       "s3",
		"S3_ENDPOINT":          "https://acct.r2.cloudflarestorage.com",
		"S3_BUCKET":            "pictures",
		"S3_ACCESS_KEY_ID":     "key-id",
		"S3_SECRET_ACCESS_KEY": "very-secret-value",
	}
	cfg, err := parse(full)
	if err != nil {
		t.Fatalf("complete s3 settings: %v", err)
	}
	if cfg.S3Region != "auto" || !cfg.S3PathStyle {
		t.Errorf("s3 defaults: %+v", cfg)
	}

	for _, name := range []string{"S3_ENDPOINT", "S3_BUCKET", "S3_ACCESS_KEY_ID", "S3_SECRET_ACCESS_KEY"} {
		vars := map[string]string{}
		for k, v := range full {
			if k != name {
				vars[k] = v
			}
		}
		_, err := parse(vars)
		if err == nil || !strings.Contains(err.Error(), name) {
			t.Errorf("without %s the error must name it: %v", name, err)
		}
	}

	bad := map[string]map[string]string{
		"unknown driver":     {"STORAGE_DRIVER": "gcs"},
		"endpoint no scheme": {"STORAGE_DRIVER": "s3", "S3_ENDPOINT": "localhost:8333", "S3_BUCKET": "b", "S3_ACCESS_KEY_ID": "k", "S3_SECRET_ACCESS_KEY": "very-secret-value"},
		"endpoint with path": {"STORAGE_DRIVER": "s3", "S3_ENDPOINT": "http://localhost:8333/b", "S3_BUCKET": "b", "S3_ACCESS_KEY_ID": "k", "S3_SECRET_ACCESS_KEY": "very-secret-value"},
	}
	for name, vars := range bad {
		if _, err := parse(vars); err == nil {
			t.Errorf("%s: expected an error", name)
		} else if strings.Contains(err.Error(), "very-secret-value") {
			t.Errorf("%s: the error leaks the secret: %v", name, err)
		}
	}

	// Settings of the other driver are not needed (and not checked) while the driver is local.
	if _, err := parse(map[string]string{"S3_BUCKET": "x"}); err != nil {
		t.Errorf("local driver with a stray S3 setting: %v", err)
	}
}

// dataKey is 32 bytes in standard base64.
const dataKey = "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="

func TestDataKeyAndInvoiceSettings(t *testing.T) {
	cfg, err := parse(map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HasDataKey() || cfg.InvoiceDueDays != 7 || cfg.InvoiceShareTTLDays != 90 || cfg.InvoiceReminderDays != 3 || cfg.InvitationTTLDays != 7 {
		t.Errorf("unexpected defaults: %+v", cfg)
	}
	if _, err := cfg.DataKey(); err == nil {
		t.Error("a missing key is an error")
	}

	cfg, err = parse(map[string]string{"DATA_ENCRYPTION_KEY": dataKey, "INVOICE_DUE_DAYS": "10"})
	if err != nil {
		t.Fatal(err)
	}
	if key, err := cfg.DataKey(); err != nil || len(key) != 32 || cfg.InvoiceDueDays != 10 {
		t.Errorf("key %d bytes, %v", len(key), err)
	}

	for name, vars := range map[string]map[string]string{
		"not base64":          {"DATA_ENCRYPTION_KEY": "***"},
		"too short":           {"DATA_ENCRYPTION_KEY": "c2hvcnQ="},
		"zero due days":       {"INVOICE_DUE_DAYS": "0"},
		"huge share ttl":      {"INVOICE_SHARE_TTL_DAYS": "99999"},
		"zero reminder":       {"INVOICE_REMINDER_DAYS": "0"},
		"zero invitation ttl": {"INVITATION_TTL_DAYS": "0"},
		"huge invitation ttl": {"INVITATION_TTL_DAYS": "91"},
	} {
		_, err := parse(vars)
		if err == nil {
			t.Errorf("%s must be refused", name)
		} else if strings.Contains(err.Error(), "***") || strings.Contains(err.Error(), "c2hvcnQ=") {
			t.Errorf("%s: the error must not repeat the key: %v", name, err)
		}
	}
}

func TestNotificationRetention(t *testing.T) {
	cfg, err := parse(map[string]string{})
	if err != nil || cfg.NotificationRetentionDays != 90 {
		t.Fatalf("default 90 days expected: %d %v", cfg.NotificationRetentionDays, err)
	}
	if cfg, err = parse(map[string]string{"NOTIFICATION_RETENTION_DAYS": "30"}); err != nil || cfg.NotificationRetentionDays != 30 {
		t.Errorf("30 days expected: %d %v", cfg.NotificationRetentionDays, err)
	}
	for _, v := range []string{"0", "-1", "3651"} {
		if _, err := parse(map[string]string{"NOTIFICATION_RETENTION_DAYS": v}); err == nil {
			t.Errorf("%s must be refused", v)
		}
	}
}

func TestPaymentProofSettings(t *testing.T) {
	cfg, err := parse(map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PaymentProofMaxFiles != 3 || cfg.PaymentProofMaxSizeKB != 1024 || cfg.PaymentProofURLTTLMinutes != 120 || cfg.APIPublicURL != "http://localhost:8090" {
		t.Errorf("unexpected defaults: %+v", cfg)
	}
	for name, vars := range map[string]map[string]string{
		"no files":         {"PAYMENT_PROOF_MAX_FILES": "0"},
		"too many files":   {"PAYMENT_PROOF_MAX_FILES": "11"},
		"tiny size":        {"PAYMENT_PROOF_MAX_SIZE_KB": "1"},
		"huge size":        {"PAYMENT_PROOF_MAX_SIZE_KB": "99999"},
		"zero link ttl":    {"PAYMENT_PROOF_URL_TTL_MINUTES": "0"},
		"huge link ttl":    {"PAYMENT_PROOF_URL_TTL_MINUTES": "5000"},
		"relative api url": {"API_PUBLIC_URL": "api.example.vn"},
		"slash on api url": {"API_PUBLIC_URL": "https://api.example.vn/"},
	} {
		if _, err := parse(vars); err == nil {
			t.Errorf("%s must be refused", name)
		}
	}
}

func TestAnnouncementImageLimits(t *testing.T) {
	cfg, err := parse(map[string]string{})
	if err != nil || cfg.AnnouncementImageMaxPlatform != 10 || cfg.AnnouncementImageMaxLandlord != 3 {
		t.Fatalf("defaults 10 and 3 expected: %+v %v", cfg, err)
	}
	for _, v := range []string{"0", "21", "-1"} {
		if _, err := parse(map[string]string{"ANNOUNCEMENT_IMAGE_MAX_LANDLORD": v}); err == nil {
			t.Errorf("%s must be refused", v)
		}
	}
}

func TestRealtimeSettings(t *testing.T) {
	cfg, err := parse(map[string]string{})
	if err != nil || cfg.RealtimeTicketTTL != time.Minute || cfg.RealtimeHeartbeat != 25*time.Second ||
		cfg.RealtimeStreamMaxAge != 15*time.Minute || cfg.RealtimeMaxStreamsPerUser != 5 {
		t.Fatalf("defaults 60s, 25s, 15m and 5 expected: %+v %v", cfg, err)
	}
	for name, vars := range map[string]map[string]string{
		"zero ticket ttl":  {"REALTIME_TICKET_TTL": "0s"},
		"zero heartbeat":   {"REALTIME_HEARTBEAT": "0s"},
		"zero max age":     {"REALTIME_STREAM_MAX_AGE": "0s"},
		"no streams":       {"REALTIME_MAX_STREAMS_PER_USER": "0"},
		"too many streams": {"REALTIME_MAX_STREAMS_PER_USER": "101"},
	} {
		if _, err := parse(vars); err == nil {
			t.Errorf("%s must be refused", name)
		}
	}
}

func TestWorkerSettings(t *testing.T) {
	cfg, err := parse(map[string]string{})
	if err != nil || cfg.WorkerTick != 30*time.Second || cfg.WorkerDispatchInterval != time.Minute || cfg.WorkerDailyInterval != 0 {
		t.Fatalf("defaults 30s, 1m and 0 expected: %+v %v", cfg, err)
	}
	for name, vars := range map[string]map[string]string{
		"zero tick":      {"WORKER_TICK": "0s"},
		"zero dispatch":  {"WORKER_DISPATCH_INTERVAL": "0s"},
		"negative daily": {"WORKER_DAILY_INTERVAL": "-1s"},
	} {
		if _, err := parse(vars); err == nil {
			t.Errorf("%s must be refused", name)
		}
	}
	if cfg, err := parse(map[string]string{"WORKER_DAILY_INTERVAL": "2m"}); err != nil || cfg.WorkerDailyInterval != 2*time.Minute {
		t.Fatalf("a daily interval must be accepted: %+v %v", cfg, err)
	}
}
