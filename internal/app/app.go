// Package app wires the pieces together and runs the process. Nothing here is global: every dependency
// is built in the Run functions and handed down.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"rental-management/config"
	"rental-management/internal/handler"
	"rental-management/internal/modules/location"
	"rental-management/internal/modules/property"
	"rental-management/internal/repository"
	"rental-management/internal/router"
	"rental-management/internal/service"
	"rental-management/pkg/db"
	"rental-management/pkg/httpx"
	"rental-management/pkg/i18n"
	"rental-management/pkg/imaging"
	"rental-management/pkg/logger"
	"rental-management/pkg/mail"
	"rental-management/pkg/ratelimit"
	"rental-management/pkg/storage"
)

// RunAPI serves the HTTP API until ctx is cancelled, then drains in-flight requests.
func RunAPI(ctx context.Context) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if err := cfg.RequireAPI(); err != nil {
		return err
	}
	log, err := logger.New(os.Stdout, cfg.LogLevel, cfg.LogFormat)
	if err != nil {
		return err
	}
	catalog, err := i18n.New()
	if err != nil {
		return err
	}
	if cfg.Env == "production" {
		gin.SetMode(gin.ReleaseMode)
	}

	pool, err := openPool(ctx, cfg, cfg.DBStatementTimeoutMS)
	if err != nil {
		return err
	}
	defer pool.Close()

	rdb, err := openRedis(ctx, cfg)
	if err != nil {
		return err
	}
	defer func() { _ = rdb.Close() }()
	ready := []func(context.Context) error{
		pool.Ping,
		func(ctx context.Context) error { return rdb.Ping(ctx).Err() },
	}
	limiter := ratelimit.NewRedis(rdb, cfg.RedisKeyPrefix)

	auth, err := newIdentity(cfg, log, pool, rdb)
	if err != nil {
		return err
	}

	files, err := storage.NewLocalDisk(cfg.MediaDir, cfg.MediaPublicBaseURL)
	if err != nil {
		return err
	}
	properties := newProperty(cfg, log, pool, files)

	srv := &http.Server{
		Addr: cfg.HTTPAddr,
		Handler: NewRouter(Deps{
			Log:            log,
			Catalog:        catalog,
			RequestTimeout: cfg.RequestTimeout,
			CORSOrigins:    cfg.CORSAllowedOrigins,
			Ready:          ready,
			Modules: []func(*gin.RouterGroup){
				location.Mount(location.NewStore(pool), location.Options{
					Limiter: limiter, Limit: location.DefaultLimit, Window: location.DefaultWindow,
				}),
				func(v1 *gin.RouterGroup) {
					router.MountAuth(v1, handler.NewAuthHandler(auth, httpx.NewBinder(), limiter), auth, limiter)
				},
				property.Mount(properties, property.RouteOptions{Auth: auth, Limiter: limiter}),
			},
		}),
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      cfg.RequestTimeout + 5*time.Second, // room to write the 503 of a timed-out request
		IdleTimeout:       60 * time.Second,
	}

	serveErr := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", cfg.HTTPAddr, "env", cfg.Env, "smtp", cfg.HasSMTP())
		serveErr <- srv.ListenAndServe()
	}()

	select {
	case err := <-serveErr:
		return fmt.Errorf("serve: %w", err)
	case <-ctx.Done():
	}

	log.Info("shutting down")
	stopCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(stopCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("shutdown: %w", err)
	}
	auth.Wait() // let the emails still being sent finish
	return nil
}

// newIdentity builds the account service: tokens, revocations in Redis, and the mailer (SMTP, or the log when no
// SMTP server is set, which RequireAPI only allows outside production).
func newIdentity(cfg config.Config, log *slog.Logger, pool *pgxpool.Pool, rdb *redis.Client) (*service.AuthService, error) {
	renderer, err := mail.NewRenderer()
	if err != nil {
		return nil, err
	}
	var mailer mail.Mailer = mail.NewLog(log)
	if cfg.HasSMTP() {
		mailer = &mail.SMTP{
			Addr: net.JoinHostPort(cfg.SMTPHost, strconv.Itoa(cfg.SMTPPort)), From: cfg.SMTPFrom,
			Username: cfg.SMTPUsername, Password: cfg.SMTPPassword,
		}
	}
	return service.NewAuthService(service.AuthDeps{
		Tx:            repository.NewTxManager(pool),
		Users:         repository.NewUserRepository(pool),
		Sessions:      repository.NewSessionRepository(pool),
		Verifications: repository.NewVerificationRepository(pool),
		Permissions:   repository.NewPermissionRepository(pool),
		Revocations:   repository.NewRevocationRepository(rdb, cfg.RedisKeyPrefix),
		Tokens:        service.NewTokenIssuer(cfg.JWTSecret, cfg.JWTIssuer, cfg.AccessTokenTTL, cfg.JWTLeeway, time.Now),
		Mailer:        mailer,
		Renderer:      renderer,
		Log:           log,
		Now:           time.Now,
	}, service.AuthOptions{
		TermsVersion:       cfg.TermsVersion,
		WebURL:             cfg.WebURL,
		RefreshTTL:         cfg.RefreshTokenTTL,
		ReuseGrace:         cfg.RefreshReuseGrace,
		VerificationTTL:    cfg.EmailVerificationTTL,
		ResendCooldown:     cfg.EmailResendCooldown,
		RevokedFor:         cfg.AccessTokenTTL + cfg.JWTLeeway,
		PermissionCacheTTL: cfg.PermissionCacheTTL,
	})
}

// newProperty builds the buildings and rooms service. The plan, guard and observer ports keep their defaults
// (nothing limited, nothing blocking, nobody listening) until the modules that own them are wired here.
func newProperty(cfg config.Config, log *slog.Logger, pool *pgxpool.Pool, files storage.Storage) *property.Service {
	return property.NewService(property.Deps{
		Store:   property.NewStore(pool),
		Storage: files,
		Images: imaging.NewProcessor(imaging.Limits{
			MaxBytes: cfg.MediaMaxUploadBytes, MaxDimension: cfg.MediaMaxDimension, ThumbMaxSize: cfg.MediaThumbnailMaxSize,
		}, cfg.ImageProcessingConcurrency),
		Log: log,
		Now: time.Now,
	}, property.Options{
		MaxMediaPerOwner: cfg.MediaMaxPerOwner, MaxUploadBytes: cfg.MediaMaxUploadBytes, MaxDimension: cfg.MediaMaxDimension,
	})
}

// openPool opens the Postgres pool with the configured limits; statementTimeoutMS overrides the per-query limit
// (the migrate and seed commands pass 0 for none).
func openPool(ctx context.Context, cfg config.Config, statementTimeoutMS int) (*pgxpool.Pool, error) {
	return db.Open(ctx, db.Config{
		URL:                        cfg.DatabaseURL,
		MaxConns:                   cfg.DBMaxConns,
		StatementTimeoutMS:         statementTimeoutMS,
		LockTimeoutMS:              cfg.DBLockTimeoutMS,
		IdleInTransactionTimeoutMS: cfg.DBIdleInTransactionTimeoutMS,
	})
}

// openRedis connects to REDIS_URL and checks that the server answers.
func openRedis(ctx context.Context, cfg config.Config) (*redis.Client, error) {
	opts, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		return nil, fmt.Errorf("parse REDIS_URL: %w", err)
	}
	rdb := redis.NewClient(opts)
	if err := rdb.Ping(ctx).Err(); err != nil {
		_ = rdb.Close()
		return nil, fmt.Errorf("ping redis: %w", err)
	}
	return rdb, nil
}
