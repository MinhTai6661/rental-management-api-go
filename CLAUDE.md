# rental-management-api-go

Backend Go của hệ thống quản lý phòng trọ nhiều chủ trọ (multi-tenant). Viết lại từ đầu, **tách hẳn** khỏi bản Laravel cũ
(`../rental-management-api-laravel`, chỉ còn là tham chiếu nghiệp vụ): không dùng chung DB, token hay dạng JSON.
Frontend Nuxt nằm ở `../rental-management-web`. **README.md là tài liệu đầy đủ** (layout, API, mã lỗi, auth, roadmap, known gaps) — đọc trước khi sửa.

Trả lời người dùng bằng **tiếng Việt**; comment trong code bằng **tiếng Anh**, ngắn gọn.

## Stack
Go 1.26, Gin, pgx + sqlc + goose (Postgres), Redis (rate limit, thu hồi token), golang-jwt, argon2id, go-mail, go-i18n (vi/en),
testcontainers-go. Dev: API cổng 8090, Postgres 5442, Redis 6389, Mailpit UI 8026 (`make up`).

## Kiến trúc: Clean Architecture
Phụ thuộc chỉ đi vào trong: `adapter → usecase → domain`.
- `internal/domain/<context>/`: luật thuần (entity, enum, lỗi nghiệp vụ). Chỉ stdlib + uuid, không struct tag.
- `internal/usecase/<context>/`: hành động nghiệp vụ; **ports (interface) khai báo ở `ports.go`**. Không import gin, pgx, redis, JWT, `pkg/db|mail|storage|imaging` hay `internal/adapter/**`.
- `internal/adapter/`: cài các ports. `http/<context>/` (handler, route, DTO), `persistence/postgres/<context>/` (repository; `sqlc/` là MỘT package sinh code), cộng `redis`, `security`, `mail`, `filestore`, `imageproc`, `snapshot`.
- `cmd/rental-management/`: **composition root** — nơi duy nhất biết mọi lớp và nối dependency (`api.go`, `router.go`, `commands.go`, `bridges*.go`). Các context **không import nhau**; nói chuyện qua interface nhỏ nối trong `bridges*.go`.
- `pkg/`: công cụ dùng chung không chứa nghiệp vụ (`authz`, `db`, `i18n`, `imaging`, `logger`, `mail`, `ratelimit`, `secretbox`, `storage`, `testdb`). `config/` đọc biến môi trường.
- `db/migrations/` (goose, đánh số tiếp) và `db/queries/<context>/` (sqlc, tên query duy nhất toàn dự án).
- **depguard trong `.golangci.yml` chặn import sai hướng** — đừng nới luật để cho qua, hãy sửa thiết kế.
- Transaction đi qua `context` (`TxManager.WithinTx`). Repository trả kiểu domain, không trả row sqlc. Không có dòng → `shared.ErrNotFound`; vi phạm unique/FK → `*shared.ConstraintError`.

## Thêm một tính năng
migration → query sqlc (`make sqlc`) → domain → usecase + ports → adapter http + persistence → nối trong `cmd/rental-management/` →
test theo lớp (unit bằng fake cho luật và use case; integration `//go:build integration` cho repository và HTTP).
Quyền mới vào `internal/domain/identity/catalog.go` rồi `make seed`. Thêm khối luật depguard cho context mới.

## Quy ước dữ liệu và API
- JSON thành công `{"data": ...}`; danh sách thêm `meta`. Lỗi `{"error":{"code","message","fields"}}`, mã lỗi ổn định, thông báo vi/en (khóa i18n cho mỗi mã).
- **Mọi danh sách đều phân trang** (`httpx.ParsePage`), trừ dữ liệu danh mục cố định/nhỏ.
- Id là UUID v7; tiền là `bigint` VND (hậu tố `_vnd`); cột `snake_case`, thời gian `timestamptz`.
- Mỗi tập giá trị cố định (trạng thái…) là một kiểu chuỗi có hàm `Parse` trong domain; ràng buộc CHECK trong DB giữ làm lớp bảo vệ cuối.
- Link bí mật (lời mời, hóa đơn) chỉ lưu hash; bản có thể sao chép lại được mã hóa bằng `pkg/secretbox` (cần `DATA_ENCRYPTION_KEY`).
- Các lệnh định kỳ (`subscriptions-expire`, `invoices-send-automatic`, `invoices-send-reminders`, `notifications-prune`, `announcements-dispatch-due`) do `rental-management worker` chạy (`make worker`, khóa Redis để nhiều bản không chạy trùng); vẫn gọi tay hoặc bằng cron được.

## Lệnh
Máy host chỉ có Go 1.17 nên **mọi công cụ Go chạy trong Docker qua Makefile — đừng chạy `go` trực tiếp**:
`make fmt-check vet lint test check` (nhanh) · `make test-integration` (container Postgres/Redis tạm, ~3 phút) · `make sqlc` · `make tidy` ·
`make migrate|seed|seed-demo` · `make up|down|logs`. Tài khoản demo: `superadmin@ admin@ landlord@ tenant@rental.test` (mật khẩu dev trong compose).

## Quy tắc an toàn
- **Test chỉ chạy trên container tạm** do `pkg/testdb` tạo (có chốt chặn từ chối mọi DB khác). Không bao giờ đọc `DATABASE_URL`/`REDIS_URL` trong test và không chạy test vào DB dev.
- **Không tạo hay sửa file `.env`** (đưa dòng cần thêm để người dùng tự đổi). **Không `docker build`** — người dùng tự build.
- Không đụng stack Laravel (container `myrent_*`, DB `rental_management_2` cổng 5432, Redis 6379).

## Git
Commit theo Conventional Commits (`type(scope): subject`, ≤ 72 ký tự), **không thêm `Co-Authored-By` hay "Generated with Claude Code"**.
**Chỉ commit khi người dùng bảo.** Hook: `make hooks` (commit-msg + pre-push chạy kiểm tra).
