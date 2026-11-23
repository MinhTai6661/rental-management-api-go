# rental-management-api-go

Go backend of the rental-management project (multi-tenant boarding-house rental), written from scratch as a **clean
break**: it keeps the business rules of the Laravel API, which stays in this monorepo as a *business reference only*,
and shares nothing with it - not the database, not the tokens, not the JSON shapes, no compatibility layer.

Status: **phase 1** - platform, location (provinces/wards) and identity (register, email verification, login,
refresh, logout, role switch, password change). Later phases are listed in the [roadmap](#roadmap).

Stack: [Gin](https://gin-gonic.com) (HTTP) - [pgx](https://github.com/jackc/pgx) + [sqlc](https://sqlc.dev) +
[goose](https://github.com/pressly/goose) (Postgres 18) - go-redis (revoked tokens, rate limits) - `log/slog` -
argon2id + JWT (golang-jwt) - testcontainers (integration tests). Go 1.26.

## Quick start

Nothing needs to be installed except Docker: every Go tool (compiler, sqlc, golangci-lint, air) runs in a container
through the `Makefile`.

```bash
make up          # api (air live reload), postgres, redis, mailpit
make migrate     # apply db/migrations to the dev database
make seed-demo   # roles, permissions, provinces, wards + the demo accounts (safe to repeat)
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
| `SEED_DEMO_PASSWORD` | - | password of the `seed -demo` accounts; the command refuses to run in production |

## Layout and dependency rules

```
cmd/rental-management/        the one binary: api | migrate [up|down|status] | seed [-demo]
db/migrations/                goose migrations, embedded in the binary
db/queries/<module>/          the SQL sqlc turns into Go (internal/modules/<module>/sqlcgen, committed, never edited by hand)
tools/fetchlocations/         one-off download of the provinces/wards snapshot (internal/modules/location/data/locations.json.gz)

internal/
  app/                        wiring: builds the dependencies and the router, runs the server and the commands
  modules/                    BUSINESS: one directory per feature, each with routes, handler, service, store, rules
    identity/                 accounts, sessions, tokens, passwords, email verification, role/permission catalog + seed
    location/                 provinces and wards (public, rate limited, cacheable)
  platform/                   INFRASTRUCTURE shared by every module; no business rules
    config/                   environment -> validated Config
    httpx/                    envelope, errors, pagination, validation, middleware, CORS, auth and rate-limit middleware
    apperr/                   the error type services return + the error-code and validation-rule catalog
    authz/                    pure authorization rules: roles, active-role resolution, permissions, Principal
    i18n/                     vi/en messages and Accept-Language negotiation
    db/                       pgx pool, transactions, error helpers, goose runner
    ratelimit/                fixed-window counters, in memory or in Redis
    mail/                     Mailer port: SMTP, log, test recorder; vi/en email templates
    logger/                   slog setup
    testdb/                   throwaway Postgres/Redis containers + the guard for integration tests
```

Where does new code go? Business behaviour of a feature -> `internal/modules/<feature>/`. A technical tool several
features share (HTTP, database, mail, i18n...) -> `internal/platform/`. Nothing else goes in `internal/`.

Rules (checked in review and by the linters):

- Direction: `modules -> platform` only, never the reverse; a module never imports another module. Inside a module: `handler -> service -> store / pure rules / ports`. Services and pure rules never see HTTP or gin.
- **gin is imported only** by `httpx`, `app` and the `handler.go` / `routes.go` files of a module.
- All error-to-HTTP mapping happens in `httpx.Fail`; services return `apperr` values (a kind, a stable code, params).
- SQL only under `db/`; no global mutable state, no `init()`; pass `c.Request.Context()` down; comments in English.
- A module never imports another module's `sqlcgen`.
- Pure business rules (calculations, state machines) live in small, DB-free functions with table tests.

## API (phase 1)

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
| `server.busy` | 503 | statement/lock timeout, request deadline, Redis or database down |
| `server.internal` | 500 | anything else (the cause is only logged) |

Validation rule keys (`validation.<rule>`): `required`, `email`, `min_string`, `max_string`, `min_number`,
`max_number`, `integer`, `uuid`, `oneof`, `accepted`, `unique`, `exists`, `role_not_held`, `password_incorrect`,
`password_same`.

## Auth model

- **Roles and permissions**: five roles (`super_admin`, `admin`, `landlord`, `manager`, `tenant`); a person holds
  several, but staff roles (`super_admin`, `admin`) are never mixed with the others (`authz.MixesStaffWithOthers`).
  Register grants `tenant` only. The permission catalog is Go data (`internal/modules/identity/catalog.go`), written to
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
- **Rate limits** (per client address, fixed window, in Redis): see the route table; login also limits per address +
  hashed email. Set up trusted proxies before putting a reverse proxy in front (gin currently trusts none, so
  `X-Forwarded-For` cannot be spoofed).

## Testing

- **Unit tests** (`make test`, part of `make check`): pure rules, config, i18n, JSON shapes. No Docker needed.
- **Integration tests** (`make test-integration`, build tag `integration`): `internal/platform/testdb` starts its own Postgres 18
  and Redis containers (label `rental-management.test=1`) through the Docker socket and clones a migrated template
  database per test. The suite covers migrations up/down/up, idempotent seeding, the location endpoints and the full
  auth flows (verify, login, refresh rotation and reuse, logout, role switch, password change, rate limits, Redis down).
- **They never touch a dev database.** The guard refuses any target that is not its own container, any database not
  named `rental_management_test_*` / the template, any user but the test user, and the ports of the dev databases
  (5432, 5442, 6379, 6389). The tests never read `DATABASE_URL` / `REDIS_URL`. This exists because a test run once
  wiped a dev database: keep it.

## Makefile

`make help` lists everything. Main targets: `check` (gofmt check + vet + unit tests), `lint`, `fmt`, `fmt-check`,
`tidy`, `sqlc` (regenerate `internal/*/sqlcgen`; commit the result), `build`, `test`, `test-integration`, `up`/`down`/
`logs`, `migrate`/`migrate-down`/`migrate-status`, `seed`, `seed-demo`, `fetch-locations`, `dev`, `hooks`.

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
2. Queries in `db/queries/<module>/*.sql`, then `make sqlc` (no hand-edits in `sqlcgen`).
3. `internal/modules/<module>/`: `store.go` (wraps `sqlcgen`), `service.go` (returns `apperr`), `handler.go` + `routes.go` (the
   only gin files), pure rules in small DB-free functions with table tests.
4. New permissions: add them to `internal/modules/identity/catalog.go` and the role matrix, then `make seed`.
5. New error codes / validation rules: constants + `Codes()`/`Rules()` in `apperr` and the vi and en locale files.
6. Paginate every list (`httpx.ParsePage` + meta); only bounded reference data may skip it.
7. Mount the module in `internal/app` and add its routes to the API table above.
8. Tests: unit for the rules, integration (`//go:build integration`, `internal/platform/testdb`) for the flows; run
   `make check lint test-integration`.

## Roadmap

Later phases (each module: migration, queries, service, handlers, tests): `building`, `room`, `listing`, `contract`,
`invoice`, `payment`, `notification` (announcements, popups), `report`, `plan` (subscriptions), `stats`, `audit`, plus
admin user management, landlord applications, manager hand-over and invitations, and social login.

Pure rules of the Laravel project to port (as Go table tests, taking its `tests/Unit` cases as the starting point):

| Laravel `app/Domain` / `Support` | Go package |
|---|---|
| Billing: `BillingPeriodCalculator`, `InvoiceCalculator`, `ServiceCharge`, `RoomBillSummary`, `PaymentLabel`, `BillingRuleViolation` | `internal/invoice` (`PaymentLabel` -> `internal/payment`) |
| Contract: `ContractLifecycle`, `MinimumRentalPeriod` | `internal/contract` |
| Building: `RoomNameRange`, `RoomInheritance` | `internal/building`, `internal/room` |
| Listing: `ListingLifecycle` | `internal/listing` |
| Manager: `ManagerCapability`, `ManagerEligibility` | `internal/manager` |
| Plan: `PlanAssignment`, `PlanLimit` | `internal/plan` |
| Statistics: `MonthRange`, `OccupancyCalculator`, `Ratio` | `internal/stats` |
| Announcement: `AudienceCriteria`, `DayRange`, `PopupVisibility` | `internal/notification` |
| Audit: `MoneyRedaction` | `internal/audit` |
| Auth: `Pkce`, `ProviderLink`, `SignInMethods` | `internal/modules/identity` (social login) |
| Invitation: `InvitationAudience` | `internal/invitation` |
| Legal: `RichText` | `internal/legal` |
| Support: `RoleAccess` | `internal/platform/authz` (`MixesStaffWithOthers` exists; `assignableBy`, `canManage` with admin user management) |

Route skeleton for later phases (all under `/v1`): landlord `/buildings`, `/buildings/{id}/rooms`, `/rooms/{id}`,
`/listings`, `/contracts`, `/invoices`, `/invoices/{id}/payments`, `/payment-methods`, `/managers`; account
`/me/contracts`, `/me/invoices`, `/me/notifications`, `/me/subscription`; public `/public/rooms`; admin
`/admin/users`, `/admin/landlord-applications`, `/admin/plans`, `/admin/subscriptions`, `/admin/announcements`,
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

- **Rehash on login** is not implemented: if the argon2 parameters change, old hashes stay until the password changes.
- No rate limit per *account* on password change (only per client address, 5/min).
- Graceful shutdown (including waiting for emails still being sent) is implemented but its log line was not observed.
- Sending real mail over SMTP with STARTTLS/authentication is untested (only Mailpit, and rendering/MIME in tests).
- When a session is closed (reuse detected, inactive account) and Redis is down, the database revocation holds (the
  refresh token stops working) but the still-valid access tokens of that session live until they expire (15 min): the
  failure is logged, not returned.
- Reverse-proxy deployment needs a trusted-proxies setting (gin trusts none now, so client IPs behind a proxy are the
  proxy's).
- `make dev` (air outside compose) is unverified; compose runs air and is.
- The provinces/wards data is a snapshot (`make fetch-locations` refreshes it when the divisions change).
