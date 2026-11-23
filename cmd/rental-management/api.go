// Package app wires the pieces together and runs the process. Nothing here is global: every dependency
// is built in the Run functions and handed down.
package main

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
	contracthttp "rental-management/internal/adapter/http/contract"
	"rental-management/internal/adapter/http/httpx"
	identityhttp "rental-management/internal/adapter/http/identity"
	invitationhttp "rental-management/internal/adapter/http/invitation"
	invoicehttp "rental-management/internal/adapter/http/invoice"
	listinghttp "rental-management/internal/adapter/http/listing"
	locationhttp "rental-management/internal/adapter/http/location"
	paymenthttp "rental-management/internal/adapter/http/payment"
	planhttp "rental-management/internal/adapter/http/plan"
	propertyhttp "rental-management/internal/adapter/http/property"
	"rental-management/internal/adapter/imageproc"
	mailadapter "rental-management/internal/adapter/mail"
	"rental-management/internal/adapter/persistence/postgres"
	contractpg "rental-management/internal/adapter/persistence/postgres/contract"
	identitypg "rental-management/internal/adapter/persistence/postgres/identity"
	invitationpg "rental-management/internal/adapter/persistence/postgres/invitation"
	invoicepg "rental-management/internal/adapter/persistence/postgres/invoice"
	listingpg "rental-management/internal/adapter/persistence/postgres/listing"
	locationpg "rental-management/internal/adapter/persistence/postgres/location"
	planpg "rental-management/internal/adapter/persistence/postgres/plan"
	propertypg "rental-management/internal/adapter/persistence/postgres/property"
	redisstore "rental-management/internal/adapter/redis"
	"rental-management/internal/adapter/security"
	contractuc "rental-management/internal/usecase/contract"
	usecase "rental-management/internal/usecase/identity"
	invitationuc "rental-management/internal/usecase/invitation"
	invoiceuc "rental-management/internal/usecase/invoice"
	listinguc "rental-management/internal/usecase/listing"
	locationuc "rental-management/internal/usecase/location"
	planuc "rental-management/internal/usecase/plan"
	propertyuc "rental-management/internal/usecase/property"
	"rental-management/pkg/db"
	"rental-management/pkg/i18n"
	"rental-management/pkg/imaging"
	"rental-management/pkg/logger"
	"rental-management/pkg/mail"
	"rental-management/pkg/ratelimit"
	"rental-management/pkg/secretbox"
	"rental-management/pkg/storage"
)

// runAPI serves the HTTP API until ctx is cancelled, then drains in-flight requests.
func runAPI(ctx context.Context) error {
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

	files, closeFiles, err := openStorage(cfg)
	if err != nil {
		return err
	}
	defer closeFiles()
	pricing := newPlans(log, pool)
	offers := newListings(log, pool, pricing)
	sealer, err := newSealer(cfg)
	if err != nil {
		return err
	}
	invitees, err := newInvitees(cfg, log, pool)
	if err != nil {
		return err
	}
	mailer, renderer, err := newMailer(cfg, log)
	if err != nil {
		return err
	}
	rentals := newContracts(cfg, log, pool, pricing, invitationPorts{
		invitees: invitees, sealer: sealer, mailer: invitationMailer{m: mailadapter.NewInvitationMailer(mailer, renderer, invitees.OptOutURL)},
	})
	properties := newProperty(cfg, log, pool, files, rentals, offers, pricing)
	billing, err := newInvoices(cfg, log, pool, pricing, sealer)
	if err != nil {
		return err
	}
	paying, err := newPayments(cfg, log, pool, files, sealer, billing)
	if err != nil {
		return err
	}

	srv := &http.Server{
		Addr: cfg.HTTPAddr,
		Handler: newRouter(routerDeps{
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
				func(v1 *gin.RouterGroup) {
					contracthttp.MountContracts(v1,
						contracthttp.NewContractHandler(rentals.contracts, files, httpx.NewBinder()),
						contracthttp.NewRentalHandler(rentals.rentals, files),
						contracthttp.NewInvitationHandler(rentals.invitations, rentals.answers, contractuc.ClockFunc(time.Now), files, httpx.NewBinder()),
						authenticator, limiter)
				},
				func(v1 *gin.RouterGroup) {
					binder := httpx.NewBinder()
					listinghttp.MountListings(v1,
						listinghttp.NewListingHandler(offers.listings, files, binder),
						listinghttp.NewReviewHandler(offers.reviews, files, binder),
						authenticator, limiter)
					listinghttp.MountSearch(v1, listinghttp.NewOfferHandler(offers.offers, files), authenticator, limiter)
				},
				func(v1 *gin.RouterGroup) {
					invoicehttp.MountInvoices(v1,
						invoicehttp.NewInvoiceHandler(billing.invoices, httpx.NewBinder(), invoiceuc.ClockFunc(time.Now), nil),
						invoicehttp.NewMeterHandler(billing.invoices), invoicehttp.NewPublicHandler(billing.public),
						authenticator, limiter)
				},
				func(v1 *gin.RouterGroup) {
					binder := httpx.NewBinder()
					paymenthttp.MountPayments(v1,
						paymenthttp.NewPaymentHandler(paying.payments, binder, paymenthttp.Limits{MaxFiles: paying.limits.maxFiles, MaxBytes: paying.limits.maxBytes}),
						paymenthttp.NewMethodHandler(paying.methods, binder), paymenthttp.NewProofHandler(paying.payments),
						authenticator, limiter)
				},
				func(v1 *gin.RouterGroup) {
					invitationhttp.MountInvitees(v1, invitationhttp.NewInviteeHandler(invitees, httpx.NewBinder()), authenticator, limiter)
				},
				func(v1 *gin.RouterGroup) {
					planhttp.MountPlans(v1, planhttp.NewPlanHandler(pricing.catalog, pricing.subscriptions, httpx.NewBinder()),
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
	mailer, renderer, err := newMailer(cfg, log)
	if err != nil {
		return nil, err
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

// newMailer builds the sender of emails (SMTP, or the log when no SMTP server is set, which RequireAPI only allows
// outside production) and the renderer of their templates.
func newMailer(cfg config.Config, log *slog.Logger) (mail.Mailer, *mail.Renderer, error) {
	renderer, err := mail.NewRenderer()
	if err != nil {
		return nil, nil, err
	}
	var mailer mail.Mailer = mail.NewLog(log)
	if cfg.HasSMTP() {
		mailer = &mail.SMTP{
			Addr: net.JoinHostPort(cfg.SMTPHost, strconv.Itoa(cfg.SMTPPort)), From: cfg.SMTPFrom,
			Username: cfg.SMTPUsername, Password: cfg.SMTPPassword,
		}
	}
	return mailer, renderer, nil
}

// plans is the use cases of service plans and subscriptions, on Postgres. Property and contract read the limits of a
// plan through planLimits (see bridges.go).
type plans struct {
	catalog       *planuc.Plans
	subscriptions *planuc.Subscriptions
	limits        *planuc.Limits
	seeder        *planuc.Seeder
}

// newPlans builds the plan use cases. The notification and audit ports keep their no-op defaults until those contexts
// exist.
func newPlans(log *slog.Logger, pool *pgxpool.Pool) plans {
	deps := planuc.Deps{
		Tx: postgres.NewTxManager(pool), Plans: planpg.NewPlanRepository(pool), Subs: planpg.NewSubscriptionRepository(pool),
		Accounts: planpg.NewAccounts(pool), Usage: planpg.NewUsage(pool), Clock: planuc.ClockFunc(time.Now), Log: log,
	}
	return plans{
		catalog: planuc.NewPlans(deps), subscriptions: planuc.NewSubscriptions(deps), limits: planuc.NewLimits(deps),
		seeder: planuc.NewSeeder(deps),
	}
}

// listings is the use cases of listings and of the search of find rooms, on Postgres. Property reaches them through
// the guard and the observer in bridges.go.
type listings struct {
	listings *listinguc.Listings
	reviews  *listinguc.Reviews
	offers   *listinguc.Offers
	links    *listinguc.RoomLinks
	seeder   *listinguc.Seeder
}

// newListings builds the listing use cases. The search priority comes from the plan use cases; no plan limits the
// number of listings yet and the notification and audit ports keep their no-op defaults until those contexts exist.
func newListings(log *slog.Logger, pool *pgxpool.Pool, p plans) listings {
	rooms := listingpg.NewRoomReader(pool)
	deps := listinguc.Deps{
		Tx: postgres.NewTxManager(pool), Listings: listingpg.NewListingRepository(pool), Rooms: rooms,
		Offers: listingpg.NewOfferRepository(pool), Seed: rooms, Priority: searchPriority{limits: p.limits},
		Clock: listinguc.ClockFunc(time.Now), Log: log,
	}
	return listings{
		listings: listinguc.NewListings(deps), reviews: listinguc.NewReviews(deps), offers: listinguc.NewOffers(deps),
		links: listinguc.NewRoomLinks(deps), seeder: listinguc.NewSeeder(deps),
	}
}

// invoices is the use cases of the bills of rental contracts, on Postgres.
type invoices struct {
	invoices   *invoiceuc.Invoices
	public     *invoiceuc.Public
	automation *invoiceuc.Automation
	seeder     *invoiceuc.Seeder
	// ledger answers the money questions of the bills with the payment use cases (see bridges_payment.go).
	ledger *paymentLedger
}

// newSealer builds the box that encrypts the secrets kept to show a link again (bills, invitations) with
// DATA_ENCRYPTION_KEY. It is built once and shared: a random nonce per seal makes one key safe for every purpose.
func newSealer(cfg config.Config) (*secretbox.Box, error) {
	key, err := cfg.DataKey()
	if err != nil {
		return nil, err
	}
	return secretbox.New(key)
}

// newInvitees builds the invitation use cases on Postgres: the accounts are a read-only view, the opt-out links are
// signed with a key derived from DATA_ENCRYPTION_KEY, and the audit port keeps its no-op default until that context
// exists.
func newInvitees(cfg config.Config, log *slog.Logger, pool *pgxpool.Pool) (*invitationuc.Invitees, error) {
	key, err := cfg.DataKey()
	if err != nil {
		return nil, err
	}
	signer, err := security.NewOptOutSigner(key)
	if err != nil {
		return nil, err
	}
	return invitationuc.NewInvitees(invitationuc.Deps{
		People: invitationpg.NewPeopleRepository(pool), Blocks: invitationpg.NewEmailBlockRepository(pool), Signer: signer, Log: log,
	}, invitationuc.Options{WebURL: cfg.WebURL}), nil
}

// newInvoices builds the invoice use cases on Postgres. The contracts and the room charges are read-only views in the
// invoice database adapter, the plan feature (automatic invoicing) comes from the plan use cases (see bridges.go), the
// secrets of share links are encrypted with the sealer, the payment ledger is the payment use cases' (see
// bridges_payment.go; newPayments gives it its file store), and the notification, audit and mail ports keep their
// no-op defaults until those contexts exist.
func newInvoices(cfg config.Config, log *slog.Logger, pool *pgxpool.Pool, p plans, sealer *secretbox.Box) (invoices, error) {
	ledger := newPaymentLedger(cfg, log, pool)
	deps := invoiceuc.Deps{
		Tx: postgres.NewTxManager(pool), Invoices: invoicepg.NewInvoiceRepository(pool), Contracts: invoicepg.NewContractReader(pool),
		Charges: invoicepg.NewChargeReader(pool), Plans: planFeatures{limits: p.limits}, Tokens: security.ShareTokens{}, Sealer: sealer,
		Ledger: ledger, Clock: invoiceuc.ClockFunc(time.Now), Log: log,
	}
	opts := invoiceuc.Options{
		DueDays: cfg.InvoiceDueDays, ShareTTL: time.Duration(cfg.InvoiceShareTTLDays) * 24 * time.Hour,
		ReminderDays: cfg.InvoiceReminderDays, WebURL: cfg.WebURL,
	}
	return invoices{
		invoices: invoiceuc.NewInvoices(deps, opts), public: invoiceuc.NewPublic(deps, opts),
		automation: invoiceuc.NewAutomation(deps, opts), seeder: invoiceuc.NewSeeder(deps, opts), ledger: ledger,
	}, nil
}

// contracts is the use cases of rental contracts, on their adapters.
type contracts struct {
	contracts   *contractuc.Contracts
	rentals     *contractuc.Rentals
	invitations *contractuc.Invitations
	answers     *contractuc.InvitationAnswers
	links       *contractuc.RoomLinks
	seeder      *contractuc.Seeder
	rooms       *roomStatus
}

// invitationPorts is what the contract invitations need from outside the contract context: who can be invited, the box
// that keeps the links, and the sender of the invitation email.
type invitationPorts struct {
	invitees *invitationuc.Invitees
	sealer   *secretbox.Box
	mailer   contractuc.InvitationMailer
}

// newContracts builds the contract use cases on Postgres. The room status goes through property's use case, which is
// built after this one (see newProperty); the plan limits come from the plan use cases; the invitees come from the
// invitation use cases (see bridges.go); the notification and audit ports keep their no-op defaults until those
// contexts exist.
func newContracts(cfg config.Config, log *slog.Logger, pool *pgxpool.Pool, p plans, inv invitationPorts) contracts {
	status := &roomStatus{}
	reader := contractpg.NewRoomReader(pool)
	deps := contractuc.Deps{
		Tx: postgres.NewTxManager(pool), Contracts: contractpg.NewContractRepository(pool), Rooms: reader, RoomState: status,
		Plans: planLimits{limits: p.limits}, Clock: contractuc.ClockFunc(time.Now), Log: log,
		Invitations: contractpg.NewInvitationRepository(pool), Invitees: inv.invitees, Tokens: security.ShareTokens{},
		Sealer: inv.sealer, Mailer: inv.mailer,
	}
	opts := contractuc.Options{InvitationTTL: time.Duration(cfg.InvitationTTLDays) * 24 * time.Hour, WebURL: cfg.WebURL}
	return contracts{
		contracts: contractuc.NewContracts(deps, opts), rentals: contractuc.NewRentals(deps, opts),
		invitations: contractuc.NewInvitations(deps, opts), answers: contractuc.NewInvitationAnswers(deps, opts), links: contractuc.NewRoomLinks(deps, opts),
		seeder: contractuc.NewSeeder(deps, opts, reader), rooms: status,
	}
}

// property is the use cases of buildings, rooms and pictures, on their adapters.
type property struct {
	buildings *propertyuc.Buildings
	rooms     *propertyuc.Rooms
	media     *propertyuc.Media
	seeder    *propertyuc.Seeder
}

// newProperty builds the property use cases on Postgres, the file store over files and the image processor. The room
// guards and the room observer are the contract and listing contexts' (combined) and the plan limits are the plan
// context's (see bridges.go).
func newProperty(cfg config.Config, log *slog.Logger, pool *pgxpool.Pool, files storage.Storage, c contracts, l listings, p plans) property {
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
		Guards:   roomGuards{contractGuards{links: c.links}, listingGuards{links: l.links}},
		Observer: roomObservers{contractObserver{links: c.links}, listingObserver{links: l.links}},
		Plans:    planLimits{limits: p.limits},
		Clock:    propertyuc.ClockFunc(time.Now),
		Log:      log,
	}
	opts := propertyuc.Options{
		MaxMediaPerOwner: cfg.MediaMaxPerOwner, MaxUploadBytes: cfg.MediaMaxUploadBytes, MaxDimension: cfg.MediaMaxDimension,
	}
	buildings, rooms := propertyuc.NewBuildings(deps, opts), propertyuc.NewRooms(deps, opts)
	c.rooms.rooms = rooms // contracts rent and free rooms through the use case just built
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
