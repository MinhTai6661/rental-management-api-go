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
`log/slog` - argon2id + JWT (golang-jwt) - testcontainers (integration tests). Go 1.26.

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
cmd/rental-management/        the one binary: api | migrate [up|down|status] | seed [-demo] | media-upload | subscriptions-expire; also the composition root (api.go builds the dependencies, router.go the router, commands.go migrate/seed, bridges.go the adapters between contexts): the only place that knows every layer
db/migrations/                goose migrations, embedded in the binary
db/queries/<module>/          the SQL sqlc turns into Go (all of it generates into the one package internal/adapter/persistence/postgres/sqlc, committed, never edited by hand)
tools/fetchlocations/         one-off download of the provinces/wards snapshot (internal/adapter/snapshot/locationdata/locations.json.gz)

config/                       environment -> validated Config
pkg/                          INFRASTRUCTURE shared by every module; no business rules
  authz/                    pure authorization rules: roles, active-role resolution, permissions, Principal
  i18n/                     vi/en messages (go-i18n, embedded JSON) and Accept-Language negotiation (x/text/language)
  db/                       pgx pool, transactions, error helpers, goose runner
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
    contract/                 Contracts (list, get, register, update, bulk answer, terminate, cancellation, delete, reorder), Rentals (the tenant's own list and detail), Invitations (the landlord's offers by link: create, list, get, copy the link, new link, withdraw), RoomLinks (what property needs: blank contracts, locks), Seeder; ports: ContractRepository, InvitationRepository, Invitees, LinkTokens, Sealer, InvitationMailer, RoomReader, RoomStatusSetter, Notifier, AuditRecorder, PlanLimits
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
      testenv/                response helpers of the HTTP integration tests, shared by the features
    persistence/postgres/     (package postgres) what the repositories share: TxManager, error translation (Translate), context-bound queries (Queries); never imports a feature folder
      listing/                (package listingpg) listing repository, the read-only views of rooms listings need, and the search of find rooms (offers, landlord cards)
      plan/                   (package planpg) plan and subscription repositories, and the read-only views of accounts and usage (rooms, buildings, contracts) the plan rules need
      identity/               (package identitypg) user, session, verification and permission repositories
      location/               (package locationpg) location repository
      property/               (package propertypg) building, room, charge and media repositories, row <-> domain mapping
      contract/               (package contractpg) contract and invitation repositories and the read-only room view contracts need, row <-> domain mapping
      sqlc/                   the one generated sqlc package (all queries of db/queries)
    redis/                    (package redisstore) the revoked tokens and sessions store
    security/                 argon2id password hasher, JWT access tokens, opaque random tokens
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
uploaded with EXIF/XMP removed; JPEG and PNG are re-encoded as JPEG (metadata dropped, alpha on white). Every picture has
a 480 px JPEG thumbnail made at upload time.

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
- The notification and audit ports are no-ops for now (see [Known gaps](#known-gaps)); the plan port is the plan context's (see [Plans](#plans-and-subscriptions)); the use cases call them where the
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
  `ends_at` as `expired`, in batches, and prints the count. It is idempotent and safe to run twice at once; run it daily from
  cron (the binary has no scheduler).
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
- **Automatic sending and reminders** are commands for cron (no scheduler in the binary): `make send-invoices`
  (`invoices-send-automatic`) sends the current period of every running contract of a landlord whose plan has
  `auto_invoicing` (rent and fixed charges only: meters cannot be known), idempotent per period; `make send-reminders`
  (`invoices-send-reminders`) reminds the tenant `INVOICE_REMINDER_DAYS` before the due date and tenant and landlord the day
  after it, once per bill (changing the due date re-arms it), skipping bills already covered by payments. Both scan by keyset
  in batches of 100; one failing contract does not stop the others.
- **Payments** (see [Payments and payment methods](#payments-and-payment-methods)): the `PaymentLedger` port is now the payment
  context's real ledger (wired in `bridges_payment.go`; `NoLedger` stays the default for tests): it feeds `payment_summary`, the
  "locked once somebody paid" rule and the "settle" step; `already_paid`/`PATCH paid` record a confirmed `recorded` payment of
  the money still owed (`Settle`, which never touches the status itself), reopening voids it (`Void`), deleting the bill removes
  its payments and proofs (`Purge`). The notification, audit and email ports are no-ops until
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
- **Proofs** are private files under `payments/YYYY/MM/<32 hex>` in the media store (`.webp` when the client sent WebP, `.jpg`
  otherwise, with a `_thumb.jpg` copy), written **before** the transaction and removed if it fails or the payment is withdrawn.
  The public `GET /v1/media/*key` route serves only `property/` keys, so a proof is reachable only through its signed link
  (HMAC-SHA256 over payment, file and expiry, key derived from `DATA_ENCRYPTION_KEY`, valid `PAYMENT_PROOF_URL_TTL_MINUTES`);
  only the two sides of a payment are given links. The bytes are served as stored, whatever the `.webp` in the name says.
- **Locks**: confirming locks the bill first and the payment second, the order the invoice use cases lock in, so the two cannot
  deadlock; submitting and recording lock the bill, so two parallel payments cannot together pay more than the total.
- **Events** (no-op ports until their phases; the unit tests check the calls): `payment_submitted` to the landlord (with
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

### Contract invitations (landlord)

A landlord offers a room on set terms and sends the offer as a link; whoever it is for opens it, signs in and accepts, and
the contract is made then (the answering side is the next batch). The offer is for **one account** (`recipient_id`, or an
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
  notification port is still a no-op, so no notification is stored yet. The email is Vietnamese only and is sent
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
`file_too_large`, `building_price_missing`, `building_price_in_use`, `range_invalid`, `range_too_many`,
`range_name_too_long`, `names_taken`, `single_cover`, `rental_period_pair`, `lte_field`, `date` (not `YYYY-MM-DD`),
`after_start_date`, `date_not_past`, `below_minimum_months` / `below_minimum_days` (params `amount`, `date`: the earliest end),
`unknown_service`, `previous_reading_required`, `reading_lower_than_previous`, `new_reading_required`, `nothing_to_bill`, `day_needs_end`, `invalid_period`, `period_billed` (bills), `recipient_invalid` / `own_email` / `recipient_mismatch` (who to invite), `required_without` (param `other`), `link_invalid` (an opt-out link), `amount_exceeds_remaining` (param `remaining`), `method_not_accepted`, `date_not_future`, `account_number_format`, `phone_format` (payments), `format` (a plan code), `datetime` (RFC 3339, `ends_at`).

Field names in `fields` are paths: a nested field is `charges.2.name` (list items are numbered from 0).
A state conflict (the request is fine but clashes with the current data) is 409, a bad field is 422.

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
`logs`, `migrate`/`migrate-down`/`migrate-status`, `seed`, `seed-demo`, `expire-subscriptions`, `send-invoices`, `send-reminders`, `migrate-media`, `fetch-locations`, `dev`, `hooks`.

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
In progress: phase 7, contract invitations (a landlord offers a room by link). Batch 1 is done: the
schema (`contract_invitations`, `invitation_email_blocks`), the invitation context with the exact lookup of an invitee and the
email opt-out, `PUT /v1/me/preferences`, the per-account rate limit, the opt-out signer, the error codes and the
`INVITATION_TTL_DAYS` setting. Batch 2 is done: the landlord's side (create with delivery by notification and email, list, get,
copy the link again, new link, withdraw), the invitation email and its templates. Next batch: answering (preview, accept,
decline, the invitee's inbox), the shared admission core, the room-deletion hook and the demo offer.
Done: phase 8, `payment` (schema; the landlord's payment methods with encrypted settings, a tenant's part or full payment with proof
pictures, confirm, reject, withdraw, the landlord's list, recorded payments, payment instructions with VietQR/MoMo links, signed
proof links, the real payment ledger for invoices, the billing summary and payment filters on the contract lists, demo seed).

Later phases (each module: migration, queries, service, handlers, tests): contract invitations,
`notification` (announcements, popups), `report`, `stats`, `audit`, plus
admin user management, landlord applications, manager hand-over and invitations, and social login.

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
| Audit: `MoneyRedaction` | `internal/audit` |
| Auth: `Pkce`, `ProviderLink`, `SignInMethods` | `internal/domain/identity` (social login) |
| Invitation: `InvitationAudience` | `internal/invitation` |
| Legal: `RichText` | `internal/legal` |
| Support: `RoleAccess` | `pkg/authz` (`MixesStaffWithOthers` exists; `assignableBy`, `canManage` with admin user management) |

Route skeleton for later phases (all under `/v1`): landlord `/buildings/{id}/rooms`, `/rooms/{id}`,
`/contract-invitations`, `/invoices` (done), `/invoices/{id}/payments` (done), `/my/payment-methods` (done), `/managers`; account
`/me/invoices`, `/me/notifications`; public `/public/rooms` (done); admin
`/admin/users`, `/admin/landlord-applications`, `/admin/announcements`,
`/admin/audit`; others `/reports`, `/stats/landlord`, `/stats/platform`.

### Web impact (separate workstream)

The Nuxt app (`rental-management-web`) reads the Laravel JSON shapes, so moving to this API is its own piece of work,
centred on `useApi` and the composables: `types/api.ts` (envelope, `ApiList`, pagination -> `{data}` / `{data, meta}` /
`{error}`), `utils/http.ts` (read `error.code` and `error.fields`), `useApi` (prefix `/v1`, new base URL, refresh reads
`data.tokens`, handle 204), `useAuthApi` (`data.user` + `data.tokens`; `name`/`phone` become `full_name`/`phone_number`;
new logout-all, change-password, verify/resend email), list pages (a `meta.page`/`meta.total` adapter, ideally in
`usePageSize`), and success toasts that today show the server `message` need local strings. Do it module by module,
as each Go module lands; the Laravel API keeps serving the web until then.

## Known gaps

- **Rooms (property)**: the plan port (it receives only the `ctx`) is the plan context's (see below); the room guards and observer are the contract and listing contexts' (combined in `bridges.go`).
  Room pictures stay in storage when the room is soft-deleted (history); orphaned files from a crash between the
  storage write and the commit are not swept (no background job yet). Copying a building with many rooms that have
  their own pictures duplicates every file before the transaction, so it can take a while.

- **Pictures in S3**: the driver is tested against a fake S3 (and SeaweedFS by hand); R2 and AWS S3 themselves are
  untested. Pictures are served by the API proxy unless `MEDIA_PUBLIC_BASE_URL` points at a public bucket domain;
  a failed upload after a database rollback leaves an orphan object, as with local files (no sweeper yet).
- **Pictures (property phase, platform part)**: Go has no WebP encoder and we take no cgo/WASM one, so a JPEG or PNG
  upload is stored as JPEG (metadata removed), while WebP (what the web sends) is stored as uploaded with its EXIF and
  XMP chunks removed. The EXIF orientation of a directly uploaded JPEG is not applied (the web always sends an upright
  WebP). Thumbnails are JPEG, made at upload time without a queue.

- **Contracts**: the `Notifier` and `AuditRecorder` ports are no-op defaults (nobody is notified, nothing is
  stored); the use cases call them where Laravel does and the unit tests check the calls, so the notification and audit phases
  only implement the ports (the `PlanLimits` port is the plan context's). No emails are sent for contracts. Not built yet: contract invitations (a
  landlord cannot create a contract directly: it is the tenant's registration, or later an invitation the tenant accepts), the
  manager (room manager) access, the landlord's `billing` fields beyond `unpaid_invoices` (`unbilled_periods`, `next_period`, `last_invoice`), the
  `my_registration` of find rooms (which rooms the viewer already registered for; the contract context can add it). Registration does not check that the landlord still holds the
  landlord role, and a tenant's list shows contracts on rooms the landlord deleted (history). The renter's detail lists the
  room's charges and pictures and a `billing` summary, but not the bills themselves.
- **Plans**: there is **no payment gateway**, so a paid plan is a request an admin activates by hand once the money arrived
  (nothing renews by itself; a subscription simply lapses at its end). The `Notifier` and `AuditRecorder` ports are no-op
  defaults: admins are not told of a request, users not of an activation, and `subscription.*`/`plan.saved` are not stored (the
  use cases call the ports where Laravel does and the unit tests check the calls). **There is no scheduler**: nothing runs
  `subscriptions-expire` by itself, so cron (or the orchestrator) must call it daily; a lapsed subscription is ignored by
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
  header says the same to browsers and shared caches). The `Notifier` and `AuditRecorder` ports are no-op defaults (the use
  cases call them where Laravel does and the unit tests check the calls); no emails are sent. A deleted room keeps its
  non-open listings (history). Listings and rooms are checked together only at write time: a room whose landlord loses the
  landlord role simply stops being offered. Not built: the limit of open listings per plan, listings drafted by a room manager,
  the 1000-room find-rooms demo seed.
- **Payments**: there is **no online gateway** (a driver seam exists; every payment is checked by hand by the landlord), no
  manager access (a manager with `payments.review`), and the notification, audit and `PaymentMailer` ports are no-ops
  (the use cases call them where Laravel does and the unit tests check the calls): nobody is told of a payment or its decision,
  nothing is stored in an audit trail, no email goes out. A bill passed on by link has no payer account, so only the landlord
  can record money on it. Go has no WebP encoder, so a proof sent as PNG/JPEG is stored as JPEG (WebP as sent) even though its
  link ends in `.webp`; thumbnails are made at upload (Laravel's `payments:generate-proof-thumbnails` has nothing to do).
  The files of a deleted bill or a withdrawn payment are removed best effort (a failure is logged, an unreferenced private file
  is left); a proof written before a transaction that then rolls back is removed again, but a crash between the two leaves an
  orphan (no sweeper). A ledger built for the daily invoice commands has no file store (they never delete bills). The
  bank list is fixed in the domain (`domain/payment.Banks`); `GET /config` does not exist yet to hand it to the web.
- **Invoices**: see [Invoices](#invoices). The notification, audit and email ports are no-ops: nobody is told
  of a bill (so `delivery: app` only means "visible to the tenant's account"), nothing is stored in an audit trail and no
  `InvoiceMail` goes out; the unit tests check the calls. Nothing runs the daily commands by itself (cron must call
  `send-invoices` and `send-reminders`). Not built: the tenant's own invoice list (`/me/invoices`), bulk bill creation, a plan limit for this feature, room managers' access.
- **Invitations (batches 1 and 2)**: the invitee lookup, the opt-out and the landlord's side exist; **nobody can answer an
  invitation yet** (preview, accept, decline and the invitee's inbox are the next batch), so an offer only waits, expires or is
  withdrawn, and a deleted room does not yet withdraw its offers. The notification and audit ports are no-op defaults (nothing
  is stored; `delivery.app` is still truthful because the offer sits in the account's inbox once that route exists). The
  invitation email is Vietnamese only (no account language is known) and is sent synchronously after the commit; expired
  offers and their sealed secrets are not pruned; the lookup has no avatar. An account's own address is also checked against
  the opt-out list before it is mailed (the plan only checked addresses without an account).
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
