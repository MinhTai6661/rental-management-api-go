# rental-management-api-go

Go backend of the rental-management project (multi-tenant boarding-house rental), written from scratch as a **clean
break**: it keeps the business rules of the Laravel API, which stays in this monorepo as a *business reference only*,
and shares nothing with it - not the database, not the tokens, not the JSON shapes, no compatibility layer.

Status: **phases 1 to 5** - platform, location (provinces/wards), identity (register, email verification, login,
refresh, logout, role switch, password change), property (buildings, rooms, pictures), contract (registrations,
rental contracts, cancellation requests, blank contracts), plan (service plans, subscriptions, the limits they set) and
listing (a landlord's advertisement of a room, the admins' review, the search of find rooms for renters and guests).
Later phases are listed in the [roadmap](#roadmap).

Stack: [Gin](https://gin-gonic.com) (HTTP) - [pgx](https://github.com/jackc/pgx) + [sqlc](https://sqlc.dev) +
[goose](https://github.com/pressly/goose) (Postgres 18) - go-redis (revoked tokens) +
[redis_rate](https://github.com/go-redis/redis_rate) (rate limits) - [go-mail](https://github.com/wneessen/go-mail)
(SMTP) - [go-i18n](https://github.com/nicksnyder/go-i18n) + `x/text/language` (vi/en messages, Accept-Language) -
`log/slog` - argon2id + JWT (golang-jwt) - testcontainers (integration tests). Go 1.27.

## Quick start

Nothing needs to be installed except Docker: every Go tool (compiler, sqlc, golangci-lint, air) runs in a container
through the `Makefile`.

```bash
make up          # api (air live reload), postgres, redis, mailpit
make migrate     # apply db/migrations to the dev database
make seed-demo   # roles, permissions, service plans, provinces, wards + the demo accounts and their data (safe to repeat)
curl localhost:8090/v1/provinces
```

Demo accounts (development only; password `demo-password-123`, set in `compose.yaml`): `superadmin@rental.test`,
`admin@rental.test`, `landlord@rental.test` (roles landlord + tenant), `tenant@rental.test`. Verification emails
show up in Mailpit at <http://localhost:8026>.

The dev stack shares nothing with the Laravel stack (`myrent_*`), so both can run together:

| Service  | Host port          | In the compose network | Laravel stack uses |
|----------|--------------------|------------------------|--------------------|
| api      | 8090               | `api:8080`             | 8000               |
| postgres | 5442               | `postgres:5432` (db `rental_management_dev`, user `rental_management`) | 5432 |
| redis    | 6389               | `redis:6379`           | 6379               |
| mailpit  | SMTP 1026, UI 8026 | `mailpit:1025`         | 1025, 8025         |

(Laravel also publishes 8080 for its websocket server.) `API_PORT` and `POSTGRES_PORT` can override the published
ports of the api and postgres in compose. No image is built for development: the api runs from the official `golang`
image with the source mounted. The `Dockerfile` (distroless, static binary) is for deployment and is built by hand:
`docker build -t rental-management-api-go:local .`; the same image runs `migrate up` and `seed` (migrations and the
locations snapshot are embedded). It has no `HEALTHCHECK` (no shell in distroless): probe `GET /healthz` (liveness)
and `GET /readyz` (readiness, pings Postgres and Redis) from the orchestrator.

## Configuration

The binary reads the **process environment only**, never a `.env` file. `.env.example` lists every variable; the
compose stack sets working development defaults itself. Durations use Go syntax (`15m`, `720h`).

| Variable | Default | Notes |
|---|---|---|
| `APP_ENV` | `local` | `local`, `testing` or `production` |
| `HTTP_ADDR` | `:8080` | |
| `HTTP_REQUEST_TIMEOUT` | `30s` | per-request deadline; exceeded = 503 `server.busy` |
| `SHUTDOWN_TIMEOUT` | `15s` | graceful shutdown |
| `LOG_LEVEL` / `LOG_FORMAT` | `info` / `json` | `debug`..`error`; `json` or `text` |
| `CORS_ALLOWED_ORIGINS` | `http://localhost:2001` | comma separated origins; no wildcard, no path |
| `TERMS_VERSION` | `2026-11-03` | changing it makes everybody accept the terms again |
| `DATABASE_URL` | - | **required** by `api`, `migrate`, `seed` |
| `DB_MAX_CONNS` | `10` | |
| `DB_STATEMENT_TIMEOUT_MS` / `DB_LOCK_TIMEOUT_MS` / `DB_IDLE_IN_TRANSACTION_TIMEOUT_MS` | `30000` / `10000` / `60000` | |
| `REDIS_URL` | - | **required** by `api` |
| `REDIS_KEY_PREFIX` | `rental-management:` | |
| `JWT_SECRET` | - | **required** by `api`, at least 32 bytes |
| `JWT_ISSUER` | `rental-management` | |
| `JWT_LEEWAY` | `5s` | clock skew allowed when checking `exp`/`iat` |
| `ACCESS_TOKEN_TTL` | `15m` | |
| `REFRESH_TOKEN_TTL` | `720h` | |
| `REFRESH_REUSE_GRACE` | `10s` | see [auth model](#auth-model) |
| `PERMISSION_CACHE_TTL` | `60s` | in-process cache of role -> permissions |
| `EMAIL_VERIFICATION_TTL` | `24h` | |
| `EMAIL_RESEND_COOLDOWN` | `60s` | |
| `GOOGLE_CLIENT_ID` / `GOOGLE_CLIENT_SECRET` / `GOOGLE_REDIRECT_URL` | - | social login with Google; **on only when both id and secret are set** (one without the other is a start-up error); the redirect URL (`<api>/v1/auth/google/callback`) is then required and must be http(s) |
| `ZALO_APP_ID` / `ZALO_APP_SECRET` / `ZALO_REDIRECT_URL` | - | the same for Zalo (`<api>/v1/auth/zalo/callback`); `ZALO_APP_ID` is the numeric App ID of developers.zalo.me |
| `SOCIAL_STATE_TTL` / `SOCIAL_LOGIN_CODE_TTL` | `10m` / `60s` | how long a round trip to a provider may take / the life of the one-time code the web trades for tokens. The secrets are never logged or echoed in an error |
| `WEB_URL` | `http://localhost:2001` | links in emails point here (`/verify-email?token=...`) |
| `SMTP_HOST` | - | empty = emails are only logged. **Required when `APP_ENV=production`**: otherwise the log would hold verification links |
| `SMTP_PORT` / `SMTP_USERNAME` / `SMTP_PASSWORD` | `1025` / - / - | |
| `SMTP_FROM` | `Rental Management <no-reply@rental.test>` | |
| `MEDIA_DIR` | `var/media` | where picture files live with the `local` driver (compose: volume at `/data/media`); also the source of `media-upload` |
| `MEDIA_PUBLIC_BASE_URL` | `http://localhost:8090/v1/media` | absolute prefix of every picture URL in responses; no trailing slash. With `s3` it can be the public address of the bucket |
| `STORAGE_DRIVER` | `local` | `local` keeps pictures under `MEDIA_DIR`; `s3` uses an S3-compatible bucket (see [Pictures in S3/R2](#pictures-in-s3r2)) |
| `S3_ENDPOINT` | - | **required when `s3`**: `http://localhost:8333` or `https://<account>.r2.cloudflarestorage.com`; the scheme decides TLS, no path |
| `S3_BUCKET` / `S3_ACCESS_KEY_ID` / `S3_SECRET_ACCESS_KEY` | - | **required when `s3`**; the secret is never logged or put in an error |
| `S3_REGION` | `auto` | `auto` for R2, `us-east-1` for SeaweedFS |
| `S3_PATH_STYLE` | `true` | `host/bucket/key` (SeaweedFS, MinIO); `false` = `bucket.host/key` |
| `MEDIA_MAX_UPLOAD_BYTES` / `MEDIA_MAX_DIMENSION` / `MEDIA_THUMBNAIL_MAX_SIZE` | `2097152` / `2048` / `480` | file bytes, longest side and thumbnail longest side (px) |
| `MEDIA_MAX_PER_OWNER` | `10` | pictures per room or building when the plan sets no limit |
| `IMAGE_PROCESSING_CONCURRENCY` | `2` | pictures decoded at the same time |
| `DATA_ENCRYPTION_KEY` | - | **required** by `api`, `seed -demo` and the invoice commands: base64 of 32 bytes; encrypts the secret of bill links so the landlord can copy the same link again, and the settings of the landlords' payment methods (where their money goes); a key derived from it (HKDF, own label) signs the proof links. Losing or changing it makes the links and the saved payment methods unreadable (the landlord saves the methods again) |
| `INVOICE_DUE_DAYS` / `INVOICE_SHARE_TTL_DAYS` / `INVOICE_REMINDER_DAYS` | `7` / `90` / `3` | days to the due date when the landlord gives none / how long a bill link works / how many days before the due date the reminder goes out (1-3650) |
| `PAYMENT_PROOF_MAX_FILES` / `PAYMENT_PROOF_MAX_SIZE_KB` | `3` / `1024` | proof pictures (screenshots of a transfer) a payment may carry (1-10) and the largest one in KB (16-10240, the web app shrinks them first) |
| `PAYMENT_PROOF_URL_TTL_MINUTES` | `120` | how long the signed link to a proof picture works (1-1440) |
| `API_PUBLIC_URL` | `http://localhost:8090` | public origin of this API (no trailing slash): the signed proof links are absolute and start with it |
| `INVITATION_TTL_DAYS` | `7` | how long an invitation to rent a room (a link the landlord sends) can be answered (1-90) |
| `ANNOUNCEMENT_IMAGE_MAX_PLATFORM` / `ANNOUNCEMENT_IMAGE_MAX_LANDLORD` | `10` / `3` | pictures one popup of the platform's admins / of a landlord may have (1-20) |
| `REALTIME_TICKET_TTL` / `REALTIME_HEARTBEAT` / `REALTIME_STREAM_MAX_AGE` | `60s` / `25s` / `15m` | how long a stream ticket works / how often an idle stream gets a `: ping` / how long one stream lasts before it ends with `expired` (keep it at most the access-token life: a revoked session loses its stream within one token life) |
| `WORKER_TICK` / `WORKER_DISPATCH_INTERVAL` / `WORKER_DAILY_INTERVAL` | `30s` / `1m` / `0s` | how often the worker looks at its jobs / how often it delivers due announcements / when above zero, an interval that replaces the daily times of the daily jobs (tests and demos only) |
| `REALTIME_MAX_STREAMS_PER_USER` | `5` | open event streams one person may hold on one API instance (1-100); the next one is a 429 |
| `REPORT_WS_MAX_MESSAGE_BYTES` | `8192` | the largest frame a client may send on a report conversation socket (512 to 1048576); a bigger one closes the socket with 1009 |
| `REPORT_WS_MESSAGES_PER_MINUTE` | `60` | frames (messages and typing) one socket may send in a minute (1-6000); past it the socket is closed with 1008 |
| `REPORT_WS_MAX_SOCKETS_PER_USER` / `REPORT_WS_MAX_SOCKETS_PER_REPORT` | `10` / `10` | conversation sockets one person / one report may hold on one API instance (1-100); the next one is a 429 |
| `REPORT_WS_WRITE_TIMEOUT` | `10s` | how long one write to a conversation socket may take (1s-1m); the ping, the ticket and the lifetime of a socket are the `REALTIME_*` values |
| `NOTIFICATION_RETENTION_DAYS` | `90` | how long a notification is kept before `notifications-prune` deletes it (1-3650) |
| `AUDIT_IMPORTANT_RETENTION_MONTHS` / `AUDIT_ROUTINE_RETENTION_DAYS` / `AUDIT_PARTITIONS_AHEAD` | `24` / `90` / `3` | how long the two tiers of the audit trail are kept (`audit-maintain` drops whole monthly partitions past it; 1-240 months / 1-3650 days) and how many months after the current one get a partition in advance (1-24) |
| `STATS_CACHE_SECONDS` | `900` | how long a landlord's statistics are kept in Redis before they are worked out again (0 = no cache, at most 86400); a change shows only when the answer expires (the platform's and the system's figures are never cached) |
| `SEED_DEMO_PASSWORD` | - | password of the `seed -demo` accounts; the command refuses to run in production |

## Pictures in S3/R2

By default pictures are files under `MEDIA_DIR`. Set `STORAGE_DRIVER=s3` (plus `S3_ENDPOINT`, `S3_BUCKET`,
`S3_ACCESS_KEY_ID`, `S3_SECRET_ACCESS_KEY`, `S3_REGION`, `S3_PATH_STYLE`) to keep them in any S3 API: Cloudflare R2,
AWS S3, SeaweedFS or MinIO. The API behaves the same; keys (`property/2026/11/<random>.webp`) are the object names.

- **Cloudflare R2**: `S3_ENDPOINT=https://<account>.r2.cloudflarestorage.com`, `S3_REGION=auto`, an R2 API token as the
  key pair. To let browsers fetch pictures straight from the bucket (and from Cloudflare's cache), attach a custom
  domain to the bucket and set `MEDIA_PUBLIC_BASE_URL=https://media.example.vn` (the key follows the slash, so the
  domain must serve the bucket root). Left on `https://<api>/v1/media`, pictures still go through the API.
- `GET /v1/media/*key` keeps working with every driver: it reads the object from the bucket and streams it (with
  `ETag`/304), so nothing has to be public.
- **Moving the existing pictures**: `STORAGE_DRIVER=s3 ... rental-management media-upload` (`make migrate-media` for
  the compose stack) walks `MEDIA_DIR` and uploads each picture under the same key. A key already in the bucket with
  the same size is skipped, so it is safe to run again; it prints the counts, exits non-zero when a file failed and
  never deletes the local files. It refuses to run unless `STORAGE_DRIVER=s3`. Upload first, then switch the API.
- **Local recipe (SeaweedFS in the compose stack)**: `make up`, `make migrate-media`, then uncomment the `STORAGE_DRIVER`
  and `S3_*` lines of the `api` service in `compose.yaml` and `docker compose up -d api`. The dev keys are in
  `docker/seaweedfs/s3.json`; the bucket `rental-management-media` is made by `seaweedfs-init`.
- Tests: the driver is tested against an in-process fake S3 (gofakes3). `go test -tags=integration ./pkg/storage` also
  runs it against a real endpoint when `S3_TEST_ENDPOINT`, `S3_TEST_BUCKET`, `S3_TEST_ACCESS_KEY_ID` and
  `S3_TEST_SECRET_ACCESS_KEY` are set (otherwise that test is skipped).

## Layout and dependency rules

```
cmd/rental-management/        the one binary: api | migrate [up|down|status] | seed [-demo] | media-upload | subscriptions-expire | invoices-send-automatic | invoices-send-reminders | notifications-prune | announcements-dispatch-due | audit-maintain | worker; also the composition root (api.go builds the dependencies, router.go the router, commands.go migrate/seed, bridges.go the adapters between contexts): the only place that knows every layer
db/migrations/                goose migrations, embedded in the binary
db/queries/<module>/          the SQL sqlc turns into Go (all of it generates into the one package internal/adapter/persistence/postgres/sqlc, committed, never edited by hand)
tools/fetchlocations/         one-off download of the provinces/wards snapshot (internal/adapter/snapshot/locationdata/locations.json.gz)

config/                       environment -> validated Config
pkg/                          INFRASTRUCTURE shared by every module; no business rules
  authz/                    pure authorization rules: roles, active-role resolution, permissions, Principal
  i18n/                     vi/en messages (go-i18n, embedded JSON) and Accept-Language negotiation (x/text/language)
  db/                       pgx pool, transactions, error helpers, goose runner
  schedule/                 the worker's scheduler: jobs on intervals or at a time of day, Locker and Marks ports, the Runner (stdlib ticker)
  ratelimit/                per-client limits on Redis (redis_rate, GCRA); the Limiter interface the middleware uses
  mail/                     Mailer port: SMTP (go-mail), log, test recorder; vi/en email templates
  logger/                   slog setup
  testdb/                   throwaway Postgres/Redis containers + the guard for integration tests

internal/
  domain/<context>/           enterprise rules: entities, enums, pure rules, typed errors; stdlib + uuid + pkg/authz only, no struct tags
    identity/                 accounts, sessions, tokens, role/permission catalog, refresh/verification/role rules
    location/                 provinces and wards, the seed snapshot and its validation, ErrProvinceNotFound
    property/                 buildings, rooms, charges and pictures: entities, read models, the pure rules (inheritance, room runs, reorder, charge sync, cover, arrangement) and typed errors
    plan/                     service plans and subscriptions: entities, the pure rules (limits where nil is unlimited, which account may take which plan, the effective plan with fail-open, subscription end dates) and typed errors; the base catalog and the demo assignments are data here
    listing/                  listings and find rooms: the statuses and the pure rules (which status can follow which, what an edit does, delete, review), the read models of the search (offers, landlord cards, plan priorities) and typed errors
    contract/                 rental contracts: entity and read models, the pure rules (status moves, closing date, rental period arithmetic and minimum period, cancellation requests, room status sync, reorder, abilities), the invitations (offers of a room by link: statuses, expiry, the secret and who wrote it) and typed errors; testdata/period-parity.json is the fixture shared with the web
    shared/                   errors every context uses: ErrNotFound, ConstraintError, ErrUnavailable
  usecase/<context>/          application rules: one file per use case, ports (interfaces) in ports.go, input/output structs without tags
    identity/                 Auth (register, login, refresh, logout, switch role, change password, me, authenticate, verify/resend email), Seeder
    location/                 Locations (list provinces, wards of a province), Seeder (load the snapshot)
    property/                 Buildings, Rooms, Media (one file per use case or small group; a building edit reaches its rooms in one transaction), Seeder (demo data); ports: repositories, FileStore, ImageProcessor, PlanLimits, RoomGuards, RoomObserver
    contract/                 Contracts (list, get, register, update, bulk answer, terminate, cancellation, delete, reorder), Rentals (the tenant's own list and detail), Invitations (the landlord's offers by link: create, list, get, copy the link, new link, withdraw), InvitationAnswers (the invited person's: preview, accept, decline, inbox; accepting shares `core.admit` with registration), RoomLinks (what property needs: blank contracts, locks, withdrawing the offers of deleted rooms), Seeder (+ the demo offer); ports: ContractRepository, InvitationRepository, Invitees, LinkTokens, Sealer, InvitationMailer, RoomReader, RoomStatusSetter, Notifier, AuditRecorder, PlanLimits
    listing/                  Listings (a landlord's own: list, get, create, update, submit, unpublish, delete), Reviews (the admins': list, get, approve, reject, take down, bulk), Offers (the search of find rooms and the landlord card), RoomLinks (what property needs: open listings, back to draft), Seeder; ports: ListingRepository, RoomReader, OfferRepository, SeedRooms, PlanLimits, SearchPriority, Notifier, AuditRecorder, Clock
    plan/                     Plans (the catalog: available, list, create, update), Subscriptions (mine, subscribe, cancel, assign, activate, revoke, search, expire), Limits (what the plan of an account allows, search priority), Seeder (base plans, demo subscriptions); ports: PlanRepository, SubscriptionRepository, Accounts, UsageReader, Notifier, AuditRecorder, Clock
  adapter/                    everything that talks to the outside world; implements the ports declared by the use cases
    http/                     (package httpadapter) what the HTTP features share: the Authenticator (+ identity error mapping) and views used by several features; never imports a feature folder
      httpx/                  envelope, errors, pagination, validation, middleware, CORS, auth and rate-limit middleware; a leaf (imports only pkg and apperr)
      apperr/                 the error type the handlers return + the error-code and validation-rule catalog; a leaf (imports nothing)
      identity/               (package identityhttp) auth handlers, routes, request/response bodies
      location/               (package locationhttp) public province and ward routes
      property/               (package propertyhttp) building, room and media handlers, routes, bodies, domain error -> apperr mapping, ETags
      contract/               (package contracthttp) contract, renter and invitation handlers, routes, bodies (partial update), domain error -> apperr mapping
      listing/                (package listinghttp) a landlord's listings, the review routes and the search routes (renters and guests), bodies (partial update), domain error -> apperr mapping
      plan/                   (package planhttp) catalog, own subscription, admin subscription and catalog handlers, routes, bodies (partial update), domain error -> apperr mapping
      report/                 (package reporthttp) the report routes, the conversation WebSocket (protocol, events publisher, socket handler; the only place that imports `gorilla/websocket`), domain error -> apperr mapping
      testenv/                response helpers of the HTTP integration tests, shared by the features
    persistence/postgres/     (package postgres) what the repositories share: TxManager, error translation (Translate), context-bound queries (Queries); never imports a feature folder
      listing/                (package listingpg) listing repository, the read-only views of rooms listings need, and the search of find rooms (offers, landlord cards)
      plan/                   (package planpg) plan and subscription repositories, and the read-only views of accounts and usage (rooms, buildings, contracts) the plan rules need
      identity/               (package identitypg) user, session, verification and permission repositories
      location/               (package locationpg) location repository
      property/               (package propertypg) building, room, charge and media repositories, row <-> domain mapping
      contract/               (package contractpg) contract and invitation repositories and the read-only room view contracts need, row <-> domain mapping
      sqlc/                   the one generated sqlc package (all queries of db/queries)
    redis/                    (package redisstore) the revoked tokens and sessions store; social.go: the one-use state of a round trip to a provider and the one-time login codes; realtime.go: the Pub/Sub broadcaster and the one-time stream tickets; report_realtime.go: the per-report Pub/Sub of conversations
    realtime/                 the in-memory hubs that hand server-sent events to the streams of a user, and the frames of a report's conversation to its sockets, open on this instance (stdlib only; `reports.go`)
    security/                 argon2id password hasher, JWT access tokens, opaque random tokens
    oauth/                    (package oauthadapter) the social login providers: Google on golang.org/x/oauth2 (the only importer, enforced by depguard), Zalo on a small net/http client
    mail/                     (package mailadapter) the verification email and the invitation email on top of pkg/mail
    snapshot/locationdata/    the provinces/wards snapshot embedded in the binary (locations.json.gz) and its decoder
    filestore/                the picture FileStore port on top of pkg/storage (local disk, S3/R2 or memory)
    imageproc/                the ImageProcessor port on top of pkg/imaging
```

Identity (accounts, sessions, tokens, passwords, email verification, role/permission catalog + seed) is the first
context moved to the clean architecture and is the template: `domain/identity/`, `usecase/identity/` (`ports.go`,
`register.go`, `login.go`, ...), `adapter/http/identity/`, `adapter/persistence/postgres/identity/`,
`adapter/redis/`, `adapter/security/`, `adapter/mail/`. Location (provinces and wards, public and rate limited,
cacheable; read-mostly, so thin) follows it: `domain/location/`, `usecase/location/` (`Locations` to list, `Seeder`
to load the snapshot), `adapter/http/location/`, `adapter/persistence/postgres/location/` and
`adapter/snapshot/locationdata/` (the embedded snapshot behind the `SnapshotSource` port). Property (buildings,
rooms, charges, pictures) is the last one moved, so every feature is in the layers: `domain/property/` (rules and
typed errors), `usecase/property/` (`Buildings`, `Rooms`, `Media`, `Seeder`), `adapter/http/property/`,
`adapter/persistence/postgres/property/`, `adapter/filestore/`
and `adapter/imageproc/`.

Contract (rental contracts) is the third context in the layers: `domain/contract/`, `usecase/contract/`,
`adapter/http/contract/` and `adapter/persistence/postgres/contract/`. **Contexts never import each other** (linter
rules `contract-does-not-know-property` and `property-does-not-know-contract`): property declares the ports
`RoomGuards` and `RoomObserver`, contract declares `RoomReader` and `RoomStatusSetter`, and `cmd/rental-management/bridges.go`
adapts each one's use cases to the other's ports (a room a tenant holds a running contract on is locked; a room that
becomes rented gets a blank contract; a contract that runs rents its room through property's use case).

Plan (service plans, subscriptions) is the fourth context: `domain/plan/`, `usecase/plan/`, `adapter/http/plan/` and
`adapter/persistence/postgres/plan/`. It imports no other context and none imports it (linter rules
`plan-does-not-know-other-contexts`, `identity-does-not-know-plan` and the property/contract ones). The limits a plan sets
reach the others through their own `PlanLimits` ports: `bridges.go` implements both (`planLimits`) on the `Limits` use
case, which asks the database on every call and caches nothing (a call made inside a transaction sees it). What the plan
context needs from the other contexts is read-only: its own database adapter reads `users`/`user_roles` (the roles an
account holds, with a row lock so one account's plans change one at a time) and counts `rooms`, `buildings` and
`contracts` for the usage figures.

Listing (a landlord's advertisement of a room) is the fifth context: `domain/listing/`, `usecase/listing/`,
`adapter/http/listing/` and `adapter/persistence/postgres/listing/`. It imports no other context and none imports it
(linter rules `listing-does-not-know-other-contexts` and a listing line in the others). `bridges.go` joins it to the rest:
property's `RoomGuards` and `RoomObserver` get a listing part (a room with a waiting or live listing cannot be deleted; a
change to the content of a room sends its waiting or live listing back to draft), the search priority of a landlord comes
from the plan use cases (`Limits.SearchPriorities`, one query for all the landlords of a page), and contract reads whether
a room has an approved listing through its own room view (a registration needs one). The listing's database adapter reads
`rooms`, `buildings`, `users`/`user_roles` and `contracts` read-only to decide what find rooms shows.

Invitation (the people side every kind of invitation shares) is a context of its own: `domain/invitation/`,
`usecase/invitation/`, `adapter/http/invitation/` and `adapter/persistence/postgres/invitation/`. It imports no other
context (linter rule `invitation-does-not-know-other-contexts`). The contexts that send invitations (contract now, the
room managers later) reach its **use cases** through a port of their own that `bridges.go` adapts, so the linter denies
`usecase/invitation` and its adapters everywhere else; but `domain/invitation` is a **shared kernel** (pure, no state, no
ports) that any context may import: the audience of an invitation and who may answer it (`AudienceOf`, `Allows`), who can
be invited (`Invitable`), `MaskEmail`, `NormalizeEmail` and `BlockHash`. Its database adapter reads `users`/`user_roles`
read-only and writes only `invitation_email_blocks`.

Payment (how a bill is paid and how a landlord is paid) is the eighth context: `domain/payment/`, `usecase/payment/`,
`adapter/http/payment/` and `adapter/persistence/postgres/payment/`. It imports no other context and none imports it (linter
rule `payment-does-not-know-other-contexts` and a payment line in the others). The invoice context and the payment context
meet **only in `cmd/rental-management/bridges_payment.go`**: invoice declares a `PaymentLedger` port (what is paid, what
waits for review, settle, void, purge) that the payment `Ledger` use case implements, and payment declares an `InvoiceStatus`
port (`MarkPaid`) that the invoice `SetStatus` use case implements, so the status rules, locks and audit of a bill stay in
invoice and the dependencies run invoice -> ledger port <- payment and payment -> status port <- invoice with no import
cycle. The payment database adapter reads `invoices`, `contracts` and `rooms` read-only (one facts query). Proof pictures use
the file and image adapters through the same kind of bridge (the adapters speak property's domain errors; the bridge turns
them into payment's), and `newInvoices` builds a ledger without a file store for the daily commands that `newPayments`
replaces with the full one in the API process.

Notification (the in-app inbox) is the ninth context: `domain/notification/`, `usecase/notification/`,
`adapter/http/notification/` (`notificationhttp`) and `adapter/persistence/postgres/notification/` (`notificationpg`). It is the
one place every context sends messages to, yet **it imports no other context and none imports it** (linter rules
`notification-does-not-know-other-contexts` and `other-contexts-do-not-know-notification`). Each context keeps its own `Notifier`
port and `Notification` struct; **`cmd/rental-management/bridges_notification.go`** adapts all five (`contractNotifier`,
`planNotifier`, `listingNotifier`, `invoiceNotifier`, `paymentNotifier`, ten lines each) to the one use case
`notificationuc.Deliveries` (`Send`, `SendToAdmins`), and a unit test there lists every `Notify*` constant of every context and
fails when one is not in the catalog. The catalog of kinds is **one typed set**, `notification.Kind` (`ParseKind`,
`DefaultAudience`), holding every kind of the Laravel API plus `announcement`, so later phases add no catalog entry; the database
only checks that a kind looks like `[a-z_]{1,50}`. `newNotifications` is built first in `runAPI` and its `Deliveries` is passed to
`newPlans`, `newListings`, `newContracts`, `newInvoices` and `newPayments` (a nil one, as the demo seed passes, leaves the
context's notifier at its no-op default, so the seed's demo flows stay silent).

Where does new code go? Business behaviour -> the layers above. A technical tool several features share (database,
mail, i18n...) -> `pkg/` (settings -> `config/`); HTTP-only helpers -> `adapter/http/httpx`. Nothing else goes in `internal/`.

Rules (checked in review and by the linters, see `.golangci.yml`):

- Direction: `adapter -> usecase -> domain`. Infrastructure is reached only through the interfaces the use case layer declares (`usecase/<context>/ports.go`); `cmd/rental-management` wires them. Everything may use `pkg` and `config`, never the reverse. Only `cmd/rental-management` (the composition root) imports every layer.
- **domain** imports the standard library, uuid and `pkg/authz` only: no struct tags, gin, pgx, redis, JWT, argon2, `apperr`. It returns typed errors (`identity.ErrEmailTaken`, ...), never HTTP ones.
- **usecase** imports `domain` (and stdlib, uuid, `pkg/authz`), never an adapter, gin, pgx, go-redis, the JWT library or `apperr`. Ports take domain types or plain values, so an adapter implements them without importing the use case package.
- **adapter/persistence** and **adapter/redis** return domain types, never sqlc rows, and translate driver errors: no rows -> `shared.ErrNotFound`, unique / foreign key violations -> `*shared.ConstraintError` (`shared.IsUnique`, `shared.IsForeignKey`), an unreachable Redis -> an error wrapping `shared.ErrUnavailable`.
- Each feature has its own folder inside `adapter/http` and `adapter/persistence/postgres`; what the features share (the authenticator, `TxManager`, `Translate`, `Queries`) lives in the parent folder, which never imports a feature folder (linter rules `shared-http-is-not-a-feature`, `shared-postgres-is-not-a-feature`).
- **adapter/http** is the only identity code that imports gin; it maps domain errors to `apperr` (`adapter/http/apperr`) values (same codes and fields as before) and builds the response bodies. It never imports the persistence or Redis adapters (its integration tests do, to wire the real stack).
- **Transactions travel in the context**: a use case calls `tx.WithinTx(ctx, func(ctx) error {...})` and every repository call made with that ctx joins the transaction (`postgres.TxManager`, `postgres.Queries(ctx, pool)`).
- **gin is imported only** by `adapter/http` (all its folders, `httpx` included) and `cmd/rental-management`.
- All error-to-HTTP mapping happens in `adapter/http` (`identity_errors.go`, `location/errors.go`, `property/errors.go`) and `httpx.Fail`; the domain and the use cases never return `apperr` values.
- SQL only under `db/`; no global mutable state, no `init()`; pass `c.Request.Context()` down; comments in English.
- There is only one generated query package, `internal/adapter/persistence/postgres/sqlc` (query names are unique across all of `db/queries`).
- Pure business rules (calculations, state machines) live in small, DB-free functions with table tests in `domain`; use cases are tested with hand-written fakes of their ports (no database).
- Pictures and image checks sit behind ports (`FileStore`, `ImageProcessor`) so the use cases import neither `pkg/storage` nor `pkg/imaging`; the plan, room-guard and room-observer ports take only the `ctx` (the transaction travels in it), never a `pgx` type.

## API

Routes live under `/v1`, plus `/healthz` and `/readyz`. Send `Accept-Language: vi|en` (default `vi`). Browsers call
the API cross-origin (see `CORS_ALLOWED_ORIGINS`); tokens travel in `Authorization: Bearer <access_token>` and in
JSON bodies, never in cookies.

**Success** is `{"data": ...}`; lists add `"meta"`: admin/owner tables `{"page","per_page","total"}`, feeds
`{"page","per_page","has_more"}`; `per_page` defaults to 20 (max 100); bounded reference data (provinces, wards, roles)
has no meta. **Errors** are `{"error": {"code", "message", "fields"}}`: `code` is a stable key clients switch on,
`message` is translated, `fields` (validation only) maps a field to its messages.

| Method | Path | Auth | Success | Notes |
|---|---|---|---|---|
| GET | `/healthz` | - | 200 `{"status":"ok"}` | liveness |
| GET | `/readyz` | - | 200 / 503 | pings Postgres and Redis |
| GET | `/v1/provinces` | public | 200 | 60/min per IP; `Cache-Control: public, max-age=86400`; not paginated (bounded data) |
| GET | `/v1/provinces/{code}/wards` | public | 200 | same limits; 404 `location.province_not_found` |
| POST | `/v1/auth/register` | public, 10/min | 201 user | tenant role only; needs `accept_terms`; sends the verification email |
| POST | `/v1/auth/login` | public, 10/min (+5/min per address and email) | 200 `{user, tokens}` | |
| POST | `/v1/auth/refresh` | public, 30/min | 200 `{tokens}` | rotates the refresh token |
| POST | `/v1/auth/email/verify` | public, 10/min | 204 | body `{token}`; 400 `auth.verification_invalid` |
| POST | `/v1/auth/email/resend` | public, 5/min | 202 | body `{email}`; always 202 (no account enumeration); 60 s cooldown |
| GET | `/v1/auth/me` | bearer | 200 user | |
| POST | `/v1/auth/logout` | bearer | 204 | optional body `{refresh_token}`; always revokes the current access token |
| POST | `/v1/auth/logout-all` | bearer | 204 | revokes every open session of the account |
| POST | `/v1/auth/switch-role` | bearer, 10/min | 200 `{user, tokens}` | body `{role, refresh_token}`; new session in that role |
| PUT | `/v1/auth/password` | bearer, 5/min | 204 | body `{current_password, new_password}`; closes the other sessions |
| GET | `/v1/auth/providers` | public, 30/min per address (`auth-social`) | 200 `{providers: [{provider, requires_matching_email}]}` | the providers that are switched on, so the web draws one button each (Go's replacement of Laravel's `auth.social_providers` in `/config`) |
| GET | `/v1/auth/google`, `/v1/auth/zalo` | public, 30/min per address (`auth-social`) | 302 to the provider | `?redirect=<relative path>` is where the web lands after a login (default `dashboard`; anything but letters, digits, `/ _ -` is replaced); 404 when the provider is not enabled |
| GET | `/v1/auth/google/callback`, `/v1/auth/zalo/callback` | public, 30/min per address | 302 to the web app | the provider's redirect target; see [Social login](#social-login); `Cache-Control: no-store`, `Referrer-Policy: no-referrer` |
| POST | `/v1/auth/social/exchange` | public, 30/min per address | 200 `{user, tokens}` | body `{code}`; the one-time code of the redirect becomes a normal login; 400 `auth.social_code_invalid` |
| GET | `/v1/me/oauth-accounts` | bearer (any role) | 200 `{has_password, providers: [{provider, linked, linked_at, can_unlink, unlinked, requires_matching_email}]}` | enabled providers only |
| POST | `/v1/me/oauth-accounts/{provider}` | bearer, 20/hour per account (`oauth-link`) | 200 `{url}` | the URL the web opens to link the provider; 404 when not enabled |
| DELETE | `/v1/me/oauth-accounts/{provider}` | bearer | 204 | 422 `provider` rule `last_sign_in_method` / `not_linked` |
| PUT | `/v1/me/preferences` | bearer (any role), 10/min per address | 200 user | body `{accepts_invitations?}` (absent = unchanged; not a boolean = 400); `false` makes the account unfindable by landlords and stops invitation emails to its address |

Register (`POST /v1/auth/register`):

```json
{"full_name": "Nguyen Van A", "email": "a@example.vn", "password": "at-least-8-chars", "accept_terms": true,
 "phone_number": "0901234567", "address_detail": "12 Le Loi", "ward_code": 4}
```

Login response:

```json
{"data": {
  "user": {
    "id": "01a206cd-5f4a-709b-8e48-03efd6c5906d", "email": "a@example.vn", "full_name": "Nguyen Van A",
    "phone_number": null, "status": "active", "address_detail": null,
    "ward": {"code": 4, "name": "Phuong Ba Dinh", "province": {"code": 1, "name": "Thanh pho Ha Noi"}},
    "roles": ["tenant"], "active_role": "tenant", "permissions": ["contracts.create", "rooms.view"],
    "terms": {"accepted_version": "2026-11-03", "current_version": "2026-11-03", "needs_acceptance": false},
    "accepts_invitations": true,
    "created_at": "2026-11-23T08:00:00Z"
  },
  "tokens": {"token_type": "Bearer", "access_token": "eyJ...", "expires_in": 900,
             "refresh_token": "q7...", "refresh_expires_in": 2592000}
}}
```

Validation error (422) and a paginated list:

```json
{"error": {"code": "validation.failed", "message": "...", "fields": {"email": ["..."], "accept_terms": ["..."]}}}
{"data": [], "meta": {"page": 2, "per_page": 20, "total": 135}}
```

### Buildings (landlord)

A building groups the rooms of one landlord at the same address and holds what its rooms start from (rent, area, rental
period, description, charges). All routes need the landlord role (a landlord acting as tenant gets 403) and the
`buildings.*` permission; every query is scoped to the caller, so another landlord's building (or an id that does not
parse) is a 404, never a 403. `PUT` is a full replacement: a field that is absent or `null` means "no value"; only
`charges` is tri-state (absent keeps the charges, `[]` removes them, a list syncs them by `id`: a known id updates in
place, anything else is created, the unlisted are deleted).

| Method and path | Permission | Success |
|---|---|---|
| `GET /v1/buildings` (`name`, `ward_code`, `province_code`, `sort=name\|-name\|created_at\|-created_at\|display_order`, `page`, `per_page`) | `buildings.view` | 200 list with `meta` and `rooms_count`, `available_rooms_count` |
| `POST /v1/buildings` | `buildings.create` | 201 detail (409 `plan.building_limit_reached`) |
| `GET /v1/buildings/{id}` | `buildings.view` | 200 detail with `charges` and `media` |
| `PUT /v1/buildings/{id}` | `buildings.update` | 200 detail |
| `DELETE /v1/buildings/{id}` | `buildings.delete` | 204; 409 `property.building_has_rooms` while it holds live rooms |
| `POST /v1/buildings/bulk-delete` `{ids}` | `buildings.delete` | 200 `{done:[ids], failed:[{id,name,code,message}]}`; an id that is not yours is a 422 on `ids` |
| `PUT /v1/buildings/order` `{ids}` | `buildings.update` | 204; the listed buildings swap into the slots they held |
| `POST /v1/buildings/{id}/copy` `{name, include_rooms}` | `buildings.create` | 201 `{building, rooms_copied}`: details and charges, and with `include_rooms` the rooms too (same names, values and inheritance, empty: `available`, or `maintenance` as before; no tenants, contracts, listings or managers; 409 `plan.room_limit_reached`). Pictures are duplicated too (new files) |
| `POST /v1/buildings/{id}/rooms` | `rooms.create` | 201 `{ids, count}`: a run of rooms (see below) |

```json
POST /v1/buildings
{"name": "Nhà trọ Demo", "ward_code": 4, "address_detail": "12 Lê Lợi", "price_vnd": 2500000,
 "area_square_meters": 20, "minimum_rental_period": 1, "rental_period_unit": "month",
 "charges": [{"name": "Điện", "kind": "electricity", "amount_vnd": 3500, "unit": "per_kwh"}]}
```

Names are unique per landlord whatever the case (a deleted building frees its name, 422 `name` `unique`); money is whole
VND (`price_vnd`, `amount_vnd`); the area keeps two decimals; `minimum_rental_period` and `rental_period_unit`
(`month|day`) come together or not at all. Charge `kind`: `service electricity water internet parking cleaning other`;
`unit`: `per_period per_person per_kwh per_cubic_meter one_time`; at most 30 charges, error fields are named by path
(`charges.0.name`). The demo seed (`make seed-demo`) gives `landlord@rental.test` the building "Nhà trọ Demo" with the
rooms P101 to P105 (follow the building), P106 (own rent) and A1 (outside any building, `maintenance`); it is safe to repeat.

Editing a building pushes what changed into its rooms in the same transaction: the address into every room, rent, area,
rental period and description into the rooms that follow them (a removed rent or rental period is not pushed, and the
rent cannot be removed while a room follows it: 422 `price_vnd` `building_price_in_use`), and a changed charge list into
the rooms that follow the charges (a charge copy keeps its id, copies of dropped charges are removed, new ones are added).

A run of rooms: `POST /v1/buildings/{id}/rooms {"name_prefix": "P", "from": 101, "to": 105, "padding": 0, "status":
"available"}` creates `P101..P105` (at most 100 per run, names up to 50 characters, `padding` zero-pads the number).
Values the request carries (`price_vnd`, `area_square_meters`, `minimum_rental_period` + `rental_period_unit`,
`description`, a non-empty `charges`) are the rooms' own; the rest follow the building. A range problem is a 422 on
`name_prefix`; if any name is already used in the building nothing is created (422 `name_prefix` `names_taken`).

### Rooms (landlord)

Same access rules as buildings (landlord role, `rooms.*` permission, other landlords' rooms are a 404). A room lives in a
building or on its own. A room in a building follows its parts (`price`, `area`, `rental_period`, `description`,
`charges`, `media`): the response lists them in `inherits`, and the room columns always hold the value in force. A part
follows the building when it is listed in `inherit` (request body, e.g. `"inherit": ["price", "media"]`; `[]` = none), or,
with no `inherit`, when the request carries no value of its own for it. On update, charges left out of the body and the
room's own pictures count as carried, so a `PUT` never drops them: to follow again, list the part in `inherit`. A room in a
building takes its address from the building; a room outside any building needs its own `address_detail` and
`price_vnd`. `PUT` is a full replacement of the scalar fields (an absent `status` keeps the current one); `charges`
is tri-state as for buildings. Room names are unique per building (case-insensitive; rooms outside any building form one
group; a deleted room frees its name).

| Method and path | Permission | Success |
|---|---|---|
| `GET /v1/rooms` (`name`, `status`, `building_id`, `no_building=true`, `ward_code`, `province_code`, `price_from`, `price_to`, `area_from`, `area_to`, `sort=display_order\|name\|-name\|price\|-price\|area\|-area\|created_at\|-created_at`, `page`, `per_page`) | `rooms.view` | 200 list with `meta`; an end below its start is a 422 on the start (`lte_field`) |
| `POST /v1/rooms` | `rooms.create` | 201 detail (409 `plan.room_limit_reached`) |
| `GET /v1/rooms/{id}` | `rooms.view` | 200 detail: `description`, `charges`, `media` (the building's pictures while the room follows them) |
| `PUT /v1/rooms/{id}` | `rooms.update` | 200 detail; 409 `property.room_status_locked` when a running contract keeps it rented |
| `DELETE /v1/rooms/{id}` | `rooms.delete` | 204 (soft delete); 409 `property.room_has_active_contract` / `property.room_has_open_listing` |
| `POST /v1/rooms/bulk-delete` `{ids}` | `rooms.delete` | 200 `{done, failed}`, refused rooms named with their `code`; an id that is not yours is a 422 on `ids` |
| `POST /v1/rooms/bulk-status` `{ids, status}` | `rooms.update` | 200 `{done, failed}`; rooms already in that status count as done |
| `PUT /v1/rooms/order` `{ids}` | `rooms.update` | 204; the listed rooms swap into the slots they held |

```json
POST /v1/rooms
{"building_id": "01936a3e-...", "name": "P101", "price_vnd": 3000000, "status": "available"}
```
Status values: `available`, `rented`, `maintenance`. What blocks a room (running contract, open listing) and who must be told
about changes (blank contracts, listings back to draft) are ports (`RoomGuards`, `RoomObserver`; both take only the `ctx`: the
room change they react to runs in the transaction the ctx carries). The contract and listing contexts fill them in (see
[Contracts](#contracts) and [Listings](#listings-and-find-rooms)). The observer hears of status changes, content changes and deletions.

### Pictures (landlord) and serving

Rooms and buildings have pictures; the two behave the same. Routes need the landlord role and `rooms.update` (rooms) or
`buildings.update` (buildings); a stranger's room, building or picture id is a 404, and the ownership is checked before
the image is even decoded. Uploads are `multipart/form-data` with the file in the part `file` and an optional text part
`kind` (`photo`, the default, or `cover`); the upload routes are rate limited (60/min per address).

| Method and path | Success |
|---|---|
| `POST /v1/rooms/{id}/media`, `POST /v1/buildings/{id}/media` | 201 the picture `{id, kind, url, thumb_url, content_type, width, height, size_bytes, original_name, display_order}`; 409 `property.media_limit_reached` (`max`: the plan's limit, else `MEDIA_MAX_PER_OWNER`) |
| `PUT /v1/rooms/{id}/media`, `PUT /v1/buildings/{id}/media` `{"items": [{"id", "kind"}]}` | 200 the pictures in the new order: every picture once, at most one `cover` (422 `items` `exists` / `single_cover`) |
| `PUT /v1/rooms/{id}/media/{media_id}/file`, `PUT /v1/buildings/{id}/media/{media_id}/file` | 200; the file is replaced under **new keys** (place and kind kept) and the old files are deleted |
| `DELETE /v1/rooms/{id}/media/{media_id}`, `DELETE /v1/buildings/{id}/media/{media_id}` | 204; positions close up, the cover moves on, the files are deleted |
| `GET /v1/media/{key}` (also `HEAD`; public, no bearer) | 200 the bytes; 304 when `If-None-Match` matches; 404 for an unknown or invalid key |

What the server accepts is checked from the bytes (never the client's content type or file name): WebP, JPEG or PNG, at
most `MEDIA_MAX_UPLOAD_BYTES` (2 MiB) and `MEDIA_MAX_DIMENSION` (2048) pixels on the longest side, checked from the image
header before any pixel is decoded (decompression bombs); animated WebP is refused. Refusals are a 422 on `file`:
`image_type`, `image_dimensions`, `image_corrupt`, `file_too_large` (a body far over the limit is 413). WebP is stored as
uploaded with EXIF/XMP removed; JPEG and PNG are re-encoded as lossy WebP (pure-Go encoder, metadata dropped, alpha on white). Every picture has
a 480 px WebP thumbnail (`_thumb.webp`) made at upload time.

Files are written to storage before the database transaction and deleted again if it fails; files that are no longer
referenced (a deleted or replaced picture, a deleted building) are removed after the commit. The cover (`cover` kind, else
the first picture) is kept on the room or building (`cover.url`, `cover.thumb_url`) so lists never read the pictures.

Inheritance: a room that follows its building's `media` shows the building's pictures and cover (`GET /v1/rooms/{id}` lists
them under `media`; the room has none of its own). A change of the building's pictures updates the cover of every
following room. A room that uploads a picture of its own **stops following** (its `inherits` loses `media`); putting
`media` back in `inherit` drops its own pictures. Copying a building duplicates the files under new keys (a delete in the
copy never touches the source): the building's pictures, and the pictures of the rooms that have their own; rooms that
follow show the new building's.

Serving: `GET /v1/media/property/2026/11/<32 hex>.webp` (the URLs in responses are `MEDIA_PUBLIC_BASE_URL` + key). The names
are random and never reused, so the response is `Cache-Control: public, max-age=31536000, immutable` with an `ETag` (the
file name), `Cross-Origin-Resource-Policy: cross-origin`, `Content-Security-Policy: default-src 'none'` and
`X-Content-Type-Options: nosniff`; the route is not rate limited. Anyone with the URL can fetch the picture (an `<img>`
tag cannot send a bearer token); payment proofs use signed URLs and a private `payments/` prefix this route never serves
(see [Payments](#payments-and-payment-methods)).

### Contracts

A contract ties one of a landlord's rooms to a tenant. Landlords and tenants use the same routes, each seeing their own
side: a landlord the contracts on their own live rooms, a tenant their own contracts (`GET /v1/contracts` and
`GET /v1/contracts/{id}` follow the **active role**). Somebody else's contract (or an id that does not parse) is a 404,
never a 403; a wrong role or a missing permission is a 403. Money is whole VND (`price_vnd`, the rent of one period of the
room's rental unit, `deposit_vnd`); dates are calendar dates (`2026-11-23`, business time zone UTC+7, so "today" is the
Vietnamese date). A contract is `pending` (a registration waiting for the landlord), `active`, `ended` or `rejected`:

```
pending -> active | rejected      active -> ended      ended -> active (reopened)      rejected -> pending | active
```

| Method and path | Role, permission | Success |
|---|---|---|
| `GET /v1/contracts` (`status`, `room_id`, `building_id`, `no_building=true`, `cancellation_status`, `search` (room, tenant name or email), `sort=display_order\|created_at\|-created_at\|starts_on\|-starts_on\|ends_on\|-ends_on`, `page`, `per_page`) | landlord or tenant, `contracts.view` | 200 list with `meta`; `abilities {review, update, delete}` per contract tell the viewer what is allowed |
| `GET /v1/contracts/{id}` | landlord or tenant, `contracts.view` | 200 |
| `POST /v1/contracts/register` `{room_id, starts_on, ends_on?, occupant_count?, notes?}` | tenant, `contracts.create` (30/hour) | 201 the `pending` contract; the landlord sees it at once (no email confirmation) |
| `PUT` or `PATCH /v1/contracts/{id}` | landlord, `contracts.update` | 200; a **partial** update: absent = unchanged, `null` clears `deposit_vnd`, `ends_on`, `notes`, `tenant_name`, `tenant_phone` |
| `DELETE /v1/contracts/{id}` | landlord or tenant, `contracts.delete` | 204; a landlord removes any contract of their rooms, a tenant only their own registration that still waits (409 `contract.not_deletable`) |
| `PUT /v1/contracts/order` `{ids}` | landlord, `contracts.update` | 204; the listed contracts swap into the slots they held (new contracts go on top) |
| `POST /v1/contracts/bulk` `{action: approve\|reject, ids}` | landlord, `contracts.update` | 200 `{done:[ids], failed:[{id,name,code,message}]}`; a contract that cannot be answered is reported, the others still go through; an id that is not yours is a 422 on `ids` |
| `POST /v1/contracts/{id}/terminate` `{reason?}` | landlord, `contracts.update` | 200; a running contract ends today (or at its earlier end date), a waiting registration is rejected |
| `POST /v1/contracts/{id}/cancellation-request` `{reason?}` | tenant, `contracts.request_cancel` | 200; the contract keeps running until the landlord answers |
| `DELETE /v1/contracts/{id}/cancellation-request` | tenant, `contracts.request_cancel` | 200; takes the request back |
| `POST /v1/contracts/{id}/cancellation-request/approve` | landlord, `contracts.update` | 200; the contract ends and the room is free |
| `POST /v1/contracts/{id}/cancellation-request/decline` `{note?}` | landlord, `contracts.update` | 200; the contract keeps running, the tenant sees `cancellation.response_note` |
| `GET /v1/me/contracts` (`group=active\|pending\|history`, `page`, `per_page`) | tenant, `contracts.view` | 200 list, latest start first; `meta.counts` has how many there are per group whichever one is listed |
| `GET /v1/me/contracts/{id}` | tenant, `contracts.view` | 200 with the room's `charges` and `pictures` |

```json
POST /v1/contracts/register
{"room_id": "01936a3e-...", "starts_on": "2026-12-01", "ends_on": "2027-06-01", "occupant_count": 2, "notes": "Two of us"}
PUT /v1/contracts/{id}
{"status": "active", "ends_on": null, "deposit_vnd": 2000000}
```

Rules worth knowing:

- **Approving** (`status: active`, by `PUT` or in bulk) makes the room `rented` and rejects the other waiting registrations of
  the room; only one contract with a tenant can run in a room (409 `contract.room_occupied`). Ending, terminating, approving
  a cancellation or deleting the last running contract makes the room `available` again; a room under repair stays so.
- **Registering** needs a vacant room (`available`, nobody holds a running contract) that is **on offer** (an admin approved a
  listing of it: 409 `contract.room_not_listed`), not the caller's own room, no other
  open registration of the caller on it, a start date of today or later, an end date after the start, and an end date that
  respects the room's minimum rental period (months follow the calendar: 31 Jan + 1 month = 28 Feb; days are added). The same
  period rule is checked when a landlord changes the dates. `domain/contract/testdata/period-parity.json` is the fixture the web
  tests read too (80 cases); the Go table test must give the same dates.
- **Blank contracts**: a room that becomes `rented` through the property API (edit, bulk status, or created rented) while no contract
  runs on it gets a contract with no tenant (`unassigned: true`, `tenant_name`/`tenant_phone` written by the landlord), so it can
  be billed later. It ends when the room stops being rented or is deleted, and gives way when a contract somebody holds is
  approved. A blank contract does not lock the room and is not counted against a plan.
- A room with a running contract **with a tenant** cannot be deleted or moved out of `rented` (property's 409 codes).
- Reopening an `ended` or `rejected` contract is an update with `status`; the request that ended it is cleared.
- The audit port is a no-op for now and the notification port is the inbox's (see [Notifications](#notifications-the-inbox), [Known gaps](#known-gaps)); the plan port is the plan context's (see [Plans](#plans-and-subscriptions)); the use cases call them where the
  Laravel API notifies and audits (`registration_confirmed|approved|rejected`, `cancellation_requested|declined`, `contract_ended`;
  `contract.registered|created|updated|deleted|terminated|cancellation_*`).
- The demo seed (`make seed-demo`) gives `tenant@rental.test` a running contract on P101 (so P101 is `rented`), a registration
  waiting on P102 and an ended contract on P103; safe to repeat.

### Plans and subscriptions

A **plan** is a row of the catalog: `code`, `name`, `description`, `audience` (`landlord` or `tenant`), `price_vnd` per
`billing_interval` (`month`/`year`; 0 is a free plan), `limits` (`max_rooms`, `max_buildings`, `max_images_per_room`,
`max_active_contracts`, `max_pending_registrations`; **`null` is unlimited**, `0` allows none), `search_priority` (greater
ranks higher in find rooms; the listing context reads it through `Limits.SearchPriorities`), `features`
(`auto_invoicing`, `vip_badge`, `priority_support`), `is_default`, `is_active`, `display_order`. An account is on the plan of
its **running subscription** (active, started, not past its end), else the **default plan of its audience**; with no plan at
all nothing is limited (fail open). A person who is landlord and tenant has a plan on each side (the role the request acts
in picks the side); the database allows one active and one pending subscription per account and side.

Every route needs a signed in caller. The seeded catalog (`make seed`) is the Laravel one: landlord Free (default: 20 rooms,
3 buildings, 5 pictures, 20 contracts), Pro 199,000 and VIP 499,000; tenant Free (default: 3 waiting registrations) and Plus
49,000. The demo seed puts `landlord@rental.test` on VIP and `tenant@rental.test` on Plus.

| Method and path | Role, permission | Success |
|---|---|---|
| `GET /v1/plans` (`audience`, `page`, `per_page`) | any signed in caller | 200 the active plans of the side the caller acts on (or of `audience`), in display order, with `meta` (default 100 per page: bounded data) |
| `GET /v1/my/subscription` | any signed in caller | 200 `{plan, subscription, pending_request, usage}`: the effective plan, the running subscription, a paid request waiting for an admin, and what is used (`rooms`, `buildings`, `active_contracts` for a landlord; `pending_registrations` otherwise) |
| `POST /v1/my/subscription` `{plan_id}` | any signed in caller (20/hour) | 201 the same body. A **free** plan takes effect at once and replaces the running one; a **paid** plan becomes a `pending` request (a newer request replaces the older) until an admin activates it, because there is no payment gateway; admins are notified |
| `DELETE /v1/my/subscription` | any signed in caller | 204; the running plan and the open request of the side the caller acts on end, the account falls back to the default plan |
| `GET /v1/admin/subscriptions` (`status` = `pending` by default, `audience`, `user_id`, `page`, `per_page`) | `subscriptions.manage` (admin, super admin) | 200 list with `meta`, newest first, each with its `user` |
| `POST /v1/admin/subscriptions/{id}/activate` | `subscriptions.manage` | 200; the request becomes the running plan from now, for one billing interval, granted by the caller; the user is notified. 409 `plan.subscription_not_pending` |
| `PUT /v1/admin/users/{id}/subscription` `{plan_id, ends_at?}` | `subscriptions.manage` | 201 the new subscription: the user is put on the plan now, replacing the running one; `ends_at` (RFC 3339, in the future) overrides the default end (one interval for a paid plan, none for a free one) |
| `DELETE /v1/admin/users/{id}/subscription` | `subscriptions.manage` | 204; every plan and request of the user ends |
| `GET /v1/admin/plans` (`page`, `per_page`) | super admin | 200 the whole catalog, inactive plans included, with `meta` |
| `POST /v1/admin/plans` | super admin | 201; `code` (lower case letters, digits, `_`, unique), `name`, `audience`, `price_vnd` are required; a plan flagged `is_default` takes the flag from the default of its audience |
| `PUT` or `PATCH /v1/admin/plans/{id}` | super admin | 200; a **partial** update: absent = unchanged, a `null` limit = unlimited. The audience cannot change. There is no delete: switch `is_active` off, and the subscriptions to the plan keep working |

```json
POST /v1/my/subscription          {"plan_id": "01936a3e-..."}
PUT /v1/admin/users/{id}/subscription   {"plan_id": "01936a3e-...", "ends_at": "2027-01-01T00:00:00+07:00"}
PATCH /v1/admin/plans/{id}        {"is_active": false, "max_rooms": null}
```

Rules worth knowing:

- Who may take a plan: the account must **hold** the role the plan is for, and when the request acts as a landlord or a tenant it
  must act in that one (a landlord acting as a tenant cannot take a landlord plan: 409 `plan.wrong_audience`); an admin
  handing a plan out only needs the account to hold the role. A plan that is switched off cannot be taken (409
  `plan.inactive`); asking for the plan one is on already, the default included, is 409 `plan.already_on_plan`.
- **The limits are enforced where the thing happens**, by the property and contract use cases, through their `PlanLimits`
  ports: rooms (`plan.room_limit_reached`), buildings (`plan.building_limit_reached`), pictures per room or building
  (`max_images_per_room`, else `MEDIA_MAX_PER_OWNER`), a landlord's running contracts **with a tenant** and a tenant's waiting
  registrations. A bigger plan lifts the limit at once; a lapsed subscription is ignored even before the expiry job runs.
- **Expiry**: `rental-management subscriptions-expire` (`make expire-subscriptions`) marks the active subscriptions past their
  `ends_at` as `expired`, in batches, and prints the count. It is idempotent and safe to run twice at once; the worker runs it
  daily (see [Background jobs](#background-jobs)), or call it from cron.
- Money is whole VND; there is no `currency` (VND only) and no `auto_renew` (no payment gateway to renew with).

### Listings and find rooms

A **listing** is a landlord's post that offers one of their rooms: a headline (`title`, up to 150 characters) and a text
(`description`, up to 5000), on top of what the room already says (rent, pictures, address and costs are the room's own and
are never copied). Its status:

```
draft -> pending -> approved | rejected         approved -> unpublished | pending | draft
rejected | unpublished -> pending               pending -> draft
```

A landlord writes a **draft** and **sends** it (`pending`); an admin **approves** it (`approved`: the room is on find
rooms while it stays vacant), **rejects** it (with a reason; the landlord can fix it and send it again) or later **takes it
down** (with a reason). A room has **at most one open listing** (waiting or live; a partial unique index and a check in the
use case: 409 `listing.room_already_listed`). **Editing the headline or the text of a waiting or live listing, or editing the
room's content** (rent, address, area, description, minimum period, pictures, charges) **sends it back to a draft**: it
leaves find rooms and the admins' queue, nobody is told, and the landlord sends it again when ready. Saving without a
change, and changing only the status of the room, leave it alone. A room with a waiting or live listing **cannot be
deleted** (409 `property.room_has_open_listing`); a live listing is taken down first. Every lookup of a landlord is scoped
to them: somebody else's listing is a 404.

| Method and path | Role, permission | Success |
|---|---|---|
| `GET /v1/my/listings` (`status`, `search` (title or room name), `ward_code`, `province_code`, `price_from/to`, `area_from/to`, `sort=-created_at\|created_at\|submitted_at\|title\|price\|-price\|area\|-area`, `page`, `per_page`) | landlord, `my_listings.manage` | 200 list with `meta` and `meta.counts` per status |
| `POST /v1/my/listings` `{room_id, title, description?}` | landlord, `my_listings.manage` (30/hour) | 201 a draft; 422 `room_id` for a room that is not theirs or is deleted, 409 `listing.room_already_listed` |
| `GET /v1/my/listings/{id}` | landlord, `my_listings.manage` | 200 with the room's `charges` and `pictures` |
| `PUT` or `PATCH /v1/my/listings/{id}` `{title?, description?}` | landlord, `my_listings.manage` | 200; a **partial** update (`null` clears the description); the room never changes |
| `POST /v1/my/listings/{id}/submit` | landlord, `my_listings.manage` (30/hour) | 200 `pending`; the admins are notified (`listing_submitted`); 409 `listing.invalid_transition` |
| `POST /v1/my/listings/{id}/unpublish` | landlord, `my_listings.manage` | 200 `unpublished` (a live listing only) |
| `DELETE /v1/my/listings/{id}` | landlord, `my_listings.manage` | 204; 409 `listing.unpublish_first` for a live one (a waiting one is withdrawn) |
| `GET /v1/admin/listings` (same filters, `status` = `pending` by default; asking for `draft` shows `pending`) | `listings.review` (admin, super admin) | 200 list with `meta`, the waiting ones oldest sent first, `meta.counts` without drafts, each with its `landlord` and `reviewer` |
| `GET /v1/admin/listings/{id}` | `listings.review` | 200 with the room's costs and pictures; 404 for a draft (the landlord's own business) |
| `POST /v1/admin/listings/{id}/approve` `{note?}` | `listings.review` | 200; the landlord is notified (`listing_approved`) |
| `POST /v1/admin/listings/{id}/reject` `{note}` | `listings.review` | 200; the reason is required (422 `note`); `listing_rejected` |
| `POST /v1/admin/listings/{id}/unpublish` `{note}` | `listings.review` | 200 takes a live listing down; the reason is required; `listing_taken_down`; 409 for a listing that is not live |
| `POST /v1/admin/listings/bulk` `{action: approve\|reject, ids, note?}` | `listings.review` | 200 `{done:[ids], failed:[{id,name,code,message}]}`; one that cannot be reviewed (a draft...) is reported, the others still go through; a rejection needs `note` (one reason for all); an unknown id is a 422 on `ids` |

**Find rooms** shows a room when it is *vacant* (`available`, nobody with an account holds a running contract on it, its
landlord still holds the landlord role) **and** has an approved listing; a landlord's plan decides who comes first (below).
Both searches take the same filters: `house_name` (the name of the boarding house: the building a room is in, or the
landlord's name for a room outside any building; the room's own name is not searched), `landlord_id`, `ward_code`,
`province_code` (ignored when a ward is given), `price_from/to`, `area_from/to`, `sort=price|-price|area|-area|created_at|-created_at`,
`page`, `per_page`.

| Method and path | Who | Success |
|---|---|---|
| `GET /v1/vacant-rooms` | tenant role, `rooms.view` | 200 list with `meta`: the public side plus `address_detail` |
| `GET /v1/vacant-rooms/{id}` | tenant role, `rooms.view` | 200 with `charges` and `pictures`; 404 when the room is not on offer |
| `GET /v1/landlords/{id}` | tenant role, `rooms.view` | 200 the landlord card |
| `GET /v1/public/rooms` | nobody (60/min per address) | 200 list, public side only; `Cache-Control: public, max-age=60` |
| `GET /v1/public/rooms/{id}` | nobody | 200 with `charges` and `pictures`; 404 when not on offer |
| `GET /v1/public/landlords/{id}` | nobody | 200 the landlord card; 404 for anybody who is not an active landlord |

- **Without a `sort`, the rooms of landlords whose plan has a higher `search_priority` come first**, newest first within a
  priority (the plan's priority is read through the `SearchPriority` port; the free plan and no plan rank 0). An explicit
  `sort` ignores the plans.
- **What a guest sees** (`PublicOfferView`): the room's name, `house_name`, area, rent, minimum period, description, ward and
  province, cover, the listing's `title`/`description`, the landlord's `id` and `full_name`, and on the detail the costs and
  the pictures. **Never** an email, a phone number, the street address (`address_detail`), a contract or a tenant: the
  integration tests assert that none of these strings appears in the JSON. A signed-in renter also gets `address_detail`.
- **The landlord card** (`/landlords/{id}`, `/public/landlords/{id}`): `id`, `full_name`, `houses` (the buildings that have
  a room on find rooms), `vacant_rooms_count` and `rooms_url` (the list of their rooms). There is no landlord profile in this
  API yet (see Known gaps), so there is no business name, description, ward or avatar.
- The demo seed writes a listing for each available room of the demo landlord (P101-P103 approved, P104 waiting, P105
  refused, P106 a draft); it runs before the demo contracts, which need approved listings.

### Invoices

A bill is for one billing period of a contract. The landlord writes it as a **draft** (nobody else sees it, nothing is sent),
**sends** it, and it is `paid` or not. `draft -> sent <-> paid`; a draft may also be settled at once (`already_paid`, or
`PATCH status: paid`), which skips `sent`. Money is whole VND, dates are calendar dates in the business time zone (UTC+7).
Landlords and tenants share the read routes, each seeing their own side: the landlord every bill of their contracts, a
tenant what was **sent** to their account (never a draft, never a bill passed on by link). Somebody else's bill is a 404;
a wrong role or permission is a 403 (admins have no bills).

**Periods.** A monthly contract is billed per calendar month counted from `starts_on` (31 Jan + 1 month = 28 Feb), a contract
rented by the day with an end date once for the whole stay (quantity = days), a contract by the day **without** an end date
month by month like a monthly one. Only periods that already started are offered. One bill per contract and period (422 on
`period_start`, `validation.period_billed`); without `period_start` the oldest period with no bill is used.

**Lines** (written into the bill, so later price changes never rewrite it): the rent (`rent_unit_price_vnd` may override it),
the room's `per_period` charges (x periods), `per_person` charges (x `occupant_count`), `one_time` charges (first bill only),
metered charges (`per_kwh`, `per_cubic_meter`) from `readings`, and free `extra_items`. A meter left out of `readings` repeats its last
index (consumption 0); a meter never read before needs a reading **and** `previous_value` (422 on `readings`); a reading below
the previous one is refused. Readings are stored (`meter_readings`) with the bill, and deleting the bill deletes
them so the period can be billed again.

| Method and path | Role, permission | Success |
|---|---|---|
| `GET /v1/contracts/{id}/invoices` (`limit` <= 50, `offset`) | landlord or tenant, `invoices.view` | 200 newest period first; `meta {limit, offset, total, has_more}` ("load more", no page numbers) |
| `GET /v1/invoices/{id}` | landlord or tenant, `invoices.view` | 200; `abilities {update, delete, set_status, send}`, `payment_summary`, `items`, `readings`, `share {live, expires_at}` |
| `GET /v1/contracts/{id}/billing-periods` | landlord, `invoices.view` | 200 the started periods with the bill each already has, and the default name/transfer words |
| `POST /v1/contracts/{id}/invoices/preview` `{period_start?, rent_unit_price_vnd?, readings?, extra_items?}` | landlord, `invoices.create` | 200 `{period_start, period_end, units, items, total_amount_vnd}`; saves nothing, items carry no ids |
| `POST /v1/contracts/{id}/invoices` `{period_start?, due_on?, note?, name?, payment_label?, delivery?, already_paid?, readings?, extra_items?, rent_unit_price_vnd?}` | landlord, `invoices.create` | 201 a draft (or a paid bill with `already_paid`); the contract must be running (409 `invoice.contract_not_running`) |
| `PUT /v1/invoices/{id}` (same fields, `resend_email?`) + `POST /v1/invoices/{id}/preview` | landlord, `invoices.update` | 200; the period stays, the lines are rebuilt; meter indexes before this bill are used as "previous" |
| `POST /v1/invoices/{id}/send` `{delivery?}` | landlord, `invoices.create` | 200; draft -> `sent`; sending again only repeats the notice (409 `invoice.already_paid` once paid) |
| `PATCH /v1/invoices/{id}` `{status: sent\|paid}` | landlord, `invoices.update` | 200; `paid` <-> unpaid; `paid_at` is cleared on unpaid |
| `DELETE /v1/invoices/{id}` | landlord, `invoices.delete` | 204; refused for a paid bill or one somebody paid/is paying (409 `invoice.locked`) |
| `POST /v1/invoices/{id}/share-link`, `POST .../share-link/regenerate`, `DELETE .../share-link` | landlord, `invoices.create` | 200 `{url, expires_at}` (the same link every time until it expires or is replaced) / new link / 204; a draft has no link (409 `invoice.link_needs_sent`) |
| `GET /v1/rooms/{id}/meter-readings` (`charge_id`, `page`, `per_page`) | landlord, `rooms.view` | 200 the room's meter history, newest first |
| `GET /v1/public/invoices/{token}` | nobody (30/min per address) | 200 the bill as the link shows it: **no ids at all**, no email or phone; a wrong, expired, withdrawn or draft link is one and the same 404 `invoice.link_invalid` |

- **Names**: `name` defaults to `MM/YYYY - room`; `payment_code` (the bank transfer text) is the landlord's `payment_label`
  (<= 14 plain characters, default `HD T11-P101`) plus a random 5-character tail, so it is unique across all bills and a
  payment can later be matched to its bill. The invoice number (`INV-YYYYMM-XXXXXX`) is unique too; a clash drawn at the
  same moment is retried with new random parts.
- **Contracts nobody with an account holds** (blank contracts) are billed by `delivery: link` only: the landlord passes the
  link on. The link's secret is stored hashed (lookup) and encrypted with `DATA_ENCRYPTION_KEY` (copy again), expires after
  `INVOICE_SHARE_TTL_DAYS`, and a copy that cannot be decrypted is replaced by a new link.
- **Automatic sending and reminders** are commands that the worker runs daily (or cron, see [Background jobs](#background-jobs)): `make send-invoices`
  (`invoices-send-automatic`) sends the current period of every running contract of a landlord whose plan has
  `auto_invoicing` (rent and fixed charges only: meters cannot be known), idempotent per period; `make send-reminders`
  (`invoices-send-reminders`) reminds the tenant `INVOICE_REMINDER_DAYS` before the due date and tenant and landlord the day
  after it, once per bill (changing the due date re-arms it), skipping bills already covered by payments. Both scan by keyset
  in batches of 100; one failing contract does not stop the others.
- **Payments** (see [Payments and payment methods](#payments-and-payment-methods)): the `PaymentLedger` port is now the payment
  context's real ledger (wired in `bridges_payment.go`; `NoLedger` stays the default for tests): it feeds `payment_summary`, the
  "locked once somebody paid" rule and the "settle" step; `already_paid`/`PATCH paid` record a confirmed `recorded` payment of
  the money still owed (`Settle`, which never touches the status itself), reopening voids it (`Void`), deleting the bill removes
  its payments and proofs (`Purge`). The notification port is the inbox's; the audit and email ports are no-ops until
  their phases (the use cases call them where Laravel does; the unit tests check the calls).
- The demo seed gives the ended contract on P103 a paid bill, a sent one and a sent one passed on by link, and the running
  contract on P101 a draft for the current period; a contract that already has a bill is left alone.

### Payments and payment methods

A **payment** is money paid (or claimed) on a bill: all or part of it, so a bill may have several. A tenant sends one for a
**sent** bill (`pending`), the landlord checks that the money arrived and **confirms** it (`confirmed`, it counts) or **rejects** it
(`rejected`, a reason is required); the tenant may withdraw a payment that is still pending (it is deleted with its proofs). A bill
is **paid when the confirmed payments cover its total**, and the payment use case moves it through the invoice use cases
(`InvoiceStatus`), so a bill is never paid by two code paths. What is still asked of the tenant is
`remaining = total - confirmed - pending` (never negative): money already sent and waiting is not asked twice, and a payment
over `remaining` is refused (422 `amount_exceeds_remaining`). A payment the landlord **writes down** (`record-payment`: cash, or
a tenant with no account, the blank contract) is `confirmed` at once with method `recorded`, no payer and the day they say. When
a landlord marks a bill paid by hand the invoice context asks the ledger to record the money still owed the same way (so it
counts in the totals), reopening it **voids** the confirmed payments (`voided`: kept in the history, no longer counted), and
deleting a bill (only possible when nothing is paid or waiting) deletes its rejected or voided payments and their proof pictures.
A bill with money waiting or received cannot be corrected or deleted (the invoice rules, now fed by the real ledger), and
marking it paid while a payment waits for review is refused (409 `invoice.review_pending_first`).

Money is whole VND (`amount_vnd`; `amount` is accepted as the same field for clients written against the Laravel API; a decimal
is 422 `integer`). Payments use the permissions of the bills as in Laravel (`invoices.view` to pay and read, `invoices.update`
to review and record); the landlord's methods need `payment_methods.manage`. Somebody else's bill or payment is a **404**; a
wrong role is a 403. Every list is paged (`page`, `per_page` <= 100, `meta {page, per_page, total}`).

| Method and path | Role, permission | Success |
|---|---|---|
| `POST /v1/invoices/{id}/payments` multipart `amount_vnd`, `note?`, `payment_method?` (default `manual_proof`), `proofs[]` (<= `PAYMENT_PROOF_MAX_FILES` files of <= `PAYMENT_PROOF_MAX_SIZE_KB` KB; none is fine) or the same fields as JSON without pictures | tenant of the bill, `invoices.view` (30/hour per account) | 201 the payment (`pending`); 409 `payment.invoice_not_payable` for a bill that is not sent; 422 `payment_method` when the landlord does not accept the method (or no such method), `proofs.N` (`image_type`, `image_corrupt`, `file_too_large`...), `amount_vnd` |
| `GET /v1/invoices/{id}/payments` | landlord of the room or tenant of the bill, `invoices.view` | 200 oldest first; each payment has `proofs [{index, url, thumbnail_url}]` (signed links), `payer`, `invoice {id, invoice_number, name, total_amount_vnd, contract_id, room_name}`, `recorded` |
| `GET /v1/invoices/{id}/payment-instructions` (`amount_vnd?`) | tenant of the bill, `invoices.view` | 200 one entry per method the landlord accepts (`method`, `amount_vnd`, `transfer_content`, `qr_url`, `bank_code`/`bank_name`/`account_number`/`account_name` or `phone`/`momo_link`); empty when nothing is due; the QR asks for what is left, or for `amount_vnd` (1..remaining) |
| `GET /v1/payments` (`status?`) | landlord, `invoices.update` | 200 the payments on the bills of their rooms, newest first |
| `POST /v1/payments/{id}/confirm` | landlord, `invoices.update` | 200; settles the bill when the confirmed money covers it; 409 `payment.already_reviewed` |
| `POST /v1/payments/{id}/reject` `{reason}` | landlord, `invoices.update` | 200 (422 `reason` without one); 409 `payment.already_reviewed` |
| `DELETE /v1/payments/{id}` | the tenant who sent it, `invoices.view` | 204; 409 `payment.already_reviewed` once reviewed |
| `POST /v1/invoices/{id}/record-payment` `{amount_vnd, note?, paid_on?}` | landlord, `invoices.update` | 201 a `confirmed` payment (the bill must be sent, 409 `payment.invoice_not_payable`; `paid_on` <= today, 422 `date_not_future`) |
| `GET /v1/my/payment-methods` | landlord, `payment_methods.manage` | 200 every method the platform offers (`manual_proof`, `momo`) with `config` (null when never saved) and `is_active` (a short fixed list: not paged) |
| `PUT /v1/my/payment-methods/{method}` `{config, is_active?}` | landlord, `payment_methods.manage` | 200; only the fields the method knows are kept; 422 per field (`config.bank_code` must be one of the listed banks, `config.account_number` 4-30 letters/digits, `config.phone` 10 digits from 0); an unknown method is 404 |
| `DELETE /v1/my/payment-methods/{method}` | landlord, `payment_methods.manage` | 204 (also when never saved) |
| `GET /v1/payments/{id}/proofs/{file}?expires&signature` | **nobody signs in**: the signature is the permission (120/min per address) | 200 the picture; `{file}` is `<index>.webp` or `<index>_thumb.webp`; a wrong, tampered, expired link and a missing file are all 404 `resource.not_found` |

- **Drivers**: a payment method is a driver (`usecase/payment/drivers.go`: code, settings check, submission check, whether the
  landlord must review, instructions, discard); adding a gateway means writing one and registering it, no schema change
  (`payments.method` and `landlord_payment_methods.method` are codes). `manual_proof` is a bank transfer: the tenant is shown the
  account and a VietQR image URL (`img.vietqr.io`, built from bank, account, amount and the bill's `payment_code`; nothing is
  fetched or stored) and may send a screenshot. `momo` works the same with a MoMo number and `nhantien.momo.vn` link; it is more
  than a stub (Laravel's is too), so it is ported. Both need review. A driver that verifies itself would confirm at once.
- **Secrets**: the settings of a method (the account number) are stored **encrypted** with `DATA_ENCRYPTION_KEY` and never
  logged; the audit trail would hold only masked numbers (last four characters). A saved method that cannot be decrypted
  (the key changed) shows as never saved.
- **Proofs** are private files under `payments/YYYY/MM/<32 hex>` in the media store (always `.webp` now, with a `_thumb.webp` copy;
  older proofs may be `.jpg` / `_thumb.jpg`, still served), written **before** the transaction and removed if it fails or the payment is withdrawn.
  The public `GET /v1/media/*key` route serves only `property/` and `announcements/` keys, so a proof is reachable only through its signed link
  (HMAC-SHA256 over payment, file and expiry, key derived from `DATA_ENCRYPTION_KEY`, valid `PAYMENT_PROOF_URL_TTL_MINUTES`);
  only the two sides of a payment are given links. The bytes are served as stored, whatever the `.webp` in the name says.
- **Locks**: confirming locks the bill first and the payment second, the order the invoice use cases lock in, so the two cannot
  deadlock; submitting and recording lock the bill, so two parallel payments cannot together pay more than the total.
- **Events** (notifications are real; audit and mail ports are no-ops until their phases; the unit tests check the calls): `payment_submitted` to the landlord (with
  `remaining_vnd`), `payment_confirmed` / `payment_rejected` to the tenant (and an email, `PaymentMailer`), audit actions
  `payment.submitted|confirmed|rejected|withdrawn|recorded`, `payment_method.saved|removed`.
- **Contracts**: the renter's `GET /v1/me/contracts` carries `billing {due_count, due_amount_vnd, partial, review_count, overdue,
  next_due_at}` on each contract (only **sent** bills count; `partial` = part of a due bill was paid or sent), accepts
  `payment=due|review|settled` and returns `meta.payment_counts {all, due, review, settled}` for the running contracts; the
  landlord's `GET /v1/contracts` carries `billing {unpaid_invoices}` (drafts and sent bills) and accepts `has_unpaid_invoice=true`.
  The contract context reads the bills and payments with its own SQL (it imports neither context).
- **Deviations from Laravel**: invoice and payment responses stay **separate** (Laravel's single `InvoiceResource` also embeds
  the payments and `payment_instructions`; here use `GET /invoices/{id}/payments` and `.../payment-instructions`, and the invoice
  keeps `payment_summary {paid_vnd, pending_vnd, remaining_vnd}`); `reject` **requires a reason** (Laravel: optional); the bank must
  be one of the listed banks (Laravel only checked the length); the QR/instructions default to what is *left* (as Laravel's
  `InvoiceResource` does) and a payment cannot be sent for more than that; amounts are integers; a bill's payments are paged;
  the landlord's `billing` of a contract has only `unpaid_invoices` (no `unbilled_periods`, `next_period`, `last_invoice`);
  no manager access (a manager with `payments.review`).
- The demo seed gives the landlord a bank transfer (VCB) and the sent bill of the ended contract on P103 a confirmed 40% payment
  and a pending 20% one (the paid bill there already has the recorded payment the ledger wrote); run twice it adds nothing.

### Notifications (the inbox)

Every signed-in person, in any role, has an inbox of **messages the other contexts sent them** (a bill, a payment decision, a
registration, a listing review, a subscription...). A message is a **kind**, its **params** and a **url**: the API never stores
text, the web translates `notifications.kinds.<kind>` with `params`, so a message reads in the language the person picks when they
look. The routes need only a bearer token (no permission, not rate limited) and **every query is scoped to the caller**: somebody
else's message is a 404 `resource.not_found`, and `bulk` silently ignores ids that are not the caller's.

| Method | Path | Auth | Success | Notes |
|---|---|---|---|---|
| GET | `/v1/notifications` | any role | 200 feed `meta {page, per_page, has_more, unread_count}` | `?unread=true\|false`, `?kind=` (unknown kind: 422 `kind` `oneof`), `?page`, `?per_page`; newest first; no COUNT |
| GET | `/v1/notifications/latest` | any role | 200 `data[]`, `meta {unread_count}` | the bell: `?limit` 1-20 (default 8, larger is clamped to 20); not paged |
| GET | `/v1/notifications/unread-count` | any role | 200 `{"unread_count": n}` | |
| POST | `/v1/notifications/read-all` | any role | 200 `{"updated": n}` | only the inbox of the role the caller acts in |
| POST | `/v1/notifications/bulk` | any role | 200 `{"done": n}` | `{action: read\|delete, ids[1..100]}`; `done` counts what changed (own messages only) |
| POST | `/v1/notifications/{id}/read` | any role | 200 the message | 404 for another person's; a second read keeps the first `read_at` |
| DELETE | `/v1/notifications/{id}` | any role | 204 | 404 for another person's |
| DELETE | `/v1/notifications?scope=read\|all` | any role | 200 `{"deleted": n}` | clears the caller's inbox of every role; `scope` is required (422 `scope` `oneof`) |

Message: `{"id", "kind", "params": {"room": "P101", ...}, "url": "/my-rooms" | null, "read_at": null | "...", "created_at"}`.

- **One inbox per role.** One account can be a tenant, a landlord and a manager and acts in one role at a time. A message has an
  `audience` (`landlord`, `tenant`, `manager`, or none = every role): the sender can say it, otherwise the kind decides (a bill
  is for the tenant side, a payment sent for the landlord side, a contract ending for both). Acting as tenant, landlord or manager
  shows the messages for that side **and** the ones for none; acting as staff shows everything addressed to the person.
  `unread_count` and `read-all` follow the same filter, so switching role switches inbox. (Deviation from Laravel, whose manager
  mode showed every row: here a manager sees the manager side and the shared messages.)
- **Who is told.** The contract, plan, listing, invoice and payment use cases already call their `Notifier` where the Laravel API
  notifies; it is now real (`registration_confirmed|approved|rejected`, `cancellation_requested|declined`, `contract_ended`,
  `contract_invitation_received|accepted|declined`, `subscription_requested` (every admin) `|activated`, `listing_submitted` (every
  admin) `|approved|rejected|taken_down`, `invoice_sent|updated|due_soon|overdue`, `tenant_invoice_overdue`,
  `payment_submitted|confirmed|rejected`). "Every admin" means the active accounts holding the `admin` role; **super admins are
  not told**, as in Laravel. Delivery is after the commit and a failure is only logged, never undoes the action.
- **Storage.** Table `notifications` (migration `00011`): `kind` checked by a regex, `params` a jsonb object, `audience_role`,
  `read_at`; an index for the inbox (`user_id, created_at desc, id desc`), one for the unread count and one for the prune. The
  rows go with the user (`ON DELETE CASCADE`).
- **Retention.** `rental-management notifications-prune` (`make prune-notifications`) deletes, in batches of 5000, what is older
  than `NOTIFICATION_RETENTION_DAYS`. It is idempotent and safe to run twice at once; the worker runs it daily.
- The demo seed writes five messages (the tenant: a bill and an approved registration, the second already read; the landlord: a
  payment and a registration, for the landlord side; the admin: a submitted listing), only when the account has none of that kind.

### Realtime (server-sent events)

An accelerator for the bell: the inbox rows are the record, the stream only says "look again". The API pushes what a notification
holds (`{kind, params, url}`; an announcement carries just `{kind: "announcement", url: "/notifications"}`, no texts) to the people who
are online, through Redis Pub/Sub (one channel, `<REDIS_KEY_PREFIX>rt:events`, that every API instance listens to; each instance's
in-memory hub (`adapter/realtime`) hands the event to the streams it holds). A failed push is logged and never undoes a delivery.
`Deliveries` (every context's notifier, the announcement delivery, `invoices-send-reminders` and `announcements-dispatch-due`, which
push when `REDIS_URL` is set) all push through the one `Pusher` port.

| Method | Path | Auth | Success | Notes |
|---|---|---|---|---|
| POST | `/v1/realtime/ticket` | any role, 30/min | 201 `{ticket, expires_in: 60, stream_url}` | a one-time ticket (32 random bytes, only its hash is in Redis, `GETDEL`, `REALTIME_TICKET_TTL`) bound to the caller and the role they act in now |
| GET | `/v1/realtime/stream?ticket=...` | the ticket (or a bearer header) | 200 `text/event-stream` | 401 `auth.unauthenticated` for a missing, unknown, expired or already used ticket; 429 `request.rate_limited` over `REALTIME_MAX_STREAMS_PER_USER` |

A browser `EventSource` cannot send headers, hence the ticket (the access token never goes in a URL); a fetch-based client may send
`Authorization: Bearer` instead of a ticket. The route has **no request deadline and no write deadline** (it skips `httpx.Timeout`
and lifts the server's write deadline); API shutdown closes every stream.

Events (`event: <name>` + one line of JSON `data:`): `ready` (`{}`, first), `notification` (`{kind, params?, url?}`), `announcement`
(`{kind, url}`), `expired` (`{}`, then the server closes), and a `: ping` comment every `REALTIME_HEARTBEAT`. **A stream only gets
the events of the role it was opened for**: a message for the landlord side does not reach the stream of the same account acting
as a tenant (staff streams get everything addressed to them). A stream that cannot keep up (16 events waiting) is closed.

**Web contract.** Get a ticket, `new EventSource(stream_url)`. On `ready`: reload `GET /v1/notifications/latest` (nothing is
replayed, `Last-Event-ID` is ignored, so this is how missed events are made up). On `notification` or `announcement`: reload the bell.
On `expired` or an error: close, wait (1, 2, 5 s with jitter), get a **new ticket**, reconnect; a ticket works once, so
`EventSource`'s own automatic reconnect to the same URL will get 401 and must be replaced by this. When the tab was hidden for 30 s
or more, reload on becoming visible. CORS: a stream from the allowed web origins is a plain cross-origin GET and works as is.

### Invitees (shared by every kind of invitation)

What an invitation needs before it can be addressed: finding the account a landlord means to invite, and letting an address
refuse invitation emails. `users.id` (UUID v7, `GET /v1/auth/me` `data.id`) is the ID a person gives a landlord instead of an
email (accounts of services without an email have none). The invitations themselves are in
[Contract invitations](#contract-invitations-landlord).

| Method and path | Who | Answer |
|---|---|---|
| `POST /v1/invitee-lookup` `{by: email\|id, value}` | landlord role, `contracts.create`; **30/hour per account** | 200 `{"found": true, "user": {id, full_name, email_masked}}` or `{"found": false, "user": null}` |
| `POST /v1/invitation-emails/opt-out` `{email, token}` | nobody; 10/min per address | 204; a token that does not belong to the address is 422 on `token` (`link_invalid`) |
| `PUT /v1/me/preferences` `{accepts_invitations}` | any signed-in account | 200 the user |

- The lookup is **exact**: a whole email (case and spaces do not matter) or a whole id; a partial email, a name, an id that
  is not a UUID or that does not exist find nothing, so a landlord cannot browse the accounts. Somebody who cannot be invited
  (not active, staff, the landlord themself, no tenant role, switched invitations off) answers **exactly like nobody**.
  The answer has no phone and no address and only a masked email (`r*****@example.com`). Every lookup is audited as
  `invitation.lookup` with only `{by, found}` and the account found (never what was typed); the audit port is a no-op
  until the audit phase.
- The opt-out token is the hex HMAC-SHA256 of the lower-case address with a key HKDF-derived (`crypto/hkdf`, info
  `invitation-opt-out/v1`) from `DATA_ENCRYPTION_KEY`; the invitation email will carry it in its link. Only the SHA-256 of the
  address is stored (`invitation_email_blocks`), so a changed key never makes the list forget an address. An address may be
  mailed an invitation only when it is not in that list and no account with it has switched invitations off.
- Per-account limits use `httpx.RateLimitPerUser` (key `bucket:u:<user id>`), so landlords behind one connection do not
  share an allowance.
- The schema of the invitations themselves (`contract_invitations`) is in migration 00009.

### Contract invitations

A landlord offers a room on set terms and sends the offer as a link; whoever it is for opens it, signs in and accepts, and
the contract is made then ([the invited person's side](#contract-invitations-the-invited-person) is below). The offer is for **one account** (`recipient_id`, or an
`email` an account holds), for **an address** nobody holds yet (`email`), or for **anybody** with the link (neither); the rule
is `invitation.AudienceOf`. Routes need the landlord role and `contracts.create`; tenants and staff get 403, and another
landlord's invitation (or an id that does not parse) is a 404 `resource.not_found`. Bodies, dates and money as for contracts.

| Method and path | Limit | Answer |
|---|---|---|
| `GET /v1/contract-invitations` (`status`, `page`, `per_page`) | - | 200 list, **open ones first**, then the newest; `meta.total`; no link in it |
| `POST /v1/contract-invitations` | 30/hour per account (shared with `regenerate`) | 201 `{invitation, link, delivery}`; the link is shown here |
| `GET /v1/contract-invitations/{id}` | - | 200 the invitation |
| `GET /v1/contract-invitations/{id}/link` | 60/min per account | 200 `{link, expires_at}` the **same link** again; 404 `invitation.link_unavailable` once it cannot be shown |
| `POST /v1/contract-invitations/{id}/regenerate` | with `POST` above | 200 `{invitation, link}`: a new link and a new end, **the old link stops working**; nothing is sent again; 409 `invitation.not_pending` when answered or withdrawn |
| `DELETE /v1/contract-invitations/{id}` | - | 204, withdrawn; 409 `invitation.not_pending` the second time |

```json
POST /v1/contract-invitations
{"room_id": "0196...", "starts_on": "2026-12-01", "ends_on": "2027-06-01", "price_vnd": 2400000, "deposit_vnd": 2400000,
 "occupant_count": 2, "contract_status": "active", "note": "Phòng tầng 2", "email": "new.person@example.vn"}
-> 201 {"data": {"invitation": {"id": "0196...", "status": "pending", "expired": false, "audience": "email",
     "room": {"id": "0196...", "name": "P104", "building": null}, "recipient": null, "recipient_email": "new.person@example.vn",
     "price_vnd": 2400000, "deposit_vnd": 2400000, "occupant_count": 2, "starts_on": "2026-12-01", "ends_on": "2027-06-01",
     "contract_status": "active", "note": "Phòng tầng 2", "expires_at": "2026-11-30T08:00:00Z", "link_available": true,
     "answered_by": null, "answered_at": null, "contract_id": null, "created_at": "2026-11-23T08:00:00Z"},
   "link": "http://localhost:2001/contract-invitation?token=<64 characters>", "delivery": {"app": false, "email": true}}}
```

An offer to an account has `"audience": "account"` and `"recipient": {"id", "full_name", "email_masked"}` (never a phone or an
address). Only `room_id` and `starts_on` are required; the price is the room's unless `price_vnd` is given; `occupant_count`
defaults to 1; `contract_status` (the status the contract gets on acceptance: `active` by default, or `pending`) can only be
`active` or `pending`.

- **Rules at creation** (a refusal stores nothing): the room must be the landlord's and live (another's, deleted or missing:
  422 `room_id` `exists`, the same answer); an `active` offer is refused when a tenant already holds a running contract on
  the room (409 `contract.room_occupied`; a blank contract does not count, it gives way on acceptance); `ends_on` must follow
  `starts_on` and respect the room's minimum period (422 `ends_on`). Recipient problems are 422: an id nobody can be invited
  with (`recipient_id` `recipient_invalid`), the landlord's own address (`email` `own_email`), an id and an address of two
  accounts (`email` `recipient_mismatch`). **Deliberately not required** (they guard unsolicited registrations, not an offer to
  a chosen person): an approved listing, a start date today or later, the tenant's waiting-registration cap, the room being
  `available`.
- **Delivery** (after the offer is stored; a failure is logged and never undoes it): an **account** gets an in-app
  notification (`contract_invitation_received`, url `/contract-invitation?invitation=<id>`) and, when it has an email and the
  address is not blocked, an email whose link names the invitation and carries **no secret**; an **address** gets an email
  with the link that carries the token (not sent when the address opted out: `delivery.email` is then false); an offer to
  **anybody** is sent nowhere (the landlord passes the link on). `delivery.app` means "it sits in the account's inbox": the
  notification goes to the inbox (`GET /v1/notifications`, as the tenant). The email is Vietnamese only and is sent
  synchronously, with the opt-out link of the invitation context at its foot.
- **The link** (`WEB_URL/contract-invitation?token=`) holds a 64-character secret (48 random bytes, base64url). The database
  keeps the SHA-256 of it (to look it up) and, while the offer is `pending`, a copy encrypted with `DATA_ENCRYPTION_KEY`
  (`link_token_sealed`, AES-GCM, random nonce) so the landlord can copy **the same link again**. The copy is wiped (a CHECK
  makes sure) when the offer is accepted, declined or withdrawn, and replaced when a new link is made. The list and the
  detail never carry the link. A copy that cannot be opened (the key changed) is `invitation.link_unavailable`: make a new
  link. `expired` in the answer is computed (`pending` and past `expires_at`; `INVITATION_TTL_DAYS`, default 7); an expired
  offer can still get a new link.
- Lock order is room, then invitation, then contracts. Audit actions `contract_invitation.created|regenerated|revoked`
  (no-op port, unit tests check the calls). Manager-drafted offers (`created_by_id`, status `awaiting_approval`) have their
  seam in the use case but nothing creates them.

### Contract invitations (the invited person)

The person an offer is for (tenant role, `contract_invitations.respond`; a landlord must switch to the tenant role, staff get
403) sees it, accepts or declines. The offer is named either by the secret of its link (`token`, which wins when both are sent)
or, for an offer addressed to the caller's account, by its `invitation_id` (what the inbox and the in-app notification carry:
no secret travels). All three routes share **one limit: 20/min per account** (`contract-invitation-answer`).

| Method and path | Answer |
|---|---|
| `POST /v1/contract-invitations/preview` `{token}` or `{invitation_id}` | 200 the offer: landlord (`id`, `full_name` only), room (name, `house_name`, address, area, minimum period, ward and province, cover), terms, `expires_at`; nothing about who else was invited; nothing changes |
| `POST /v1/contract-invitations/accept` `{token}` or `{invitation_id}` | 201 the new contract as its tenant sees it (`ContractView`); the landlord is notified (`contract_invitation_accepted`), the tenant is not |
| `POST /v1/contract-invitations/decline` `{token}` or `{invitation_id}` | 204; the landlord is notified (`contract_invitation_declined`) |
| `GET /v1/me/contract-invitations` (`page`, `per_page`) | 200 the open offers addressed to the caller's account (named offers only: never an address offer or a public one), newest first, `meta.total` |

```json
POST /v1/contract-invitations/accept   {"token": "<64 characters>"}      or   {"invitation_id": "0196..."}
-> 201 {"data": {"id": "0196...", "status": "active", "price_vnd": 2400000, "tenant": {"id": "..."}, "room": {...}, ...}}
```

- **Accepting makes the contract through the same admission core as a registration** (`core.admit`): the room is not the
  caller's own, a running contract gives way only to a blank one and stays within the landlord's plan limit (409
  `contract.room_occupied`, `plan.contract_limit_reached`), the other waiting registrations of the room are rejected and the room
  becomes `rented`; a `pending` offer is the caller's only open contract on the room (409 `contract.already_registered`); the
  dates respect the room's minimum period (422 `ends_on`). **Any refusal rolls the whole transaction back, so the invitation
  stays open** and the landlord may fix the cause. The landlord's own offer is refused (409 `invitation.own_offer`).
- **Every bad link is the same 404 `invitation.invalid`** with the same body: malformed, unknown, expired, answered, withdrawn,
  awaiting approval, not for the caller (named account / address audience), an `invitation_id` that is not addressed to the
  caller or does not parse, and an offer whose room was deleted. A body with neither `token` nor `invitation_id` is a 422 on
  `token` (`required_without`). Who may answer: an offer to an account, only that account; to an address, only an account whose
  email equals it **and is verified**; to anybody, any signed-in tenant.
- **Not required for an offer** (unlike a registration): a listing, a start date today or later, the tenant's waiting-registration
  cap, the room being `available` (they guard unsolicited requests, not an offer to a chosen person).
- **Expiry is read from the clock** (`pending` and `expires_at` passed): nothing runs to mark it. **Deleting a room withdraws its
  waiting invitations** in the same transaction (status `revoked`, secret wiped, audit `contract_invitation.revoked` with
  `reason: room_deleted`). Lock order everywhere: room, then invitation, then contracts.
- Audit actions `contract_invitation.accepted|declined` (and `contract.created` with `origin: invitation`); notification kinds
  `contract_invitation_received|accepted|declined` (the notifications reach the inbox; the audit port is still a no-op; the unit tests check the calls).
- The demo seed offers **P105** to `tenant@rental.test` by id (starts in 14 days, `active`); the tenant's inbox shows it. Safe to
  repeat: nothing is made once the landlord has any invitation.

### Admin (staff work, landlord applications, settings, legal documents, personal data)

The tenth context, `admin`: `domain/admin/`, `usecase/admin/`, `adapter/http/admin/` (`adminhttp`) and
`adapter/persistence/postgres/admin/` (`adminpg`). It works on tables other contexts own (`users`, `user_roles`,
`user_sessions`) with queries of its own and knows no other context (`admin-does-not-know-other-contexts`,
`other-contexts-do-not-know-admin`): the first building of a new landlord (`property`), the messages (`notification`), the
revoked sessions (Redis) are ports adapted in `cmd/rental-management/admin.go`; its audit port is the real recorder (`bridges_audit.go`), and erasing an account blanks the address, device and session of that person's audit rows. Migration `00013` adds
`users.erased_at|deleted_at`, `landlord_applications`, `landlord_profiles`, `legal_documents` (insert-only) and `app_settings`.
Rules: [`docs/go-business-rules.md`](docs/go-business-rules.md) section 1 and the owner's decisions at its end.

| Method | Path | Who | Notes |
|---|---|---|---|
| GET | `/v1/admin/users` | `users.view` (admin, super admin) | paged; `?status`, `?role_name`, `?search` (one word, name or email, wildcards escaped), `?ward_code`, `?province_code`, `?sort_by=created_at\|name\|email`. Never the caller; **staff accounts only for a super admin** |
| DELETE | `/v1/admin/users/{id}` | `users.delete` | soft delete, sessions revoked. Nobody deletes themselves or a super admin; an admin deletes only landlords, managers, tenants |
| POST | `/v1/admin/users/bulk-delete` `{ids}` | `users.delete` | 1-100 ids; 200 `{done, failed[{id,name,message}]}`; an unknown id is skipped |
| GET | `/v1/admin/roles` | `users.assign_roles` | the roles the caller may give (paged): admin gives landlord, manager, tenant; super admin all |
| PUT | `/v1/admin/users/{id}/roles` `{roles, note}` | `users.assign_roles` | `roles: []` is a 422; nobody changes their own roles, gives `super_admin` or changes a super admin's; a staff role never mixes with another. Losing `landlord` needs no running contract (422 `roles`) and tells the landlord; gaining it sets up a profile and first building |
| GET | `/v1/admin/users/{id}/landlord-impact` | `landlord_applications.review` | `{rooms, active_contracts, pending_registrations, unpaid_invoices, can_revoke}`; 404 for a non-landlord |
| POST | `/v1/admin/users/{id}/revoke-landlord` `{note}` | `landlord_applications.review` | 204; rooms, contracts and profile stay |
| GET/POST/DELETE | `/v1/my/landlord-application` | signed in, acting as tenant; POST **10/hour** | GET: the newest or `null`; POST 201 (waits for an admin, the admins - not the super admins - are told); DELETE withdraws the open one |
| GET | `/v1/admin/landlord-applications` | `landlord_applications.review` | paged; `?status` (default `pending_review`), `?search`, `?sort_by=newest\|oldest\|name`; `meta.counts` per status |
| GET | `/v1/admin/landlord-applications/{id}` | same | with `history` (the person's other applications) |
| POST | `.../{id}/approve` `{note}`, `.../{id}/reject` `{note}` | same | 204; reject needs the note; approve adds the `landlord` role, a profile and a first building named after the business (cut to 50) |
| POST | `/v1/admin/landlord-applications/bulk` `{action, ids, note}` | same | 200 `{done, failed}` |
| GET/PUT | `/v1/landlord/profile` | landlord | PUT upserts; GET `null` before the first write |
| GET | `/v1/admin/settings/history` | super admin (`settings.manage`) | feed (`has_more`) of `{key, from, to, changed_at, changed_by{id,name}}`, newest first, read from the audit trail (`setting.changed`) |
| GET/PUT | `/v1/admin/settings` | super admin (`settings.manage`) | `{settings:[{key,value}]}` (`value: null` resets); only the keys of `domain/admin/settings.go`; 422 `settings.{i}.value` out of range; answers `{settings[], version}` |
| GET/POST | `/v1/admin/legal/{terms\|privacy}` | super admin (`legal.manage`) | POST adds a **new version** (201 `{version}`) with both languages, a `change_summary` in both and a white-listed TipTap body |
| GET | `/v1/admin/legal/{kind}/versions`, `.../{document_id}` | super admin | versions paged (default 10, max 50); an id of the other kind is a 404 |
| GET | `/v1/legal/{kind}` | public | the newest version or `data: null` |
| GET | `/v1/profile/legal-updates` | signed in | what was published since the caller accepted (the newest of each kind if never); `?per_page` max 20 |
| POST | `/v1/profile/accept-terms` | signed in | stores the version in force |
| GET | `/v1/profile/export` | signed in; **5/hour** | JSON download of the caller's data (no proofs, nothing about others) |
| GET | `/v1/profile/erasure-check` | signed in | `{blockers[]}`: `running_rental_as_tenant`, `running_contract_as_landlord` |
| DELETE | `/v1/profile` `{password, confirm}` | signed in; **5/hour** | erasure (also deletes the account's social sign-in links); staff, a wrong password or a blocker is a 422 |

The version people must accept is the newest legal document of any kind (`TERMS_VERSION` until one is published); `GET /v1/auth/me`
(`terms.current_version`, `needs_acceptance`) and registration use it through `identity.TermsVersions`, and staff are never asked.

### Managers (rooms handed to a manager)

A landlord hands rooms to a person who does the billing for them. The invitation is a link (as contract invitations): `domain/manager`,
`usecase/manager`, `adapter/http/manager`, `adapter/persistence/postgres/manager`, migration `00015`, linter rule
`manager-does-not-know-other-contexts` (it reuses only the shared kernel `domain/invitation`). A hand-over (`room_managers`) exists only after
the person accepted; a room has one active manager (partial unique index); capabilities (`payments.review`, `contracts.review_registrations`,
`contracts.propose`, `contracts.create`; `create` needs `propose`) are data per room, not permissions.

| Route | Who | Notes |
|---|---|---|
| `GET /v1/manager-capabilities` | landlord, `managers.manage` | the catalogue |
| `GET\|POST /v1/my/manager-invitations`, `GET .../{id}/link`, `POST .../{id}/regenerate`, `DELETE .../{id}` | landlord, `managers.manage` | `POST` 30/hour; link 60/min; 422 `room_ids` names rooms that already have a manager or wait in another invitation (`replace_room_ids` takes them over) |
| `POST /v1/manager-invitations/preview\|accept\|decline` `{token \| invitation_id}`, `GET /v1/me/manager-invitations` | tenant role, `manager_invitations.respond` | 20/min; every unusable link is the same 422 `token`; accept gives the role `manager`, all rooms or none |
| `GET /v1/my/room-managers` (`view=groups`, `manager_id`), `PUT\|DELETE .../{id}`, `POST .../bulk {revoke\|update}` | landlord | taking back the last room removes the role |
| `GET /v1/my/managed-rooms` (`view=groups`, `landlord_id`) | manager role | read only |
| `POST /v1/contract-invitations/{id}/approve\|reject` | landlord | offers a manager wrote wait for the landlord unless the manager holds `contracts.create` |

What a hand-over allows counts **only for a request made in the manager role** (`reqctx` role, bridged in `bridges_manager.go`): a manager
reads the contracts of their rooms (`viewer.AsManager` in the contract and invoice scopes), writes, corrects, sends and previews bills, deletes a
draft they wrote, and writes contract offers per capability. Marking bills paid / `already_paid` needs `payments.review` (403 otherwise). A manager
never sees or touches the contract, bills or payments of a contract they hold as tenant (404). The landlord is told (`manager_bill_sent|edited`) when the
hand-over asks for it; the audit entries carry `actor_role=manager` and the landlord as owner. A manager who loses every room has the offers they
wrote that still wait withdrawn.

### Reports (problem reports with a conversation)

The fourteenth context, `report`: `domain/report` (kinds, statuses, who a report goes to and the pure visibility rules), `usecase/report`,
`adapter/http/report`, `adapter/persistence/postgres/report`, migration `00016` (`reports`, `report_replies` and two read views), linter rules
`report-does-not-know-other-contexts`, `other-contexts-do-not-know-report` and `websocket-only-in-the-report-adapter-and-realtime`. A tenant reports on a
**room** they rent or rented (it goes to the landlord of that room) or on a **feature** of the app (it goes to the admins); a landlord reports only on a feature.
The author and the recipient (the room's landlord, an admin for a feature report; a super admin is the recipient of everything) write as many messages as
they like; **only the recipient** changes the status (`open|in_progress|resolved`). A tenant **deletes** a report they wrote with a *soft* delete: it is gone for
them (404) while the recipient keeps it, `deleted: true`, read only (writing or changing the status: 403 `report.read_only`).

| Method | Path | Permission | Success | Notes |
|---|---|---|---|---|
| GET | `/v1/reports` | `reports.view` | 200 list `meta {page, per_page, total}` | the reports the caller wrote, newest first; `?status=`, `?type=room\|feature`, `?search=` (subject or body, not a pattern), `?page`, `?per_page` (default 10) |
| GET | `/v1/reports/inbox` | `reports.update` | 200 list | the reports the caller has to answer (deleted ones too, `deleted: true`); a landlord's inbox is the room reports of their rooms (acting as landlord), an admin's the feature reports, a super admin's all |
| POST | `/v1/reports` | `reports.create` | 201 the report | `{type: room\|feature, room_id?, subject <= 150, body <= 5000}`; 403 for a landlord reporting a room; 422 `room_id` when a room report names no room (`required`) or a room the tenant never rented (`room_not_yours`); the recipient is told (`report_received`) |
| GET | `/v1/reports/{id}` | `reports.view` | 200 the report | `{id, type, subject, body, status, deleted, created_at, reporter {id, name, role}, room {id, name} \| null, replies_count}`; 404 when the caller may not see it |
| GET | `/v1/reports/{id}/replies` | `reports.view` | 200 list | the conversation **newest first**, `?per_page` (default 20, at most 50), with `total`; each `{id, body, created_at, author {id, name, role}}` |
| POST | `/v1/reports/{id}/replies` | `reports.view` | 201 the report | `{body <= 5000}`; the same use case as a message sent over the socket; 404 for a stranger, 403 `report.read_only` once deleted |
| PUT | `/v1/reports/{id}/status` | `reports.update` | 200 the report | `{status}`; 403 for the author, 403 `report.read_only` once deleted; resolving tells the author |
| DELETE | `/v1/reports/{id}` | `reports.view` | 204 | a tenant deletes their own report (soft); 403 for the recipient |
| GET | `/v1/reports/{id}/ws` | the one-time `?ticket` (or a bearer token) | 101 WebSocket | the live conversation, below |

`role` of a person is the side they wrote from (`tenant`, `landlord`, `admin`); a landlord who is also the tenant of the room is a `tenant` there.
**Notifications** (the real inbox, link `/reports?open={id}`): `report_received` to the recipient (the landlord, or every admin) when a report is written;
`report_replied` to the recipient when the author writes again; `report_answered` to the author when the recipient writes or resolves. **Audit**:
`report.created|replied|status_changed|deleted` (a message is recorded only with its length, never its text; `status_changed` carries `{from, to}`).

**Live conversation (WebSocket).** A report conversation is two-way and chatty (messages, typing, acks), so it uses a WebSocket (library
`github.com/gorilla/websocket` v1.5.3); notifications stay on server-sent events, which are one-way and simpler to keep alive. The ticket flow is the
notification stream's: `POST /v1/realtime/ticket` gives a one-time ticket (stored hashed in Redis, `GETDEL`, `REALTIME_TICKET_TTL`) bound to the caller
and the role they act in, then `new WebSocket(".../v1/reports/{id}/ws?ticket=...")`. The ticket is *not* bound to a report: **access is checked when the socket
opens** with the report rules (only the author, the recipient and a super admin; anybody else gets the same 404 as `GET /v1/reports/{id}`; a deleted report
can be followed by its recipient and no longer by its author). The `Origin` header must be one of `CORS_ALLOWED_ORIGINS` (403 otherwise, and the ticket is
not used up); a request without an `Origin` is not a browser page and is let through. Over `REPORT_WS_MAX_SOCKETS_PER_USER` or `..._PER_REPORT`: 429.

JSON text frames, protocol version 1 (the `ready` frame says so):

| Client sends | Meaning |
|---|---|
| `{"type":"reply","client_id":"<uuid>","body":"..."}` | a message; goes through the same use case as `POST .../replies` (validation, permissions, notification, audit, storage) |
| `{"type":"typing"}` | ephemeral, never stored; at most one per second is passed on |

| Server sends | Meaning |
|---|---|
| `{"type":"ready","v":1,"report_id":"...","status":"open","deleted":false}` | first frame; reload the conversation over HTTP now (nothing is replayed) |
| `{"type":"reply","reply":{"id","body","created_at","author":{"id","name","role"}}}` | a message was added (to **every** socket on the report, the sender's other tabs too: dedupe by `reply.id`; the same object as the HTTP replies list; also for a message sent by `POST .../replies`) |
| `{"type":"ack","client_id":"...","reply_id":"..."}` | to the sender only: the message was stored |
| `{"type":"status","status":"resolved"}` | the recipient changed the status |
| `{"type":"typing","user_id":"..."}` | to the **other** party only |
| `{"type":"error","code":"report.read_only","message":"...","fields":{...},"client_id":"..."}` | a refused frame, with the API's codes and the message in the language of the upgrade request (`validation.failed`, `request.malformed`, `report.read_only`, `resource.not_found`, `auth.forbidden`...); the socket stays open |

Limits: a frame over `REPORT_WS_MAX_MESSAGE_BYTES` closes the socket (1009); more than `REPORT_WS_MESSAGES_PER_MINUTE` frames, a binary frame or a client
that does not read (its queue of 16 acks/errors fills) closes it (1008 / 1003); every write has `REPORT_WS_WRITE_TIMEOUT`; the server pings every
`REALTIME_HEARTBEAT` and drops a socket that answered nothing for twice that long (the read deadline is refreshed by every frame and pong); a socket lasts at
most `REALTIME_STREAM_MAX_AGE` (going away, 1001: the client reconnects with a new ticket, which is how a revoked session or a disabled account loses its
sockets within one token life); API shutdown closes every socket with 1001 (`ReportHub.Close`). Exactly one goroutine reads and one writes each socket; a
full per-socket queue closes the socket instead of blocking the hub.

**Fan-out.** The use case publishes through its `ReportEvents` port; `reporthttp.EventPublisher` turns the event into the frame and publishes it on Redis
channel `<REDIS_KEY_PREFIX>report:{id}`; every API instance listens to the pattern `report:*` (`redisstore.ReportBroadcaster`) and hands the frame to the
sockets it holds on that report (`realtime.ReportHub`, keyed by report id). A frame nobody is connected for is lost; the socket reloads over HTTP.
The demo seed (`seed -demo`) writes a room report with a short conversation and a feature report with an admin answer.

### Audit trail

The eleventh context, `audit`: `domain/audit/` (actions and their tier, scrubbing, money redaction, who sees what, partition names and
retention), `usecase/audit/` (`Recorder`, `Reader`, `Maintainer`), `adapter/http/audit/` (`audithttp`) and
`adapter/persistence/postgres/audit/` (`auditpg`). It knows no other context and none knows it (`audit-does-not-know-other-contexts`,
`other-contexts-do-not-know-audit`): each context's own audit port (`AuditRecorder`, admin's `Auditor`, identity's `Auditor`) is adapted
to the one `Recorder` in `cmd/rental-management/bridges_audit.go`. Rules: [`docs/go-business-rules.md`](docs/go-business-rules.md) section 2.

- **Tables** (migration `00014`): `audit_logs` (the *important* tier: money, rights, contracts, roles, settings; 24 months) and
  `audit_activity` (the *routine* tier: `auth.login|login_failed|logout`, `invitation.lookup`; 90 days) have the same shape and are only
  added to. No foreign keys (the trail outlives the rows it talks about). Both are `PARTITION BY RANGE (created_at)`, one partition per
  month cut at midnight UTC, plus a default partition; the migration makes this month and the next three. The reference columns
  (`room_id`, `contract_id`, `invoice_id`, `payment_id`, `target_user_id`) are plain uuids with partial indexes; `request_id` is text
  (the middleware accepts any sane `X-Request-Id`). `session_id` is the id of the `user_sessions` row.
- **Writing**: `Recorder.Record(ctx, Event)` runs in the transaction of `ctx` (so an entry commits or rolls back with its action). An
  important entry that cannot be written fails the action; a routine one is written in a savepoint (`postgres.Isolated`) and a failure is
  only logged. Who did it, in which role and session, from which address and device comes from the request (`pkg/reqctx`, filled by
  `httpx.RequestID` and `RequireAuth`); a job has no request and is recorded as role `system`; a sign-in names its actor itself. The owner
  of the data (`OwnerID`) becomes `acting_for_id` when somebody else acted. `changes` and `meta` are normalized and scrubbed (`password`,
  `token`, `secret`... removed, `account_number` kept to its last four characters, over 8 KB replaced by `{"truncated":true,"bytes":N}`).
  An action name outside the catalog (`domain/audit/action.go`) is refused; add the constant there when a context gets a new action.
- **Recorded today**: accounts (`auth.register|email_confirmed|login|login_failed|logout|role_switched|password_changed|token_reuse`; a failed
  sign-in keeps the SHA-256 of the email, never the address, and the reason), admin (users, roles, landlord applications and profiles,
  settings, legal documents, terms, export, erasure), contracts and their invitations, invoices, payments and methods, plans and
  subscriptions, listings, announcements and the invitee lookups. Not yet: see Known gaps.
- **Maintenance**: `rental-management audit-maintain` (`make audit-maintain`, daily 03:00 in the worker) makes the partitions of this month
  and `AUDIT_PARTITIONS_AHEAD` more and drops whole the partitions past their tier's retention. A month whose rows already sit in the
  default partition cannot be made (logged, the run goes on).
- **Erasure**: `Recorder.Scrub` (through admin's `Auditor.ScrubPersonalData`) sets `ip`, `user_agent` and `session_id` to NULL on every row
  the erased person made, in both tables: the one exception to "only added to".

| Method | Path | Who | Notes |
|---|---|---|---|
| GET | `/v1/admin/audit-logs` | staff + `audit.view` | feed `{data, meta{page, per_page, has_more}}`, default 50, max 100; `?actor_id`, `?actor_role`, `?action` (in the catalog), `?target_user_id`, `?room_id`, `?contract_id`, `?invoice_id`, `?from`, `?to` (days, ICT, `to` not before `from`), `?tier`. An admin does not see entries made by or about admins and super admins, nor `setting.changed`; **no staff reader sees amounts of money** (`•••`; plan and subscription entries keep theirs) |
| GET | `/v1/my/activity` | signed in | both tiers, what the caller did and what was done to their account (default 20); what *others* did shows only their role (no name, no changes, no address); neighbouring identical entries of one second merge with `count` |
| GET | `/v1/rooms/{id}/history`, `/v1/contracts/{id}/history`, `/v1/invoices/{id}/history` | landlord + `rooms\|contracts\|invoices.update` | important tier only, default 20; the landlord who owns it, anyone else a 404; a room's history includes its contracts and invoices (their entries carry the room) |

### Statistics

The thirteenth context, `stats`: `domain/stats/` (`Percent`/`Change` that are null, never 0, without a denominator; `MonthRange` of at most 36
months, default the last 12; `Occupancy` worked out day by day from the days a room existed and the days it was rented), `usecase/stats/`
(`Property`, `Platform`), `adapter/http/stats/` (`statshttp`) and `adapter/persistence/postgres/stats/` (`statspg`). It only reads: its
queries (`db/queries/stats/landlord_figures.sql`, `platform_figures.sql`) count the tables of other contexts with `GROUP BY` in SQL, months cut
in `Asia/Ho_Chi_Minh`, and it knows no other context and none knows it (`stats-does-not-know-other-contexts`, `other-contexts-do-not-know-stats`).
No migration. Only totals are returned, never a person. Money is whole VND in `*_vnd`; a ratio is a percentage with one decimal and `null` when
there is nothing to divide by.

- **Three readings of money.** *Billed* = invoices `sent` or `paid`, by the month their period starts (a draft is not billed). *Collected* =
  `confirmed` payments by the month they were paid (not those awaiting review; a payment recorded by hand counts). *Still to collect* = what the
  `sent` invoices of the month lack of their confirmed payments. *Overdue* = `sent` and past `due_at`. The collection rate of a month is the part of
  its own invoices already paid, so money for an older invoice does not lift it.
- **Occupancy** is days rented over days available per month (a room exists from its creation to its deletion; a contract counts when `active` or
  `ended`, an `ended` one with no end date ended today; nothing after today is counted). `overview.occupancy_rate` is a different thing: rooms
  `rented` now over rooms standing.
- **Cache**: the landlord's answers sit in Redis for `STATS_CACHE_SECONDS` (default 900; key = endpoint, landlord, active role, building, range, sort, limit
  and the day). They are **not** dropped when an invoice or payment changes: a figure can lag by that time (see Known gaps). The platform's and the
  system's answers are never cached.
- **Query**: `?from`, `?to` (days; the months they fall in; 36 months at most, `to` not before `from`), `?building_id`, `?sort=billed|collected|occupancy`
  (rooms), `?limit` (1-100; rooms and buildings default 50, the dashboard's ranking 10). A bad value is a 422 on that field.

| Method | Path | Who | Answer (`{"data": ...}`) |
|---|---|---|---|
| GET | `/v1/stats/property/dashboard` | landlord + `stats.view` | `{overview, revenue, occupancy, rooms, utilities}`, each exactly what its own endpoint gives (the web calls it with `?from&to&sort=billed&limit=10`) |
| GET | `/v1/stats/property/overview` | same | `{rooms{total,rented,available,maintenance}, occupancy_rate, active_contracts, pending_registrations, expiring_soon (active ending within 30 days), money{outstanding_vnd, overdue_vnd, overdue_count, this_month{billed_vnd, collected_vnd, collection_rate}, previous_month{...}, billed_change, collected_change}}` |
| GET | `/v1/stats/property/revenue` | same | `{months[{month "YYYY-MM", billed_vnd, collected_vnd, outstanding_vnd}], totals{billed_vnd, collected_vnd, outstanding_vnd}}`, every month of the range |
| GET | `/v1/stats/property/occupancy` | same | `{months[{month, occupied_days, available_days, rate}]}` |
| GET | `/v1/stats/property/rooms` | same | `{rooms[{room_id, building_id, name, billed_vnd, collected_vnd, occupied_days, available_days, occupancy_rate}]}` over the whole range, deleted rooms included, by `sort` (null rates last) |
| GET | `/v1/stats/property/buildings` | same | `{buildings[{building_id, name, rooms, rented, billed_vnd, collected_vnd, occupied_days, available_days, occupancy_rate}]}`, richest first; rooms in no building are one row with `building_id: null`; takes no `building_id` |
| GET | `/v1/stats/property/utilities` | same | `{months[{month, electricity{consumption, cost_vnd}, water{consumption, cost_vnd}}]}` from the meter readings (cost = consumption x the unit price it was billed at) |
| GET | `/v1/stats/admin/overview` | admin or super admin + `stats.admin` | `{users{total, active, new_this_month, new_previous_month, change, by_role{tenant,landlord,manager,admin,super_admin}}, applications{by_status{pending_review,approved,rejected}, average_review_hours, oldest_waiting_days}, subscriptions{pending_requests}, announcements{sent_this_month, recent_read_rate}}`; a person with two roles counts under each; the two nullable fields are `null` when nothing was reviewed / nobody waits |
| GET | `/v1/stats/admin/growth` | same | `{months[{month, users, applications}]}` (new accounts and landlord applications; `?from`, `?to`) |
| GET | `/v1/stats/system/overview` | super admin only + `stats.system` | `{staff{admins, super_admins}, plans[{code, name, audience, price_vnd, billing_interval, subscribers}], monthly_recurring_vnd (a yearly plan spread over 12), paid_subscribers, setting_changes[{at, actor_id, changes}] (the 5 latest), audit_last_30_days{important, routine}}` |

An admin has no landlord figures and a landlord no staff ones (the roles do not overlap); a manager has no `stats.view` yet.

### Error codes

Each code has a vi and en message (`errors.<code>`); a test fails if a code or a validation rule has no translation or
the two locale files differ.

| Code | HTTP | When |
|---|---|---|
| `validation.failed` | 422 | invalid fields (`fields` filled) |
| `request.malformed` | 400 | body is not JSON or has wrong types |
| `request.too_large` | 413 | body over 1 MiB |
| `request.rate_limited` | 429 | with a `Retry-After` header |
| `auth.unauthenticated` | 401 | access token missing, invalid, expired or revoked |
| `auth.invalid_credentials` | 401 | wrong email or password |
| `auth.refresh_invalid` | 401 | refresh token unknown, expired, revoked or reused |
| `auth.email_unverified` | 403 | login attempted before the email is verified (only after the password is right) |
| `auth.account_inactive` | 403 | the account is disabled |
| `auth.forbidden` | 403 | missing permission or role |
| `auth.verification_invalid` | 400 | verification token unknown, expired or already used |
| `auth.social_code_invalid` | 400 | the one-time social login code is unknown, used or expired |
| `location.province_not_found` | 404 | unknown province code |
| `resource.not_found` | 404 | generic not found |
| `http.route_not_found` / `http.method_not_allowed` | 404 / 405 | unknown route / wrong method (`Allow` header set) |
| `property.building_has_rooms` | 409 | deleting a building that still has rooms (`count`) |
| `property.room_has_active_contract` / `property.room_has_open_listing` | 409 | deleting a room with a running contract / a listing under review or published |
| `property.room_status_locked` | 409 | a room with a running contract must stay `rented` |
| `property.media_limit_reached` | 409 | too many pictures on a room or building (`max`) |
| `plan.building_limit_reached` / `plan.room_limit_reached` | 409 | the plan's building / room limit (`limit`) |
| `plan.contract_limit_reached` / `plan.registration_limit_reached` | 409 | the landlord's running contract limit / the tenant's waiting registration limit (`limit`) |
| `plan.wrong_audience` | 409 | the account does not hold the role the plan is for, or acts in the other one |
| `plan.inactive` / `plan.already_on_plan` | 409 | the plan is no longer offered / the account is on it already |
| `plan.subscription_not_pending` | 409 | activating a subscription that is not a pending request |
| `plan.listing_limit_reached` | 409 | the landlord's plan allows no more waiting or live listings (`limit`; no plan sets it yet) |
| `listing.invalid_transition` | 409 | the listing cannot move to that status (sending a waiting one, approving a draft...) |
| `listing.room_already_listed` | 409 | the room already has a waiting or live listing (`room`: its name) |
| `listing.unpublish_first` | 409 | deleting a live listing |
| `contract.invalid_transition` | 409 | the status change (or the answer to a registration) does not fit the contract's state |
| `contract.room_occupied` | 409 | another contract is already running on the room |
| `contract.room_not_vacant` | 409 | registering for a room that is rented, under repair or held |
| `contract.room_not_listed` | 409 | registering for a room that has no approved listing |
| `contract.already_registered` | 409 | the tenant already has a waiting or running contract on the room |
| `contract.own_room` | 409 | registering for one's own room |
| `contract.cancellation_not_allowed` | 409 | a request to end a contract (or its answer) that does not fit its state |
| `contract.not_deletable` | 409 | a tenant deleting anything but their own waiting registration |
| `invoice.contract_not_running` | 409 | billing a contract that is not running |
| `invoice.already_paid` | 409 | sending a bill that is already paid |
| `invoice.send_first` | 409 | changing the payment status of something that cannot (kept for the rule; a draft may be settled directly) |
| `invoice.locked` | 409 | correcting or deleting a bill that is paid, or that somebody paid or is paying |
| `invoice.review_pending_first` | 409 | marking paid while a payment waits for review |
| `payment.invoice_not_payable` | 409 | paying or recording money on a bill that is not `sent` (a draft is a 404 for the tenant) |
| `payment.already_reviewed` | 409 | confirming, rejecting or withdrawing a payment that is no longer pending |
| `announcement.recalled` | 409 | changing or putting out a message that was recalled |
| `announcement.image_limit_reached` | 409 | a popup already has the most pictures allowed (param `max`) |
| `invoice.link_needs_sent` | 409 | asking the link of a draft |
| `invoice.link_invalid` | 404 | a bill link that is wrong, expired, withdrawn or for a draft (one answer for all) |
| `invitation.invalid` | 404 | an invitation link or id that is wrong, expired, answered, withdrawn or not for the caller (one answer for all) |
| `invitation.not_pending` | 409 | answering, renewing or withdrawing an invitation that is no longer waiting |
| `invitation.link_unavailable` | 404 | copying the link of an invitation that cannot show it again (make a new one) |
| `invitation.own_offer` | 409 | accepting an offer for one's own room |
| `server.busy` | 503 | statement/lock timeout, request deadline, Redis or database down |
| `server.internal` | 500 | anything else (the cause is only logged) |

Validation rule keys (`validation.<rule>`): `required`, `email`, `min_string`, `max_string`, `min_number`,
`max_number`, `integer`, `uuid`, `oneof`, `accepted`, `unique`, `exists`, `role_not_held`, `password_incorrect`,
`password_same`, `min_items`, `max_items`, `distinct`, `image_type`, `image_dimensions`, `image_corrupt`,
`file_too_large`, `link_not_allowed`, `after_or_equal` (param `other`), `max_days` (param `max`), `building_price_missing`, `building_price_in_use`, `range_invalid`, `range_too_many`,
`range_name_too_long`, `names_taken`, `single_cover`, `rental_period_pair`, `lte_field`, `date` (not `YYYY-MM-DD`),
`after_start_date`, `date_not_past`, `below_minimum_months` / `below_minimum_days` (params `amount`, `date`: the earliest end),
`unknown_service`, `previous_reading_required`, `reading_lower_than_previous`, `new_reading_required`, `nothing_to_bill`, `day_needs_end`, `invalid_period`, `period_billed` (bills), `recipient_invalid` / `own_email` / `recipient_mismatch` (who to invite), `required_without` (param `other`), `link_invalid` (an opt-out link), `amount_exceeds_remaining` (param `remaining`), `method_not_accepted`, `date_not_future`, `account_number_format`, `phone_format` (payments), `format` (a plan code), `datetime` (RFC 3339, `ends_at`).

Field names in `fields` are paths: a nested field is `charges.2.name` (list items are numbered from 0).
A state conflict (the request is fine but clashes with the current data) is 409, a bad field is 422.


### Announcements (admin and landlord messages)

One table, `announcements` (migration `00012`): a message is a notification in the inbox, a popup, or both. Status is never stored (`draft|scheduled|active|ended|recalled` from `published_at`, `starts_at`, `ends_at`,
`recalled_at`). The sender's role is stored (`sender_role`), so a platform message belongs to any admin or super admin and a landlord's
to that landlord; somebody else's id is a 404.

| Method | Path | Who | Success | Notes |
|---|---|---|---|---|
| GET | `/v1/admin/announcements`, `/v1/my/announcements` | admin or super admin (`announcements.send`) / landlord (`announcements.send_to_tenants`) | 200 table `meta.total` | `?status`, `?from`, `?to` (ICT days, by publishing date), `?type`, `?channel=popup\|notification` |
| POST | same path | same | 201 detail | `publish` defaults to true; landlord 30/hour |
| POST | `.../reach` | same | 200 `{recipient_count}` | counts with the very definition delivery uses; 120/min |
| GET | `.../{id}` | same | 200 detail | |
| PUT | `.../{id}` | same | 200 detail | full replacement (absent = default); `publish`, `reshow` (raises `revision`); 409 `announcement.recalled` |
| POST | `.../{id}/publish`, `.../{id}/unpublish` | same | 200 detail | idempotent; unpublish = back to draft, notifications already sent stay and are not sent twice |
| DELETE | `.../{id}` | same | 204 | recall: deletes its notifications; 409 when already recalled |
| GET | `/v1/my/announcements/people` | landlord | 200 table | `?search`: the landlord's running tenants |
| POST | `.../{id}/images` | same | 201 picture | multipart `file`; adds at the end; 60/hour; 409 `announcement.image_limit_reached` (`max`: 10 platform, 3 landlord) |
| PUT | `.../{id}/images` | same | 200 pictures | `{"items": [{"id", "link_url"?}]}` = the new order; pictures not listed are removed; `link_url` left out keeps the link, `""` clears it; 422 `items` `exists` / `link_url` `url_unsafe`; 422 `link_url` `link_not_allowed` for a landlord |
| DELETE | `.../{id}/images/{image_id}` | same | 204 | positions close up, the file is deleted |
| GET | `.../stats`, `.../{id}/stats` | same | 200 | `?from&to` (ICT days, default last 30, max 92: 422 `to` `max_days` / `after_or_equal`), `?channel=notification\|popup` (the figures of that channel only, absent = both) |

**Popups** (pull). `GET /v1/popups/active` (any signed-in role) returns at most **3** popups, most important first (`priority`, then newest):
`{id, type, title, body, cta{label,url}, popup_size, images[{id,url,width,height,link_url}], revision, from{name,kind,role}}`. A popup is
returned when it is published, inside its window, the person belongs to its audience (the same SQL function as delivery) and the pure rules
`domain/notification.Shows` allow it: never seen, or seen under an older `revision` (an edit with `reshow`), else by frequency (`once`: until
closed; `every_session`: closed in this login session stays closed, a new session shows it; `every_days`: again N days after closing), then
`max_total` (views in all) and `max_per_period` (views in the session / the N-day period). `POST /v1/popups/{id}/seen|dismiss|click`
(204, 120/min) record what the person did (one row per person: `view_count`, `dismissed_at`, `clicked_at`; a click also closes); a popup that
is not out, or not for the caller, is a 404 so nobody can skew the figures. Popups are always closable.

**Pictures.** `announcements/YYYY/MM/<32 hex>.<webp|jpg>` through the same storage and image checks as property (the file is written before
the transaction and removed if it fails; replaced files are deleted after the commit); only the main picture is stored. Only the platform's
pictures may carry a `link_url` (`/page` or `https://`); links are set with `PUT .../images` (not on upload: a multipart text part is capped
at 64 bytes). They are served by the shared public `GET|HEAD /v1/media/*key`, which `cmd/rental-management` mounts and dispatches by key prefix
(`announcements/` here, anything else to property), same cache headers.

**Statistics** (totals only, never who). Overview: `{range, messages{total, with_notification, with_popup}, totals, series}`. *Totals* follow a
cohort: the messages published in the range (not recalled; the platform's for admins, a landlord's own) and everything their recipients did
afterwards: `recipients` (sum of `recipient_count`), `read`, `read_rate`; `viewers`, `views`, `views_per_viewer`, `closed`, `clicked`,
`close_rate`, `click_rate` (rates are `null` without a denominator, one decimal). *Series* are per day (ICT) over all the sender's messages: people
who first saw the popup (`viewers`), closed it, pressed its button, and notifications read. The popup keeps one row per person, so there is no
"views per day". Detail (`/{id}/stats`) is the same for one message. No cache.

- **Audiences.** Platform: `everyone`, `landlords`, `tenants`, `custom` (criteria: roles, provinces, wards, plan codes (`none` = no plan),
  registered within/at least N days, registered before a date, active within N days (max 90, from sessions), up to 50 user ids; AND between
  criteria, OR inside one; they also narrow `everyone`). Landlord: `my_tenants`, `my_people` (tenants for now), `selected_people` + `people_ids`
  (cut down to their own tenants). **One definition**: the SQL function `announcement_audience(...)` takes a filter resolved in Go
  (`domain/notification.ResolveFilter`) and is used by the count, the delivery and the popup check, so they cannot disagree.
- **Staff seniority.** Staff are reached only when the criteria name their role **and** they are below the sender: a super admin may name `admin`;
  nobody else may name any staff role; `everyone`, `landlords`, `tenants` and a landlord's scopes never reach staff.
- **Delivery.** Exactly once: `ClaimDelivery` (an `UPDATE ... WHERE sent_at IS NULL` that serializes concurrent callers) then one
  `INSERT ... SELECT` from the audience function (`ON CONFLICT DO NOTHING`, unique `(announcement_id, user_id)`). Notification params
  `announcement_id`, `title_vi|en`, `body_vi|en`; url `/notifications`; tenant/landlord audiences go to that side's inbox. Published with
  no start or a start in the past it is delivered at once; otherwise `rental-management announcements-dispatch-due`
  (`make dispatch-announcements`, idempotent, the worker runs it every minute) delivers it, and retries a failed delivery.
- Validation rules (422): per language title and body both or neither (`title_body_pair`), one language at least (`one_language`), title <=150,
  text <=2000, a channel (`nowhere_to_go`), button label and address together (`cta_incomplete`), safe address (`url_unsafe`: `/page` or `https://`),
  `audience_empty`, `no_people` (a landlord with nobody to reach), roles within what the sender may name (`targeting.roles.N` `oneof`).
- Deviation: the Go Kind constant is `KindAnnouncement` (the entity is `Announcement`), and the filter resolver is `ResolveFilter`.
- Audit actions `announcement.created|updated|sent|unpublished|recalled` go to the audit port (no-op until the audit phase).
- The demo seed adds one **draft** welcome popup by `admin@rental.test` (custom, registered within 14 days, once, button to `/find-rooms`).

## Background jobs

There is no cron to set up: `rental-management worker` (`make worker`, or `docker compose --profile worker up -d worker`) runs the
jobs of the cron-style commands on their own schedules, in the foreground, until it gets SIGTERM/SIGINT. The commands keep working
unchanged (for a one-off run or an external cron).

| Job (same name as the command) | Schedule (ICT, UTC+7) |
|---|---|
| `announcements-dispatch-due` | every `WORKER_DISPATCH_INTERVAL` (1 minute) |
| `subscriptions-expire` | daily 00:15 |
| `audit-maintain` | daily 03:00 |
| `notifications-prune` | daily 03:30 |
| `invoices-send-automatic` | daily 07:00 |
| `invoices-send-reminders` | daily 08:00 |

- **Engine**: `pkg/schedule` (stdlib ticker every `WORKER_TICK`, 30 s). A job is due when its interval has passed since its last start, or
  when the latest daily slot is after its last start; a worker that has never run a daily job waits for today's slot, and one that was down
  for days runs it once. Jobs run one after another; a job times out after 10 minutes.
- **Several workers are safe**: for each due job a worker takes the Redis lock `worker:lock:<job>` (`SET NX PX`, released with a
  compare-and-delete script), reads the mark `worker:last:<job>` again, runs, and sets the mark (also after a failure, so a broken job
  waits for its next slot instead of failing every tick). The jobs are idempotent as well.
- **Logs**: one line per run, `job done` with `job`, `duration`, `result` (`sent 3 reminders`), or `job failed` with the error;
  `worker started` and `worker stopped` frame them. On SIGTERM a running job is finished (up to its timeout) before `worker stopped`; the
  compose service has `stop_grace_period: 90s` and execs the binary as PID 1 so that it receives the signal.
- **Needs** `DATABASE_URL`, `REDIS_URL`, `DATA_ENCRYPTION_KEY` (the compose service sets them for development). The realtime pushes of
  the notifications it creates go out through Redis Pub/Sub to the API instances. `make up` does not start it (profile `worker`).
- **Trying it**: `WORKER_TICK=3s WORKER_DISPATCH_INTERVAL=10s WORKER_DAILY_INTERVAL=25s make worker` runs every job within seconds.
- Not covered: there is no invitation-pruning job (expired invitation rows are kept; no command deletes them yet).

## Auth model

- **Roles and permissions**: five roles (`super_admin`, `admin`, `landlord`, `manager`, `tenant`); a person holds
  several, but staff roles (`super_admin`, `admin`) are never mixed with the others (`authz.MixesStaffWithOthers`).
  Register grants `tenant` only. The permission catalog is Go data (`internal/domain/identity/catalog.go`), written to
  `roles`, `permissions`, `role_permissions` by `seed`. Permissions are `resource.action`; `resource.manage` covers
  every `resource.*`. A request acts in **one role** (the active role): its permissions are the only ones it has.
- **Access token**: JWT HS256, 15 minutes, claims `iss, sub (user id), sid (session id), role, jti, iat, exp`.
- **Refresh token**: 32 random bytes (base64url); only its SHA-256 is stored. Each session (`user_sessions`) has a chain
  of refresh tokens (`refresh_tokens`).
- **Rotation**: `refresh` locks the token row (`FOR UPDATE`), marks it rotated and issues the next token. Using a
  token that was already rotated *within* `REFRESH_REUSE_GRACE` (two tabs at once) is accepted; *after* the grace it
  is treated as theft: the whole session is revoked and that revocation is committed before the error is returned.
  An inactive account or one without a role also closes the session.
- **Revocation**: logout, logout-all, role switch, password change and reuse detection write Redis keys
  `<prefix>auth:revoked_jti:{jti}` and `auth:revoked_sid:{sid}` (TTL = access-token lifetime), checked on every
  request. Redis errors **fail closed** (503), they never let a token through.
- **Passwords**: argon2id (m=19456 KiB, t=2, p=1), PHC string, constant-time compare; an unknown email still costs one
  hash so timing does not reveal which emails exist.
- **Email verification**: register creates the account with `email_verified_at` empty and emails a one-time link
  (`WEB_URL/verify-email?token=...`, 24 h, only the hash is stored). Login before verification answers 403
  `auth.email_unverified` (after the password checks out). `email/resend` always answers 202 and respects a cooldown.
  Emails are sent in the background, so the response time does not reveal whether an address has an account.
- **Rate limits** (per client address, GCRA in Redis through redis_rate: a burst of N, then N per minute; keys are
  `rate:<REDIS_KEY_PREFIX>rl:<bucket>:<address>`): see the route table; login also limits per address +
  hashed email. Set up trusted proxies before putting a reverse proxy in front (gin currently trusts none, so
  `X-Forwarded-For` cannot be spoofed).

## Social login

Signing in (and up) with **Google** or **Zalo**, and linking them to an existing account. Ported from the Laravel
`SocialAuthService`; the rules and the web-facing values are the same, the plumbing is Go's.

**Where it lives.** It is part of the identity context, not a new one: a social sign-in ends in the same session code as
a password login (`Auth.openSessionVia`: session row, refresh token, active role, access token, audit entry), and a
second context would have had to copy it or reach into identity through a bridge. Domain `identity/social.go` (pure
rules and errors), use cases `usecase/identity/social*.go` (`Social`, on top of `Auth`), HTTP `adapter/http/identity/social.go`,
tables in `adapter/persistence/postgres/identity` (`oauth_repository.go`), Redis stores in `adapter/redis/social.go`,
providers in `adapter/oauth`. `cmd/rental-management/social.go` registers the providers that have credentials.

**Ports** (`usecase/identity/social_ports.go`): `SocialProvider` (code, vouches-for-email, authorization URL with state and
PKCE, profile from the callback code), `SocialStateStore` and `LoginCodeStore` (Redis, plain JSON, one use through
`GETDEL`, only the hash of the key is a Redis key), `OAuthAccountRepository`, `SocialUserStore` (what the use cases need of the
account repository: create without a password, confirm an email from a provider). The registry (`SocialRegistry`) holds only the
providers that are switched on: others are neither offered nor reachable (404).

**Login.** `GET /v1/auth/{provider}?redirect=` stores `{provider, intent: login, return path, PKCE verifier}` in Redis (10 minutes,
one use; the key is the OAuth `state`) and redirects to the provider with only the S256 challenge. The provider calls
`GET /v1/auth/{provider}/callback?state&code`: the state is taken (a missing, reused, expired or forged one, a provider that
differs from the state's, no code (the person cancelled) or a provider error all end as `social_failed`, the login-CSRF
guard). The profile gives the account: (1) the account linked to `(provider, provider user id)`; else (2) when the provider
**vouches for the email** (Google's `email_verified`) the account with that email, which then gets the provider linked, and
an email the account never verified becomes verified and **its password is removed** (somebody else may have set it while the
address was unverified); else (3) a **new tenant account** (no password; an email only when vouched, and then verified; the terms
are not accepted yet, so the web asks once). Refused: a **disabled or deleted** account (`account_inactive`; an email still held
by a deleted account is never taken over), a **staff** account (`link_staff`, stricter than Laravel which let a staff email
merge), a provider the person **unlinked** (`link_unlinked`, also when it comes in through the email), an account that has another
account of that provider linked (`link_provider_taken`). The redirect carries **only a one-time code**: `WEB_URL/<redirect>?code=`;
failures go to `WEB_URL/login?error=<value>`. `POST /v1/auth/social/exchange {code}` redeems the code (Redis, `SOCIAL_LOGIN_CODE_TTL`,
single use) into `{user, tokens}` exactly like a login (role = the one used last, else the most senior; an account disabled since
the callback is refused), with `via: google|zalo` in the `auth.login` audit entry (a sign-up also writes `auth.register` with the `via`).

**Linking.** `POST /v1/me/oauth-accounts/{provider}` (the browser cannot send a bearer on a redirect, so the caller is kept in the state)
returns `{url}`; the callback lands on `WEB_URL/profile?linked=<provider>` or `?link_error=<value>&provider=<provider>`. Rules
(`DecideLink`, `LinkEmailRefusal`): a provider account opens one account (`link_elsewhere`); an account has one account of each provider
(`link_provider_taken`; after unlinking another one may take its place); **linking never changes the account's email**: a provider that gives
an email (Google) links only when it equals the account's (case-insensitive: `link_email_mismatch`), and an account without an email
cannot link it (`link_no_email`); Zalo gives none and links freely; staff cannot link (`link_staff`); linking again what is linked is a no-op.
`DELETE` does not delete the row: it sets `unlinked_at`, so the provider cannot sign in by itself any more (`link_unlinked`) until the person, signed in,
links it again. The **last way to sign in cannot be removed** (`SignInMethods.CanUnlink`: a password counts as one) and the rows are locked while this is decided.
Audit: `auth.oauth_linked` (`provider`, `again`) and `auth.oauth_unlinked` (`provider`).

**Values the web reads** (`error` on `/login`, `link_error` on `/profile`): `social_failed`, `account_inactive`, `link_staff`, `link_unlinked`,
`link_provider_taken`, `link_elsewhere`, `link_email_mismatch`, `link_no_email`.

**Libraries.** Google uses `golang.org/x/oauth2` (+ `/google` endpoint), v0.38.0: authorization code with PKCE (`S256ChallengeOption`,
`VerifierOption`), the client credentials and the token request are the library's. The profile comes from the OpenID Connect **userinfo endpoint**
(`sub`, `email`, `email_verified`, `name`) called with the access token we just obtained from Google over TLS, rather than verifying an ID token:
it needs no signature or key-rotation handling and no extra dependency, and the answer is as trustworthy as the token exchange itself. Zalo is not
standard OAuth2 (`secret_key` header on the token request, v4 endpoints, `access_token` header on `graph.zalo.me/v2.0/me`, errors with HTTP 200 and a
non-zero `error`), so it is a small `net/http` client behind an interface (timeouts of 10 s, answers capped at 1 MiB). Both are tested against `httptest`
fakes. `go.mod` gained `golang.org/x/oauth2 v0.38.0` and its one dependency `cloud.google.com/go/compute/metadata v0.3.0`; no existing module changed version.
depguard forbids importing `golang.org/x/oauth2` outside `adapter/oauth`, and `net/http` in domain and use cases.

**Rate limits.** The routes of a person who is not signed in share the named bucket `auth-social` (30 per minute per address; the login limit is per address
and email, which these routes do not have); the link route is 20 per hour per account.

**Deviations from Laravel.** No avatar is imported (the Go users have no avatar column yet). A half-configured provider (id without secret) is a start-up error instead
of silently off. A staff account is refused at sign-in too. `/v1/auth/providers` replaces the provider list of `/config`. One route pair per provider instead of a
`{provider}` wildcard (no clash with `/auth/me`). The exchange code and the state are Redis-only plain data; the code is not tied to the callback's IP.

**Known gaps.** No avatar from the provider; no Zalo OA/ZNS/Mini App; no Facebook/Apple; no "unlink and delete the provider's tokens" call to the provider (nothing of the
provider's tokens is kept: they are used once); the redirect target of a login is validated by pattern, not against a list of web routes; the exchange code can be redeemed from any
client address (it is single use and lives 60 s).

## Testing

- **Unit tests** (`make test`, part of `make check`): pure rules, config, i18n, JSON shapes. No Docker needed.
- **Integration tests** (`make test-integration`, build tag `integration`): `pkg/testdb` starts its own Postgres 18
  and Redis containers (label `rental-management.test=1`) through the Docker socket and clones a migrated template
  database per test. The suite covers migrations up/down/up, idempotent seeding, the location endpoints and the full
  auth flows (verify, login, refresh rotation and reuse, logout, role switch, password change, rate limits, Redis down).
- **They never touch a dev database.** The guard refuses any target that is not its own container, any database not
  named `rental_management_test_*` / the template, any user but the test user, and the ports of the dev databases
  (5432, 5442, 6379, 6389). The tests never read `DATABASE_URL` / `REDIS_URL`. This exists because a test run once
  wiped a dev database: keep it.

## Makefile

`make help` lists everything. Main targets: `check` (gofmt check + vet + unit tests), `lint`, `fmt`, `fmt-check`,
`tidy`, `sqlc` (regenerate `internal/adapter/persistence/postgres/sqlc`; commit the result), `build`, `test`, `test-integration`, `up`/`down`/
`logs`, `migrate`/`migrate-down`/`migrate-status`, `seed`, `seed-demo`, `expire-subscriptions`, `send-invoices`, `send-reminders`, `prune-notifications`, `dispatch-announcements`, `audit-maintain`, `worker`, `migrate-media`, `fetch-locations`, `dev`, `hooks`.

## Git workflow

- Branch `develop`. Run `make hooks` once per clone (sets `core.hooksPath` to `.githooks`).
- **`commit-msg`**: Conventional Commits `type(scope): subject` (subject up to 72 characters; types `feat fix refactor
  perf test docs style build ci chore revert`; `!` marks a breaking change). It also rejects any message containing
  `Co-Authored-By` or `Generated with ...`: only the author appears in the history (this applies to AI assistants
  too).
- **`pre-push`**: `gofmt` check, `go vet`, `golangci-lint`, unit tests, then the integration tests when Docker is
  reachable. `SKIP_INTEGRATION=1 git push` skips the integration tests.
- CI (`.github/workflows/ci.yml`) runs the same checks plus `go mod tidy` and `sqlc diff` (generated code up to date).

## Adding a module

1. Migration in `db/migrations/` (UUID v7 ids, snake_case names with units, `timestamptz`, money as `bigint` VND).
2. Queries in `db/queries/<module>/*.sql`, then `make sqlc` (no hand-edits in `internal/adapter/persistence/postgres/sqlc`).
3. One slice per layer, named after the feature (see identity and property): domain first, then the use cases and their ports, then the adapters, then the wiring: `internal/domain/<context>/` (entities, pure rules with table tests, typed errors), `internal/usecase/<context>/` (`ports.go` + one file per use case), `internal/adapter/http/<context>/` (handlers, routes, bodies, error mapping; a new subfolder and package `<context>http`), `internal/adapter/persistence/postgres/<context>/` (repositories; a new subfolder and package `<context>pg` that uses `postgres.Queries` and `postgres.Translate` from the parent) and any other adapter the ports need.
4. New permissions: add them to `internal/domain/identity/catalog.go` and the role matrix, then `make seed`.
5. New error codes / validation rules: constants + `Codes()`/`Rules()` in `internal/adapter/http/apperr` and the vi and en locale files.
6. Paginate every list (`httpx.ParsePage` + meta); only bounded reference data may skip it.
7. Wire it in `cmd/rental-management` (build the repositories and use cases, mount the routes) and add its routes to the API table above.
8. Tests per layer: table tests for the rules in `domain`, hand-written fakes of the ports for the use cases, `<context>http_test` for the error mapping, integration (`//go:build integration`, `pkg/testdb`) for the repositories and the HTTP flows; run
   `make check lint test-integration`.

## Roadmap

Done: phase 2, `property` (schema; buildings and rooms API with inheritance, propagation, runs, copy, bulk, reorder, the
demo seed; pictures with upload, cover, arrangement, replace, delete, inheritance, copy and public serving).
Done: phase 3, `contract` (schema; registration, answer, edit, reopen, terminate, cancellation request, delete, reorder, bulk
answer; the renter's list and detail; blank contracts and room locks through the property bridges; demo seed).
Done: phase 4, `plan` (schema; the catalog, own subscription, admin activation, assignment and revocation, the super admin's catalog
edits, daily expiry; limits wired into property and contract through the bridges; base and demo seeds).
Done: phase 5, `listing` (schema; a landlord's listings with the review flow, the admins' review and bulk review, the guards and
observer bridges on rooms, the search of find rooms for renters and guests ranked by plan priority, the public landlord card,
demo seed; contract registration needs an approved listing).
Done: phase 6, `invoice` (schema; periods, lines and meter readings, drafts, send, edit, delete, status, preview, billing periods,
share links with a recoverable encrypted secret, the public bill, `already_paid`, the automatic-sending and reminder commands,
meter history, demo seed). Its `PaymentLedger` port is implemented by phase 8.
Done: phase 7, contract invitations (schema; the invitee lookup and the email opt-out with `PUT /v1/me/preferences`; the landlord's
offers by link with delivery, recoverable links, new link and withdraw; the invited person's preview, accept, decline and
inbox; the shared contract admission core used by registration too; withdrawal of a deleted room's offers; the demo offer).
Done: phase 8, `payment` (schema; the landlord's payment methods with encrypted settings, a tenant's part or full payment with proof
pictures, confirm, reject, withdraw, the landlord's list, recorded payments, payment instructions with VietQR/MoMo links, signed
proof links, the real payment ledger for invoices, the billing summary and payment filters on the contract lists, demo seed).

Done: phase 12, `manager` (migration `00015`; invitations by link, answers, hand-overs with capabilities, managed rooms, offers written by managers with the landlord's approval; the manager scope of contracts and bills). Not done: see Known gaps.
Done: phase 10, `admin` (migration `00013`; user list/delete/bulk delete, roles and who may give what, landlord applications with
approve/reject/bulk, the landlord profile and impact/revoke, the settings overrides, the insert-only legal documents with the white-listed
rich text and `change_summary`, acceptance of the terms, the export, the erasure check and the erasure of an account). Emails of the
applications are not sent.
Done: phase 11, `audit` (migration `00014`; the two partitioned tables, the recorder behind every context's audit port, the staff's, own and per-object
views, `audit-maintain`, the settings history, the scrub on erasure; see [Audit trail](#audit-trail)).

In progress: phase 9, `notification`. **Batch 5 is done**: the worker (`rental-management worker`, `pkg/schedule`, Redis locks; see [Background jobs](#background-jobs)). **Batch 1 is done**: the inbox (migration `00011`; the typed kind catalog; the one real `Notifier` behind
the five contexts' ports; the eight inbox routes with per-role inboxes; the prune command; the demo messages). **Batch 2 is done**:
announcements through the notification channel (migration `00012`, the `announcement_audience` SQL function, admin and landlord routes,
reach, publish/unpublish/recall, scheduling with `announcements-dispatch-due`, the draft welcome popup). **Batch 3 is done**: popups
(`/popups/active` + seen/dismiss/click with the pure visibility rules), announcement pictures, statistics with channel tabs, and the shared
`/v1/media` route. **Batch 4 is done**: realtime push with server-sent events (ticket, stream, hub, Redis Pub/Sub, heartbeat, 15-minute streams).
Still to come in this phase: the worker.

Done: phase 14, `report` (migration `00016`; reports with a conversation, soft delete, the inbox, status by the recipient, the notifications and audit entries, the WebSocket conversation with Redis fan-out; see [Reports](#reports-problem-reports-with-a-conversation)).
Done: phase 13, `stats` (no migration; the landlord's dashboard, revenue, occupancy, rooms, buildings and utilities, the platform's overview and growth, the system's overview; see [Statistics](#statistics)).

Done: phase 15, social login (migration `00017`, `oauth_accounts`; Google and Zalo sign-in and sign-up, linking and unlinking, the one-time exchange code; see [Social login](#social-login)).

Later phases: none planned beyond the [known gaps](#known-gaps).

Pure rules of the Laravel project to port (as Go table tests, taking its `tests/Unit` cases as the starting point):

| Laravel `app/Domain` / `Support` | Go package |
|---|---|
| Billing: `BillingPeriodCalculator`, `InvoiceCalculator`, `ServiceCharge`, `RoomBillSummary`, `PaymentLabel`, `BillingRuleViolation` | `internal/domain/invoice` (`PaymentLabel` is in `label.go` there: the transfer text belongs to the bill) |
| Payment: `PaymentService` (remaining, settle, confirm/reject/withdraw), `PaymentMethodDriver`s (`ManualProofDriver`, `MomoDriver`), `PaymentMethodRegistry`, `config/banks.php`, `PaymentStatus` | `internal/domain/payment` (`rules.go`, `methods.go`, ported and tested) and `internal/usecase/payment` (`drivers.go` registry) |
| `PaymentMethodService`, `LandlordPaymentMethod` | `usecase/payment/methods.go`; table `landlord_payment_methods` (settings encrypted with `DATA_ENCRYPTION_KEY`) |
| `Payment`, `payments` table, the signed `payments/{payment}/proofs/{file}` route | table `payments` (adds `voided`, kept from Laravel; `*_vnd` money); `GET /v1/payments/{id}/proofs/{file}` |
| `BillingSummary` (`forContracts`, `filter`), `payment_counts` | the contract context's own read queries (`SearchContracts`, `CountTenantContractsByPaymentState`) |
| Contract: `ContractLifecycle`, `MinimumRentalPeriod` (+ `RentalPeriodUnit::addTo`) | `internal/domain/contract` (`rules.go`, ported and tested; the period fixture is shared with the web) |
| Building: `RoomNameRange`, `RoomInheritance` | `internal/domain/property` (`rules.go`, ported and tested) |
| Listing: `ListingLifecycle` | `internal/domain/listing` (`rules.go`, ported and tested: `CanMove`, `EditingReturnsToDraft`, plus the transitions as methods of `Listing`) |
| Manager: `ManagerCapability`, `ManagerEligibility` | `internal/manager` |
| Plan: `PlanAssignment`, `PlanLimit` | `internal/domain/plan` (`rules.go`, ported and tested; `Interval.EndsAfter` is Laravel's `BillingInterval::endsAfter`, `EffectivePlan` the "subscription else default plan" of `PlanService::planFor`) |
| Statistics: `MonthRange`, `OccupancyCalculator`, `Ratio` | `internal/stats` |
| Announcement: `AudienceCriteria`, `DayRange`, `PopupVisibility` | `internal/notification` |
| Audit: `MoneyRedaction`, `AuditAction`, payload scrubbing | `internal/domain/audit` (ported and tested) |
| Auth: `Pkce`, `ProviderLink`, `SignInMethods` | `internal/domain/identity/social.go` (`PKCEChallenge`, `ValidPKCEVerifier`, `DecideLink` + `LinkEmailRefusal` = `ProviderLink::decide|emailRefusal`, `SignInMethods`; table tests ported, incl. the RFC 7636 vector) |
| `SocialAuthService`, `SocialStateStore`, `SocialProviderRegistry`, `SocialProvider` | `usecase/identity` (`social.go`, `social_ports.go`, `social_registry.go`), `adapter/redis/social.go`, `adapter/oauth` |
| `ZaloClient`/`ZaloOAuth`/`ZaloGraph` | `adapter/oauth/zalo_client.go` (one client behind the `zaloAPI` interface) and `zalo.go` |
| Invitation: `InvitationAudience` | `internal/invitation` |
| Legal: `RichText` | `internal/legal` |
| Report: `ReportPolicy`, `Report::roleOf|scopeAddressedTo`, `ReportService` | `internal/domain/report` (`rules.go`, ported and tested) and `internal/usecase/report` |
| Support: `RoleAccess` | `pkg/authz` (`MixesStaffWithOthers` exists; `assignableBy`, `canManage` with admin user management) |

Route skeleton for later phases (all under `/v1`): landlord `/buildings/{id}/rooms`, `/rooms/{id}`,
`/contract-invitations`, `/invoices` (done), `/invoices/{id}/payments` (done), `/my/payment-methods` (done), `/managers`; account
`/me/invoices`, `/notifications` (done); public `/public/rooms` (done); admin
`/admin/users`, `/admin/landlord-applications`, `/admin/announcements`,
`/admin/audit-logs` (done), `/stats/*` (done); others `/reports`.

### Web impact (separate workstream)

The Nuxt app (`rental-management-web`) reads the Laravel JSON shapes, so moving to this API is its own piece of work,
centred on `useApi` and the composables: `types/api.ts` (envelope, `ApiList`, pagination -> `{data}` / `{data, meta}` /
`{error}`), `utils/http.ts` (read `error.code` and `error.fields`), `useApi` (prefix `/v1`, new base URL, refresh reads
`data.tokens`, handle 204), `useAuthApi` (`data.user` + `data.tokens`; `name`/`phone` become `full_name`/`phone_number`;
new logout-all, change-password, verify/resend email), list pages (a `meta.page`/`meta.total` adapter, ideally in
`usePageSize`), and success toasts that today show the server `message` need local strings. Do it module by module,
as each Go module lands; the Laravel API keeps serving the web until then.

## Known gaps

- **Reports**: the typing indicator is a frame of the socket, not the `POST /reports/{id}/typing` of Laravel (the socket is the only way to type live). Deleting a
  report publishes nothing live (the recipient finds it read only when they next load it); a report that gets a status change that is the same status is a no-op
  (Laravel recorded and announced it again). A non-party gets 404, not Laravel's 403, and a super admin cannot reply to or change a deleted report (Laravel's
  super admin bypass ignored the soft delete) nor delete somebody else's. The ticket is not bound to a report (access is re-checked at upgrade), a socket of a
  disabled account or revoked session lives until `REALTIME_STREAM_MAX_AGE`, and the `report.updated` event of the notification stream is gone: use the socket.
  There is no email for a report. The platform statistics and the personal-data export still have no `reports` block.

- **Stats**: a landlord's figures are cached for `STATS_CACHE_SECONDS` and are **not** dropped when an invoice, payment, contract, room or meter reading changes
  (no event hook yet), so a payment shows after at most 15 minutes by default (set `STATS_CACHE_SECONDS=0` to turn it off, or add an invalidation by
  landlord in `bridges*.go` later). A manager has no `stats.view` (Laravel gives none either), so there are no figures for the rooms a manager runs.
  The platform overview has no `reports` block (no report context yet), there is no CSV/xlsx export, and `failed_jobs` is left out of the system
  overview (the owner's decision). `active_contracts` counts every active contract, blank ones (no account) included. A figure of billed money
  reads the invoice's own period month, so an invoice sent in October for September is September's.

- **Manager**: the manager's rights are wired into contract reads, bills and contract offers only. **Not yet**: payments (`payments.review` to confirm/reject a payment, record money received, and the manager's view of payments), `contracts.review_registrations` (approve/reject registrations, bulk, and the notice of a new registration), the manager's `abilities` on the contract JSON, the `payment_methods` 403 test, and the manager mail has only a Vietnamese template. The room history routes stay for the landlord.

- **Audit trail**: not recorded yet, because the context or the flow does not exist or has no audit port: rooms and buildings (`room.*`,
  `building.*`: the property use cases have no audit port; the room's history today holds only listing, contract, invoice and payment entries),
  password reset (`auth.password_reset*`; `auth.oauth_linked|unlinked` are recorded now), the profile and avatar
  (`user.profile_updated|avatar_changed`), the invitations preference and the opt-out link (`user.invitations_preference_changed`,
  `invitation.opted_out`), the contract-invitation approve/reject of a manager. The action catalog already holds those names. The actions have
  no translated labels in the API (the web labels them by `action`). The history routes are for the owning landlord only (a manager the
  landlord handed a room to does not read it; no manager context yet). `request_id` is text, not uuid. Money keys hidden from the staff are
  listed in `domain/audit/money.go` (`*_vnd` included); a new action that records money under another key must add it, and no test yet runs
  every real money flow through the admin view.
- **Admin**: the `/config` endpoint and
  the `X-Config-Version` header do not exist, so a changed setting is only stored (nothing reads the overrides yet except `GET /admin/settings`);
  no email for landlord applications (only the in-app notices); a deleted or erased account is hidden from the other contexts' own reads only
  where they already filter by status (the erased one is `inactive`); the export has no reports (no report context yet); profile
  preferences stay at `PUT /v1/me/preferences`; `PUT /v1/admin/settings` takes and answers a `{settings}` body (the rules did not fix the shape);
  an admin who revokes the only role of a landlord leaves the `tenant` role.

- **Notifications (inbox, announcements)**: the inbox and the announcements' notification channel exist. Popups, pictures and
  statistics exist (no cache on the popup audience check or the statistics; popups cost up to 20 indexed audience lookups per app open).
  The **worker** is a later batch. Realtime: one shared Pub/Sub channel (every instance sees every event; shard by user hash if it ever
  matters), no replay of missed events (the web reloads on `ready`), the cap of streams per person is per instance, a revoked session keeps
  its open stream until it expires (at most `REALTIME_STREAM_MAX_AGE`), and the notification stream carries notifications only (report conversations have their own WebSocket, see [Reports](#reports-problem-reports-with-a-conversation)). Pictures are not checked for sensitive content on the server, and impressions are
  one row per person (no per-view history, so no "views per day"). The
  `AuditRecorder` port is declared and no-op. Announcements have no `my_managers` audience (no managers yet), `active_within_days`
  reads `user_sessions.last_used_at`, and a delivery that failed is retried only by `announcements-dispatch-due` (the worker, every minute). There is **no email** for any notification (the contexts' own mail
  ports are separate and mostly no-ops). The daily commands run in the worker.
  Messages of kinds no context sends yet (landlord applications...) are in the catalog but nothing creates
  them. `audience` is not exposed in the message JSON (the web does not need it: the inbox is already filtered). The inbox has
  no "mute a kind" setting and no per-user rate limit (the sender side bounds what is created).

- **Rooms (property)**: the plan port (it receives only the `ctx`) is the plan context's (see below); the room guards and observer are the contract and listing contexts' (combined in `bridges.go`).
  Room pictures stay in storage when the room is soft-deleted (history); orphaned files from a crash between the
  storage write and the commit are not swept (no background job yet). Copying a building with many rooms that have
  their own pictures duplicates every file before the transaction, so it can take a while.

- **Pictures in S3**: the driver is tested against a fake S3 (and SeaweedFS by hand); R2 and AWS S3 themselves are
  untested. Pictures are served by the API proxy unless `MEDIA_PUBLIC_BASE_URL` points at a public bucket domain;
  a failed upload after a database rollback leaves an orphan object, as with local files (no sweeper yet).
- **Pictures (property phase, platform part)**: everything new is stored as WebP: a JPEG or PNG upload is re-encoded with the
  pure-Go `go-webp` encoder (metadata removed), WebP (what the web sends) is stored as uploaded with its EXIF and XMP chunks
  removed, thumbnails are WebP (`_thumb.webp`) made at upload time without a queue. Rows written before keep their `.jpg` /
  `_thumb.jpg` keys, which are served from the stored key (content type from the extension). The EXIF orientation of a
  directly uploaded JPEG is not applied (the web always sends an upright WebP).

- **Contracts**: the `Notifier` is the inbox's (see [Notifications](#notifications-the-inbox)); the `AuditRecorder` port is a no-op default (nothing is
  stored); the use cases call them where Laravel does and the unit tests check the calls, so the audit phase
  only implements the port (the `PlanLimits` port is the plan context's). No emails are sent for contracts. Not built yet: the manager (room manager) access, the landlord's `billing` fields beyond `unpaid_invoices` (`unbilled_periods`, `next_period`, `last_invoice`), the
  `my_registration` of find rooms (which rooms the viewer already registered for; the contract context can add it). Registration does not check that the landlord still holds the
  landlord role, and a tenant's list shows contracts on rooms the landlord deleted (history). The renter's detail lists the
  room's charges and pictures and a `billing` summary, but not the bills themselves.
- **Plans**: there is **no payment gateway**, so a paid plan is a request an admin activates by hand once the money arrived
  (nothing renews by itself; a subscription simply lapses at its end). The `Notifier` is the inbox's (admins are told of a request, users of an activation) and the `AuditRecorder` port is a no-op
  default: `subscription.*`/`plan.saved` are not stored (the
  use cases call the ports where Laravel does and the unit tests check the calls). `subscriptions-expire` runs daily in the worker (without it nothing marks them expired); a lapsed subscription is ignored by
  the limits and the plan screens even before it runs, so a late run only leaves a stale `active` row. `GET /v1/plans` needs a
  signed in caller (as in Laravel), so a guest cannot see the pricing yet; the `UserResource.plan` badge, the `auto_invoicing`
  feature (no invoices yet), a `max_active_listings` limit (the listing use cases check a `PlanLimits` port that no plan sets yet) and the clean-up of the plan of an
  account that loses a role are not built. The plan context counts usage with its
  own read-only queries on `rooms`, `buildings` and `contracts` (the limits themselves are checked by property and contract).
- **Listings**: there is **no landlord profile** (`landlord_profiles` belongs to the landlord applications phase), so the
  landlord card has only the full name, the building names, the count of rooms on find rooms and a link to them (never an
  email or a phone number), and `house_name` is the building's name, or the landlord's own name for a room outside any
  building (Laravel falls back to the profile's business name). Deviations from Laravel: a registration **needs** an approved
  listing (Laravel only hides the room from find rooms); another landlord's listing is a 404 (not a 403); a room that is not
  the landlord's (or is deleted) is a 422 on `room_id`; deleting a waiting listing is allowed (as in Laravel: only a live
  one has to be taken down first); there is no server-side cache of the public list (a `Cache-Control: public, max-age=60`
  header says the same to browsers and shared caches). The `Notifier` is the inbox's and the `AuditRecorder` port is a no-op default (the use
  cases call them where Laravel does and the unit tests check the calls); no emails are sent. A deleted room keeps its
  non-open listings (history). Listings and rooms are checked together only at write time: a room whose landlord loses the
  landlord role simply stops being offered. Not built: the limit of open listings per plan, listings drafted by a room manager,
  the 1000-room find-rooms demo seed.
- **Payments**: there is **no online gateway** (a driver seam exists; every payment is checked by hand by the landlord), no
  manager access (a manager with `payments.review`), and the audit and `PaymentMailer` ports are no-ops
  (the use cases call them where Laravel does and the unit tests check the calls): payments and their decisions reach the inbox,
  but nothing is stored in an audit trail and no email goes out. A bill passed on by link has no payer account, so only the landlord
  can record money on it. a proof sent as PNG/JPEG is re-encoded to WebP (WebP as sent), so new proofs are always `.webp`; thumbnails are made at upload (Laravel's `payments:generate-proof-thumbnails` has nothing to do).
  The files of a deleted bill or a withdrawn payment are removed best effort (a failure is logged, an unreferenced private file
  is left); a proof written before a transaction that then rolls back is removed again, but a crash between the two leaves an
  orphan (no sweeper). A ledger built for the daily invoice commands has no file store (they never delete bills). The
  bank list is fixed in the domain (`domain/payment.Banks`); `GET /config` does not exist yet to hand it to the web.
- **Invoices**: see [Invoices](#invoices). The audit and email ports are no-ops: bills reach the tenant's inbox (`delivery: app`), nothing is stored in an audit trail and no
  `InvoiceMail` goes out; the unit tests check the calls. The worker runs the daily commands (`send-invoices`, `send-reminders`). Not built: the tenant's own invoice list (`/me/invoices`), bulk bill creation, a plan limit for this feature, room managers' access.
- **Invitations**: the audit port is a no-op default and the invitation notifications go to the inbox through the contract context (`delivery.app` is truthful
  because the offer sits in the account's inbox, `GET /v1/me/contract-invitations`). The invitation email is Vietnamese only (no
  account language is known) and is sent synchronously after the commit; expired offers and their sealed secrets are never pruned
  (no command yet); the lookup has no avatar. An account's own address is also checked against the opt-out list before it is
  mailed. **Deviations from the Laravel API, on purpose**: every bad link is a 404 `invitation.invalid` (Laravel used a 422), and
  an offer does not need a listing, a future start date or the tenant's registration cap (D9 of the plan). Not built:
  manager-drafted offers (`created_by_id`, status `awaiting_approval` have their seam but nothing creates them and there are no
  approve/reject routes), "block a sender". A `Preview` does not lock the room, so a preview racing a room deletion may show an
  offer that the next call refuses (accept and decline lock and refuse it).
- **Rehash on login** is not implemented: if the argon2 parameters change, old hashes stay until the password changes.
- No rate limit per *account* on password change (only per client address, 5/min).
- Graceful shutdown (including waiting for emails still being sent) is implemented but its log line was not observed.
- SMTP goes through go-mail with opportunistic STARTTLS (TLS 1.2+) and the strongest AUTH the server offers when
  `SMTP_USERNAME` is set, but only plain delivery was tested (Mailpit in the compose stack has no TLS or AUTH, and the
  unit test uses a fake server): STARTTLS and AUTH against a real provider are untested.
- When a session is closed (reuse detected, inactive account) and Redis is down, the database revocation holds (the
  refresh token stops working) but the still-valid access tokens of that session live until they expire (15 min): the
  failure is logged, not returned.
- Reverse-proxy deployment needs a trusted-proxies setting (gin trusts none now, so client IPs behind a proxy are the
  proxy's).
- `make dev` (air outside compose) is unverified; compose runs air and is.
- The provinces/wards data is a snapshot (`make fetch-locations` refreshes it when the divisions change).
