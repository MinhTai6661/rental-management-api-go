# rental-management-api-go

Go backend of the rental-management project (multi-tenant boarding-house rental), written from scratch as a **clean
break**: it keeps the business rules of the Laravel API, which stays in this monorepo as a *business reference only*,
and shares nothing with it - not the database, not the tokens, not the JSON shapes, no compatibility layer.

Status: **phase 1** - platform, location (provinces/wards) and identity (register, email verification, login,
refresh, logout, role switch, password change). Later phases are listed in the [roadmap](#roadmap).

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
| `MEDIA_DIR` | `var/media` | where picture files live (compose: volume at `/data/media`) |
| `MEDIA_PUBLIC_BASE_URL` | `http://localhost:8090/v1/media` | absolute prefix of every picture URL in responses; no trailing slash |
| `MEDIA_MAX_UPLOAD_BYTES` / `MEDIA_MAX_DIMENSION` / `MEDIA_THUMBNAIL_MAX_SIZE` | `2097152` / `2048` / `480` | file bytes, longest side and thumbnail longest side (px) |
| `MEDIA_MAX_PER_OWNER` | `10` | pictures per room or building when the plan sets no limit |
| `IMAGE_PROCESSING_CONCURRENCY` | `2` | pictures decoded at the same time |
| `SEED_DEMO_PASSWORD` | - | password of the `seed -demo` accounts; the command refuses to run in production |

## Layout and dependency rules

```
cmd/rental-management/        the one binary: api | migrate [up|down|status] | seed [-demo]
db/migrations/                goose migrations, embedded in the binary
db/queries/<module>/          the SQL sqlc turns into Go (all of it generates into the one package internal/adapter/persistence/postgres/sqlc, committed, never edited by hand)
tools/fetchlocations/         one-off download of the provinces/wards snapshot (internal/modules/location/data/locations.json.gz)

config/                       environment -> validated Config
pkg/                          INFRASTRUCTURE shared by every module; no business rules
  httpx/                    envelope, errors, pagination, validation, middleware, CORS, auth and rate-limit middleware
  apperr/                   the error type services return + the error-code and validation-rule catalog
  authz/                    pure authorization rules: roles, active-role resolution, permissions, Principal
  i18n/                     vi/en messages (go-i18n, embedded JSON) and Accept-Language negotiation (x/text/language)
  db/                       pgx pool, transactions, error helpers, goose runner
  ratelimit/                per-client limits on Redis (redis_rate, GCRA); the Limiter interface the middleware uses
  mail/                     Mailer port: SMTP (go-mail), log, test recorder; vi/en email templates
  logger/                   slog setup
  testdb/                   throwaway Postgres/Redis containers + the guard for integration tests

internal/
  app/                        wiring: builds the dependencies and the router, runs the server and the commands
  router/                     route registration (gin groups), one file per feature: auth_routes.go
  handler/                    HTTP handlers (gin), one file per feature: auth_handler.go
  service/                    use cases + pure business rules; declares the repository interfaces it needs (auth_ports.go)
  repository/                 database and Redis: the only code that imports pgx / go-redis and the generated sqlc package
    sqlc/                     the one generated sqlc package (all queries of db/queries)
  model/                      plain structs: entities, request and response bodies, domain errors (ErrNotFound, ConstraintError)
  modules/                    features not moved to the layers yet (a feature directory has its own routes, handler, service, store)
    location/                 provinces and wards (public, rate limited, cacheable)
    property/                 buildings, rooms, charges and pictures (upload, cover, arrangement, public serving); one module because a building edit reaches its rooms in one transaction
```

Identity (accounts, sessions, tokens, passwords, email verification, role/permission catalog + seed) is already split
into these layers and is the template: `model/{user,session,auth_request,auth_response,permission}.go`,
`repository/{user,session,verification,permission,revocation}_repository.go`, `service/auth_*.go` + `identity_seed.go`,
`handler/auth_handler.go`, `router/auth_routes.go`. `location` and `property` are still being moved the same way.

Where does new code go? Business behaviour -> the layers above. A technical tool several features share (HTTP,
database, mail, i18n...) -> `pkg/` (settings -> `config/`). Nothing else goes in `internal/`.

Rules (checked in review and by the linters, see `.golangci.yml`):

- Direction: `router -> handler -> service -> model`, and `repository -> model`; `app` wires them. Everything may use `pkg` and `config`, never the reverse.
- **service** declares the interfaces it needs from the outside (repositories, transaction manager) and never imports `repository`, gin, pgx or go-redis; the concrete repositories satisfy those interfaces implicitly.
- **repository** returns model types, never sqlc rows, and translates driver errors: no rows -> `model.ErrNotFound`, unique / foreign key violations -> `*model.ConstraintError` (`model.IsUnique`, `model.IsForeignKey`).
- **Transactions travel in the context**: `service` calls `tx.WithinTx(ctx, func(ctx) error {...})` and every repository call made with that ctx joins the transaction (`repository.TxManager`, `queries(ctx, pool)`).
- **model** imports only the standard library, uuid and `pkg/authz` constants.
- **gin is imported only** by `pkg/httpx`, `app`, `handler` and `router`.
- All error-to-HTTP mapping happens in `httpx.Fail`; services return `apperr` values (a kind, a stable code, params).
- SQL only under `db/`; no global mutable state, no `init()`; pass `c.Request.Context()` down; comments in English.
- There is only one generated query package, `internal/adapter/persistence/postgres/sqlc` (query names are unique across all of `db/queries`).
- Pure business rules (calculations, state machines) live in small, DB-free functions with table tests; services are also tested with hand-written fakes of the repository interfaces (no database).
- Feature modules still in `internal/modules` never import each other or the layers.

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
about changes (blank contracts, listings back to draft) are ports with no-op defaults (`RoomGuards`, `RoomObserver`) wired
in `internal/app` when the contract and listing modules arrive.

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
tag cannot send a bearer token); payment proofs, later, will use signed URLs, not this route.

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
| `server.busy` | 503 | statement/lock timeout, request deadline, Redis or database down |
| `server.internal` | 500 | anything else (the cause is only logged) |

Validation rule keys (`validation.<rule>`): `required`, `email`, `min_string`, `max_string`, `min_number`,
`max_number`, `integer`, `uuid`, `oneof`, `accepted`, `unique`, `exists`, `role_not_held`, `password_incorrect`,
`password_same`, `min_items`, `max_items`, `distinct`, `image_type`, `image_dimensions`, `image_corrupt`,
`file_too_large`, `building_price_missing`, `building_price_in_use`, `range_invalid`, `range_too_many`,
`range_name_too_long`, `names_taken`, `single_cover`, `rental_period_pair`, `lte_field`.

Field names in `fields` are paths: a nested field is `charges.2.name` (list items are numbered from 0).
A state conflict (the request is fine but clashes with the current data) is 409, a bad field is 422.

## Auth model

- **Roles and permissions**: five roles (`super_admin`, `admin`, `landlord`, `manager`, `tenant`); a person holds
  several, but staff roles (`super_admin`, `admin`) are never mixed with the others (`authz.MixesStaffWithOthers`).
  Register grants `tenant` only. The permission catalog is Go data (`internal/model/permission.go`), written to
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
2. Queries in `db/queries/<module>/*.sql`, then `make sqlc` (no hand-edits in `internal/adapter/persistence/postgres/sqlc`).
3. One file per layer, named after the feature (see identity): `internal/model/<feature>.go`, `internal/repository/<feature>_repository.go`,
   `internal/service/<feature>_service.go` + `<feature>_ports.go` (returns `apperr`), `internal/handler/<feature>_handler.go`,
   `internal/router/<feature>_routes.go`; pure rules in small DB-free functions with table tests. (location and property still use `internal/modules/<module>/` until they are moved.)
4. New permissions: add them to `internal/model/permission.go` and the role matrix, then `make seed`.
5. New error codes / validation rules: constants + `Codes()`/`Rules()` in `apperr` and the vi and en locale files.
6. Paginate every list (`httpx.ParsePage` + meta); only bounded reference data may skip it.
7. Mount the module in `internal/app` and add its routes to the API table above.
8. Tests: unit for the rules, integration (`//go:build integration`, `pkg/testdb`) for the flows; run
   `make check lint test-integration`.

## Roadmap

Done: phase 2, `property` (schema; buildings and rooms API with inheritance, propagation, runs, copy, bulk, reorder, the
demo seed; pictures with upload, cover, arrangement, replace, delete, inheritance, copy and public serving).
Next: `contract` (phase 3: blank contracts open and close through the `RoomObserver`/`RoomGuards` ports).

Later phases (each module: migration, queries, service, handlers, tests): `listing`, `contract`,
`invoice`, `payment`, `notification` (announcements, popups), `report`, `plan` (subscriptions), `stats`, `audit`, plus
admin user management, landlord applications, manager hand-over and invitations, and social login.

Pure rules of the Laravel project to port (as Go table tests, taking its `tests/Unit` cases as the starting point):

| Laravel `app/Domain` / `Support` | Go package |
|---|---|
| Billing: `BillingPeriodCalculator`, `InvoiceCalculator`, `ServiceCharge`, `RoomBillSummary`, `PaymentLabel`, `BillingRuleViolation` | `internal/invoice` (`PaymentLabel` -> `internal/payment`) |
| Contract: `ContractLifecycle`, `MinimumRentalPeriod` | `internal/contract` |
| Building: `RoomNameRange`, `RoomInheritance` | `internal/modules/property` (`rules.go`, ported and tested) |
| Listing: `ListingLifecycle` | `internal/listing` |
| Manager: `ManagerCapability`, `ManagerEligibility` | `internal/manager` |
| Plan: `PlanAssignment`, `PlanLimit` | `internal/plan` |
| Statistics: `MonthRange`, `OccupancyCalculator`, `Ratio` | `internal/stats` |
| Announcement: `AudienceCriteria`, `DayRange`, `PopupVisibility` | `internal/notification` |
| Audit: `MoneyRedaction` | `internal/audit` |
| Auth: `Pkce`, `ProviderLink`, `SignInMethods` | `internal/service` (identity, social login) |
| Invitation: `InvitationAudience` | `internal/invitation` |
| Legal: `RichText` | `internal/legal` |
| Support: `RoleAccess` | `pkg/authz` (`MixesStaffWithOthers` exists; `assignableBy`, `canManage` with admin user management) |

Route skeleton for later phases (all under `/v1`): landlord `/buildings/{id}/rooms`, `/rooms/{id}`,
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

- **Rooms (property)**: the plan, room-guard and room-observer ports have no-op defaults until the plan and contract
  modules are wired in `internal/app`; deleting a room does not tell the observer (phase 3 will add what it needs).
  Room pictures stay in storage when the room is soft-deleted (history); orphaned files from a crash between the
  storage write and the commit are not swept (no background job yet). Copying a building with many rooms that have
  their own pictures duplicates every file before the transaction, so it can take a while.

- **Pictures (property phase, platform part)**: Go has no WebP encoder and we take no cgo/WASM one, so a JPEG or PNG
  upload is stored as JPEG (metadata removed), while WebP (what the web sends) is stored as uploaded with its EXIF and
  XMP chunks removed. The EXIF orientation of a directly uploaded JPEG is not applied (the web always sends an upright
  WebP). Thumbnails are JPEG, made at upload time without a queue.

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
