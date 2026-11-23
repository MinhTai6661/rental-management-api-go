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
	"rental-management/internal/adapter/filestore"
	httpadapter "rental-management/internal/adapter/http"
	identityhttp "rental-management/internal/adapter/http/identity"
	locationhttp "rental-management/internal/adapter/http/location"
	propertyhttp "rental-management/internal/adapter/http/property"
	"rental-management/internal/adapter/imageproc"
	mailadapter "rental-management/internal/adapter/mail"
	"rental-management/internal/adapter/persistence/postgres"
	identitypg "rental-management/internal/adapter/persistence/postgres/identity"
	locationpg "rental-management/internal/adapter/persistence/postgres/location"
	propertypg "rental-management/internal/adapter/persistence/postgres/property"
	redisstore "rental-management/internal/adapter/redis"
	"rental-management/internal/adapter/security"
	usecase "rental-management/internal/usecase/identity"
	locationuc "rental-management/internal/usecase/location"
	propertyuc "rental-management/internal/usecase/property"
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
	authenticator := httpadapter.NewAuthenticator(auth)
	locations := locationuc.NewLocations(locationpg.NewLocationRepository(pool))

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
				func(v1 *gin.RouterGroup) {
					locationhttp.MountLocations(v1, locationhttp.NewLocationHandler(locations), limiter,
						locationhttp.DefaultLimit, locationhttp.DefaultWindow)
				},
				func(v1 *gin.RouterGroup) {
					identityhttp.MountAuth(v1, identityhttp.NewAuthHandler(auth, httpx.NewBinder(), limiter), authenticator, limiter)
				},
				func(v1 *gin.RouterGroup) {
					binder := httpx.NewBinder()
					propertyhttp.MountProperty(v1,
						propertyhttp.NewBuildingHandler(properties.buildings, files, binder),
						propertyhttp.NewRoomHandler(properties.rooms, files, binder),
						propertyhttp.NewMediaHandler(properties.media, files, binder, cfg.MediaMaxUploadBytes),
						authenticator, limiter)
				},
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

// newIdentity builds the account use cases on their adapters: Postgres, the revocation store in Redis, argon2id
// and JWT, and the mailer (SMTP, or the log when no SMTP server is set, which RequireAPI only allows outside
// production).
func newIdentity(cfg config.Config, log *slog.Logger, pool *pgxpool.Pool, rdb *redis.Client) (*usecase.Auth, error) {
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
	return usecase.NewAuth(usecase.Deps{
		Tx:            postgres.NewTxManager(pool),
		Users:         identitypg.NewUserRepository(pool),
		Sessions:      identitypg.NewSessionRepository(pool),
		Verifications: identitypg.NewVerificationRepository(pool),
		Permissions:   identitypg.NewPermissionRepository(pool),
		Revocations:   redisstore.NewRevocationStore(rdb, cfg.RedisKeyPrefix),
		Hasher:        security.PasswordHasher{},
		Tokens:        security.NewTokenIssuer(cfg.JWTSecret, cfg.JWTIssuer, cfg.AccessTokenTTL, cfg.JWTLeeway, time.Now),
		Opaque:        security.OpaqueTokens{},
		Mailer:        mailadapter.NewVerificationMailer(mailer, renderer, cfg.WebURL),
		Clock:         usecase.ClockFunc(time.Now),
		Log:           log,
	}, usecase.Options{
		TermsVersion:       cfg.TermsVersion,
		RefreshTTL:         cfg.RefreshTokenTTL,
		ReuseGrace:         cfg.RefreshReuseGrace,
		VerificationTTL:    cfg.EmailVerificationTTL,
		ResendCooldown:     cfg.EmailResendCooldown,
		RevokedFor:         cfg.AccessTokenTTL + cfg.JWTLeeway,
		PermissionCacheTTL: cfg.PermissionCacheTTL,
	})
}

// property is the use cases of buildings, rooms and pictures, on their adapters.
type property struct {
	buildings *propertyuc.Buildings
	rooms     *propertyuc.Rooms
	media     *propertyuc.Media
	seeder    *propertyuc.Seeder
}

// newProperty builds the property use cases on Postgres, the file store over files and the image processor. The
// plan, guard and observer ports keep their defaults (nothing limited, nothing blocking, nobody listening) until
// the contexts that own them are wired here.
func newProperty(cfg config.Config, log *slog.Logger, pool *pgxpool.Pool, files storage.Storage) property {
	deps := propertyuc.Deps{
		Tx:        postgres.NewTxManager(pool),
		Buildings: propertypg.NewBuildingRepository(pool),
		Rooms:     propertypg.NewRoomRepository(pool),
		Charges:   propertypg.NewChargeRepository(pool),
		Media:     propertypg.NewMediaRepository(pool),
		Files:     filestore.New(files),
		Images: imageproc.New(imaging.NewProcessor(imaging.Limits{
			MaxBytes: cfg.MediaMaxUploadBytes, MaxDimension: cfg.MediaMaxDimension, ThumbMaxSize: cfg.MediaThumbnailMaxSize,
		}, cfg.ImageProcessingConcurrency)),
		Clock: propertyuc.ClockFunc(time.Now),
		Log:   log,
	}
	opts := propertyuc.Options{
		MaxMediaPerOwner: cfg.MediaMaxPerOwner, MaxUploadBytes: cfg.MediaMaxUploadBytes, MaxDimension: cfg.MediaMaxDimension,
	}
	buildings, rooms := propertyuc.NewBuildings(deps, opts), propertyuc.NewRooms(deps, opts)
	return property{
		buildings: buildings, rooms: rooms, media: propertyuc.NewMedia(deps, opts), seeder: propertyuc.NewSeeder(buildings, rooms),
	}
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
