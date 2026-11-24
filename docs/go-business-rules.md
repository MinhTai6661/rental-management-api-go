# Luật nghiệp vụ cần mang sang Go (rút từ code Laravel cũ)

Nguồn: `rental-management-api-laravel` (code + test). Chỉ ghi phần CHƯA có ở Go (đã có: platform, location, identity cơ bản, property, contract, plan, listing, invoice, payment, notification, announcements, contract_invitations). Ghi chú "Go" = so với repo Go hiện tại.
Quy ước: mã lỗi 422 = `ValidationException` của Laravel; 403 = policy/permission từ chối; "Cần hỏi" = chưa quyết, KHÔNG tự đoán.

---

## 1. admin (người dùng, vai trò, đơn chủ trọ, cài đặt, pháp lý, quyền chủ thể dữ liệu)

### Bảng/field cần thêm
- `landlord_applications`: `id uuid`, `user_id`, `business_name ≤150`, `phone ≤20`, `ward_code?`, `address_detail? ≤500`, `note? ≤1000`, `status` pending_review|approved|rejected, `reviewed_by_id?`, `reviewed_at?`, `review_note?`, `created_at`. (Không còn trạng thái "chờ xác nhận email".) Withdraw = xóa dòng đang `pending_review`.
- `landlord_profiles`: một dòng/tài khoản (`user_id` unique), `business_name`, `phone`, `ward_code`, `address_detail`, `description ≤2000`. Tách khỏi hồ sơ cá nhân trên `users`.
- `legal_documents`: `id`, `kind` terms|privacy, `version` (chuỗi `Y-m-d.His` lúc đăng), `content` json `{vi:{title,body},en:{title,body}}` (body = TipTap), `change_summary` json `{vi,en}`, `published_by_id`, `created_at`. Chỉ INSERT, không sửa/xóa.
- `app_settings`: `key` PK, `value` json, `updated_by_id`, timestamps. Lịch sử đổi nằm ở audit (`setting.changed`), không có bảng riêng.
- `users` thêm: `terms_version`, `terms_accepted_at` (Go đã có `terms_version`), `accepts_invitations bool default true` (Go đã có), `erased_at?`, `deleted_at` (xóa mềm).
- `invitation_email_blocks` (băm email từ chối mời) — thuộc mục 7/invitation, chỉ nhắc.

### Luật
**Người dùng (admin)**
1. Danh sách `GET /admin/users`: luôn loại chính người xem; loại mọi tài khoản có role thuộc `staffRolesHiddenFrom(actor)` — super_admin thấy tất cả; mọi actor khác (admin) KHÔNG thấy tài khoản admin/super_admin (kể cả chính mình bị loại). — `HasRoleScopes::scopeVisibleTo`, test "lists staff accounts only to a super admin".
2. Bộ lọc: `status` (enum UserStatus), `role` (id), `role_name` ∈ {tenant,landlord,manager,admin,super_admin} (giá trị khác → 422), `search` (một từ, `lower(name) LIKE` hoặc `lower(email) LIKE`, không phân biệt hoa thường, phải escape `%_\`), `name`/`email` (LIKE), `ward_code`, `province_code` (bỏ qua nếu có ward_code), sort `name|email|created_at` (mặc định created_at desc), phân trang. — `UserService::paginate`.
3. Xóa người dùng (`UserPolicy::delete`), theo thứ tự: tự xóa mình → 403; target có role `super_admin` → 403 (kể cả actor là super admin); actor `super_admin` → được; ngược lại `RoleAccess::canManage`: MỌI role của actor phải cho phép MỌI role của target. Bảng `MANAGEABLE`: `admin → [landlord, manager, tenant, (không role)]`; role khác không quản lý ai. Target không role được coi là `null` và được admin xóa. Hệ quả: admin không xóa được admin/super_admin. Xóa = xóa mềm + audit `user.deleted` (meta roles). — test "lets nobody delete a super admin…".
4. Xóa hàng loạt `POST /admin/users/bulk-delete {ids[1..100] uuid distinct}`: chạy policy từng người; trả `{done:[ids], failed:[{id,name,message}]}`, 200 dù có failed; id không tồn tại bị bỏ qua im lặng.
**Vai trò**
5. `PUT /admin/users/{id}/roles {roles:[roleId...] (present, có thể rỗng), note?}` (`RolePolicy::updateUserRoles`), từ chối 403 nếu: actor = target; role mới chứa `super_admin`; target đang có `super_admin`. Sau đó: actor super_admin → cho; ngược lại cần `canManage(actorRoles, targetRoles)` VÀ `canManage(actorRoles, newRoles)` (admin: chỉ target & role mới ⊂ {landlord, manager, tenant}; admin không sửa role admin khác). Nếu role mới rỗng thì `newRoles=[]` ⇒ `canManage([admin], [])` đang cho phép (xem Cần hỏi 1.3).
6. Staff không trộn: tập role mới có admin/super_admin cùng role khác (`RoleAccess::mixesStaffWithOthers`) → 422 `roles`. (Go đã có `ValidateRoleGrant`/`HasStaffRole` — dùng lại.)
7. Nếu target đang có `landlord` mà role mới không có → coi là thu hồi: gọi `LandlordService::revoke` (luật 14–15) TRƯỚC khi sync; nếu mới thêm `landlord` → `grantRole` (luật 12).
8. Sau sync: xóa cache quyền, audit `roles.changed` với `changes.roles {from:[sorted names], to:[sorted names]}`, meta target_user_id.
9. `GET /admin/roles` (quyền `users.update.roles`): trả đúng các role actor được phân (`assignableBy`): super_admin = tất cả; admin = `[landlord, manager, tenant]`; role khác không có quyền route. Phân trang/ `per_page`.
**Đơn chủ trọ**
10. Nộp `POST /my/landlord-application` (chỉ tài khoản đang ở chế độ tenant; throttle 10/giờ tên `landlord-application-store`): đã có role landlord → 422 `application` (already_landlord); không có role tenant HOẶC trộn staff → 422 (not_renter); đã có đơn `pending_review` → 422 (already_pending; kiểm tra trong transaction có khóa dòng user). Tạo đơn `pending_review` ngay (không xác nhận email). Báo role `admin` (KHÔNG báo super_admin): notification `landlord_application_received` {applicant,business} url `/admin/landlord-applications` + email. Audit `landlord_application.submitted`.
11. `DELETE /my/landlord-application`: xóa đơn đang mở; có xóa thì audit `…withdrawn`. `GET` trả đơn mới nhất của mình.
12. Duyệt: đơn phải `pending_review` còn không → 422 `application` (not_reviewable). Duyệt: đặt status/reviewed_by/at/note; gắn role `landlord` (giữ `tenant`); `firstOrCreate` landlord_profile từ đơn (thiếu thì lấy `users.name/phone/ward/address`); nếu landlord CHƯA TỪNG có tòa nhà (kể cả đã xóa mềm) tạo một tòa nhà đầu tiên tên = `business_name` (cắt 50, rỗng → tên mặc định) cùng địa chỉ profile, `display_order=1`, audit `building.created` meta `automatic:true`. Báo người nộp (`landlord_application_approved` url `/become-landlord`, email). Audit `landlord_application.approved`.
13. Từ chối: `note` bắt buộc (≤1000) → status rejected; người nộp được báo `landlord_application_rejected` (kèm reason), có thể nộp lại. Audit `…rejected`.
14. Thu hồi landlord (`POST /admin/users/{id}/revoke-landlord`, 404 nếu không phải landlord): đếm hợp đồng `active` của phòng chủ trọ; > 0 → 422 `roles` (has_active_contracts, count). Hợp lệ: gỡ role landlord, nếu `last_active_role=landlord` thì về null; giữ nguyên phòng/hợp đồng/profile; audit `landlord.revoked` meta `{reason, impact}`; báo chủ trọ `landlord_revoked` url `/profile`. Phòng thôi được đăng (find-rooms yêu cầu chủ còn role landlord).
15. `GET /admin/users/{id}/landlord-impact` (404 nếu không phải landlord): `{rooms, active_contracts, pending_registrations, unpaid_invoices (hóa đơn sent), can_revoke = active_contracts==0}`.
16. Danh sách đơn `GET /admin/landlord-applications?status=(mặc định pending_review)&search&sort_by=newest|oldest|name&page&per_page`: search `lower(business_name)`/`phone`/tên-email người nộp (escape `%_\`); trả kèm `counts` theo status (toàn bộ, không theo search); chi tiết `GET …/{id}` kèm `history` = các đơn khác cùng người (id,status,business_name,review_note,created_at,reviewed_at), mới nhất trước.
17. Hàng loạt `POST …/bulk {action approve|reject, ids[1..100], note (bắt buộc khi reject)}`: từng đơn qua luật 12/13; đơn không duyệt được → `failed[{id,name=business_name,message}]`, còn lại vẫn chạy; trả `{done,failed}`.
18. Hồ sơ chủ trọ `GET|PUT /landlord/profile` (chỉ landlord; PUT upsert; audit `landlord.profile_updated` chỉ ghi TÊN trường đổi). Hồ sơ bắt buộc `business_name`.
**Cài đặt hệ thống** (super_admin; middleware `super_admin`)
19. Chỉ các key trong DEFINITIONS được sửa, kèm khoảng (value ngoài khoảng, hoặc int mà không nguyên → 422 `settings.{i}.value`). `value:null` = về mặc định (xóa dòng). Giá trị không đổi thì bỏ qua (không audit). Mỗi thay đổi: audit `setting.changed` `changes.value{from,to}` meta `{key}`.

| key | kiểu | min | max |
|---|---|---|---|
| uploads.room_media.max_files | int | 1 | 50 |
| uploads.room_media.max_size_kb | int | 100 | 10240 |
| uploads.room_media.max_dimension | int | 512 | 8000 |
| uploads.payment_proof.max_files | int | 1 | 10 |
| uploads.payment_proof.max_size_kb | int | 100 | 10240 |
| uploads.payment_proof.max_dimension | int | 512 | 8000 |
| uploads.client_quality | float | 0.5 | 0.95 |
| uploads.avatar.max_size_kb | int | 100 | 4096 |
| uploads.avatar.max_dimension | int | 128 | 2048 |
| invoices.due_days | int | 1 | 90 |
| invoices.reminder_days | int | 1 | 30 |

20. `version` = hash 12 ký tự của toàn bộ overrides; ghi xong thì xóa cache và áp lại ngay; response API mang header `X-Config-Version` (+ `/config` ETag) để web tải lại cấu hình. Mặc định (default) lấy từ config env lúc khởi động.
21. `GET /admin/settings` trả từng key `{key,group,type,min,max,default,value,overridden}` + `version`; `GET /admin/settings/history` phân trang (từ audit `setting.changed`: key, old/new, changed_by, created_at). Thay đổi cài đặt chỉ super_admin xem được trong audit.
**Điều khoản pháp lý**
22. `POST /admin/legal/{terms|privacy}` (super_admin; kind khác → 404): body `content.{vi,en}.{title ≤200, body}` + `change_summary.{vi,en}` (mỗi bên bắt buộc, ≤2000, không key khác). Tạo dòng MỚI (version = `Y-m-d.His` giờ lúc đăng), audit `legal.published` meta {kind,version}; xóa cache `legal.in_force`. Response 201 `{version}`.
23. Phiên bản cần đồng ý (`currentVersion`) = `version` của dòng mới nhất TRONG MỌI loại (cả terms lẫn privacy); chưa đăng gì thì dùng config `rental.legal.terms_version`. Mỗi lần đăng = mọi người (trừ staff) bị hỏi lại; không có "sửa nhỏ".
24. `needs_acceptance = user.terms_version != currentVersion` VÀ user không có role admin/super_admin. Staff không bị hỏi. Chỉ trả cho chính tài khoản đó.
25. `POST /profile/accept-terms`: ghi `terms_version=currentVersion`, `terms_accepted_at=now`, audit `terms.accepted` meta version. Đăng ký bắt buộc `accept_terms` (Go đã có `TermsVersion`).
26. `GET /legal/{kind}` công khai: `{kind,version,published_at,change_summary,content}` của bản mới nhất, `data:null` nếu chưa đăng. `GET /profile/legal-updates?per_page(≤20, mặc định 5)`: các bản đăng SAU `terms_accepted_at` của user (mới nhất trước); user chưa từng đồng ý → bản mới nhất của mỗi kind. Admin: `GET /admin/legal/{kind}` (current_version + content), `…/versions` phân trang (≤50, mặc định 10, kèm publishedBy name), `…/{documentId}` (404 nếu id thuộc kind khác).
27. `RichText` (danh sách trắng, `body` phải là TipTap doc): root `{type:doc, content:[]}`; node cho phép: `doc>[heading,paragraph,bulletList,orderedList]`, `bulletList|orderedList>[listItem]`, `listItem>[paragraph,bulletList,orderedList]`, `heading|paragraph>[text]`; node chỉ có key ∈ {type,content,attrs,text,marks}; `heading.attrs.level ∈ {2,3}`; PHẢI có ≥1 heading level 2 (`no_heading`) và ≥1 ký tự chữ sau trim (`empty`); `text` không rỗng ≤6000 ký tự, không kèm content/attrs; marks ≤4 và ∈ {bold,italic,underline,link}; link phải `^(https?://|mailto:)\S+$` (không phân biệt hoa thường) ≤2000 ký tự (`link`); tối đa 5000 node, sâu ≤6 (`too_large`). Mã lỗi: structure,node_type,mark_type,heading_level,link,too_large,no_heading,empty. — `Domain/Legal/RichText`, `tests/Unit/Legal/RichTextTest`.
**Quyền chủ thể dữ liệu**
28. `GET /profile/export` (throttle 5/giờ, tên `profile-export`): JSON tải về gồm account (id,name,email,phone,address,ward,province,status,roles,created_at,terms*,accepts_invitations), landlord_profile, rooms (id,name,address,price,status), contracts_as_renter (room,status,price,starts/ends), payments của mình, reports của mình, activity (500 dòng gần nhất mỗi tầng audit: action,actor_role,ip,user_agent,created_at). KHÔNG có ảnh chứng từ, KHÔNG có dữ liệu người khác. Audit `personal_data.exported`.
29. `GET /profile/erasure-check` → `{blockers:[...]}`: chặn nếu (a) là người thuê của hợp đồng `active|pending`; (b) là chủ phòng có hợp đồng `active|pending`.
30. `DELETE /profile {password, confirm accepted}` (throttle 5/giờ `profile-erase`): staff (admin/super_admin) → 422 `account`; sai mật khẩu → 422 `password`; có blocker → 422 `account` (mảng). Hợp lệ (một transaction): audit `user.erased` (trước); hủy lời mời thuê/quản lý đang mở của họ (status → revoked, answered_at=now: `pending|awaiting_approval` cho lời mời thuê, `pending` cho lời mời quản lý); gỡ `room_managers` active (revoked); hủy subscription active/pending (canceled, ends_at=now); XÓA cứng `oauth_accounts`, `landlord_payment_methods`, `landlord_applications`, `landlord_profiles`, `notifications`, `role_user`; thu hồi mọi phiên/refresh token; trong `audit_logs` và `audit_activity` đặt `ip, user_agent, session_id = NULL` cho `actor_id` = họ (ngoại lệ duy nhất của "audit chỉ thêm"); user: name = "Người dùng đã xóa", email/phone/address/ward/avatar = null, password = random 64, status inactive, `accepts_invitations=false`, `erased_at=now`, rồi xóa mềm. Giữ hợp đồng/hóa đơn/thanh toán (chỉ mất tên).
31. `PUT /profile/preferences {accepts_invitations}` audit `invitations.preference_changed`.

### Endpoint (gọn)
| method path | quyền | throttle |
|---|---|---|
| GET /admin/users | users.view | — |
| DELETE /admin/users/{id}; POST /admin/users/bulk-delete | users.delete | — |
| PUT /admin/users/{id}/roles | users.update.roles (Go: `users.assign_roles`) | — |
| GET /admin/roles | users.update.roles | — |
| GET|POST /admin/landlord-applications…, bulk, approve, reject, GET users/{id}/landlord-impact, POST users/{id}/revoke-landlord | landlord_applications.review | — |
| GET|POST|DELETE /my/landlord-application | đăng nhập (tenant) | POST 10/giờ |
| GET|PUT /landlord/profile | landlord | — |
| GET|PUT /admin/settings, GET /admin/settings/history | super_admin (middleware) | — |
| GET|POST /admin/legal/{kind}, versions, {docId} | super_admin | — |
| GET /legal/{kind} | công khai | — |
| GET /profile/legal-updates, POST /profile/accept-terms, PUT /profile/preferences | đăng nhập | — |
| GET /profile/export, DELETE /profile | đăng nhập | 5/giờ mỗi cái |

### Quyền cần trong catalog
Go `catalog.go` đã có `landlord_applications.review`, `users.manage` (admin & super_admin), `roles.manage` (super_admin), `users.assign_roles`. Laravel dùng tên `users.update.roles` và seed `users.*` cho cả hai role; Go cần quyết cách map (xem Cần hỏi). Cần middleware `super_admin` riêng (settings, legal, plans) — hiện catalog Go không có permission riêng cho settings/legal: nên thêm `settings.manage`, `legal.manage` (chỉ super_admin) hoặc giữ kiểm tra role.

### Khác biệt khi sang Go
- Bỏ xác nhận email của đơn chủ trọ (đã bỏ ở Laravel). Bỏ cache Redis của quyền (Go tính theo token/role).
- `legal_documents.content` lưu jsonb; `RichText` là domain thuần (struct/validator, không tag).
- Xóa người dùng: Laravel chỉ soft delete; Go cần thêm thu hồi session khi bị xóa (Laravel không làm — Cần hỏi).
- `pending_registrations` trong impact dựa `confirmed_at` (đã bỏ xác nhận email) — Go đếm theo `status=pending` của phòng chủ trọ.

### Cần hỏi
1. `canManage(admin, newRoles=[])`: admin gửi `roles: []` cho một tenant → được phép (target null-role ∈ MANAGEABLE) nên admin có thể gỡ sạch role của tenant/landlord/manager (landlord thì kích hoạt revoke). Cố ý hay sơ hở? Go nên cấm roles rỗng?
2. `UserService::delete` không thu hồi token/phiên của người bị xóa (chỉ soft delete); đăng nhập sau đó bị chặn nhờ query bỏ qua deleted? Cần xác nhận hành vi Go (nên thu hồi phiên).
3. Tên quyền: Laravel `users.update.roles` với `users.*`; Go catalog có `users.assign_roles` NHƯNG `users.manage` ("mọi hành động trên users") có bao trùm `users.assign_roles` không (khác prefix kiểu `x.y.z`)? Cần quyết `authz.HasPermission`.
4. `assertCanLoseLandlordRole` chỉ chặn hợp đồng `active`, không chặn `pending` hay hóa đơn chưa thu; impact báo nhưng không chặn. Giữ nguyên?
5. Admin không thấy chính mình/admin khác trong danh sách nhưng route xóa/roles vẫn dùng policy theo id (404 vs 403): Laravel trả 403 khi biết id — chấp nhận lộ tồn tại?
6. `bulk` đơn chủ trọ không kiểm quyền hơn `permission:landlord_applications.review`; admin nộp đơn thay cho chính mình? (Admin không có role tenant nên bị chặn ở luật 10.) OK?

---

## 2. audit (nhật ký truy vết)

### Bảng/field
Hai bảng CÙNG cấu trúc, chỉ thêm, KHÔNG khóa ngoại (nhật ký phải sống lâu hơn dữ liệu nó nhắc tới): `audit_logs` (tầng `important`) và `audit_activity` (tầng `routine`).
`id bigserial`, `created_at timestamptz default now()`, `actor_id uuid?`, `actor_role varchar(20)?` (`system` cho job/lệnh), `acting_for_id uuid?` (chủ trọ sở hữu dữ liệu khi người khác thao tác), `action varchar(60)`, các khóa tham chiếu `room_id bigint?` (Go: UUID — xem Cần hỏi), `contract_id`, `invoice_id`, `payment_id`, `target_user_id` (uuid?), `changes jsonb?` (`field → {from,to}`), `meta jsonb?`, `ip varchar(45)?`, `user_agent varchar(120)?` (cắt 120), `session_id uuid?` (= family của refresh token / claim `sid`), `request_id uuid?`.
PK `(id, created_at)`; **partition theo tháng** `PARTITION BY RANGE (created_at)` + partition `_default`; partial index `(col, created_at) WHERE col IS NOT NULL` cho từng khóa tham chiếu và `acting_for_id`, index `(action, created_at)`, index `(created_at DESC, id DESC)`.

### Luật
1. Tầng: `Routine` = `auth.login`, `auth.login_failed`, `auth.logout`, `invitation.lookup`; mọi action khác = `Important`. Retention: routine `AUDIT_ROUTINE_RETENTION_DAYS`=90 ngày; important `AUDIT_IMPORTANT_RETENTION_MONTHS`=24 tháng. — `AuditAction::tier`, `config/audit.php`.
2. Ghi: `record(action, references, changes, meta, actor?)` chạy TRONG cùng transaction với thao tác; lỗi ghi tầng important làm hỏng thao tác; lỗi ghi tầng routine bị nuốt (log) — không bao giờ làm hỏng request. Ghi hàng loạt = MỘT câu insert nhiều dòng (`recordMany`, mỗi dòng một đối tượng).
3. Actor: lấy từ tham số nếu có, không thì từ context request (`actor_id`, role đang dùng, session). `actor_role`: role đang dùng của actor; không có actor + không có request (console/queue) → `system`. `acting_for_id` = `references.owner_id` nếu khác `actor_id`, ngược lại null. Chỉ các khóa `room_id|contract_id|invoice_id|payment_id|target_user_id` được lưu thành cột; khóa khác bị bỏ.
4. Làm sạch payload (`changes`,`meta`) ở mọi độ sâu, key so không phân biệt hoa thường: key ∈ `password, password_confirmation, token, access_token, refresh_token, secret, authorization` → `"[redacted]"`; key ∈ `account_number, bank_account` → che còn 4 ký tự cuối `"****"+last4` (áp cho cả scalar, cả mảng và cặp `{from,to}`); payload JSON > 8192 byte → thay bằng `{"truncated":true,"bytes":N}`; payload rỗng → null.
5. Hồ sơ cá nhân/landlord chỉ ghi TÊN trường đã đổi (`meta.fields`), không ghi giá trị. `modelChanges` chỉ gồm trường thực sự đổi (loại `updated_at`).
6. Đăng nhập: `auth.login` (routine) meta `{via: password|google|zalo…}`, có `session_id`; `auth.login_failed` (routine) meta `{reason:'bad_credentials'..., email_hash: sha256(lowercase email)}`, KHÔNG lưu email gõ vào, `target_user_id` = user nếu tìm ra. `auth.role_switched` (important) meta `{from,to}`, dòng thuộc session MỚI. Không ghi mỗi lần refresh token; chỉ ghi `auth.token_reuse` khi phát hiện dùng lại.
7. Không ghi thao tác chỉ đọc. Xem ảnh chứng từ thanh toán chưa truy vết được (link có chữ ký).
8. Ba cách đọc, tất cả phân trang kiểu `has_more` (không đếm tổng): mỗi nhánh tầng chỉ lấy `page*per_page+1` dòng gần nhất trước khi gộp (UNION ALL) rồi sort `created_at DESC, id DESC`; trả `{data, has_more}`.
   a. **Admin** `GET /admin/audit-logs` (quyền `audit.view`; per_page ≤100 mặc định 50): lọc `actor_id, actor_role(≤20), action(∈ AuditAction), target_user_id, room_id, contract_id, invoice_id, from, to (≥from), tier`. Admin (không phải super_admin): (i) loại action `setting.changed`; (ii) loại mọi dòng mà `actor_role ∈ {admin,super_admin}` HOẶC `actor_id` hoặc `target_user_id` là user đang có role admin/super_admin ("không thấy việc của/với người cùng cấp hoặc cao hơn"); (iii) che tiền (luật 9) — LUÔN che cho cả super_admin (`includeMoney:false`). Super_admin thấy cả staff và `setting.changed`.
   b. **Của tôi** `GET /my/activity` (per_page ≤100 mặc định 20): dòng có `actor_id = me` HOẶC `target_user_id = me`, cả hai tầng. Với dòng do NGƯỜI KHÁC làm lên tài khoản tôi: `actor` chỉ còn `{id:null,name:null,role}`, `changes/meta/ip/device = null`, `acting_for/target_user` null. Dòng liên tiếp cùng action+tầng+actor_role+actor_id+cùng giây (cắt 19 ký tự) gộp thành một, `count++` (thao tác hàng loạt hiện một lần).
   c. **Theo đối tượng** `GET /rooms|contracts|invoices/{id}/history`: chỉ tầng important, lọc theo cột `room_id|contract_id|invoice_id`; quyền = policy `update` của đối tượng (chủ sở hữu; quản lý chỉ nếu policy cho). Lịch sử phòng gồm cả hợp đồng/hóa đơn của phòng (vì các dòng đó cũng mang `room_id`).
9. Che tiền (`MoneyRedaction`): với action KHÔNG bắt đầu bằng `plan.` hoặc `subscription.`, trong `changes` và `meta` thay giá trị dưới các key (so không phân biệt hoa thường, mọi độ sâu) `amount, total_amount, price, unit_price, rent_unit_price, deposit_amount, paid, pending, remaining` bằng `"•••"`; nếu giá trị là cặp `{from,to}` giữ dạng, mỗi vế `"•••"` (null giữ null); giá trị null giữ null. Thêm hành động ghi tiền dưới khóa mới ⇒ thêm khóa vào `money_keys` (test `AuditMoneyHiddenTest` chạy các luồng tiền thật).
10. Bảo trì (lệnh `audit:maintain`, hằng ngày 03:00, Go: thêm vào `worker`): tạo trước `AUDIT_PARTITIONS_AHEAD`=3 tháng (tháng hiện tại + 3) cho cả 2 bảng `CREATE TABLE IF NOT EXISTS {table}_{YYYY}_{MM} PARTITION OF … FOR VALUES FROM (đầu tháng) TO (đầu tháng sau)`; xóa partition mà `đầu_tháng + 1 tháng <= cutoff` (cutoff = now − retention) bằng `DROP TABLE`. Không xóa từng dòng.
11. Ngoại lệ "chỉ thêm": xóa tài khoản (mục 1, luật 30) NULL `ip,user_agent,session_id` của dòng có `actor_id` = người đó trong cả hai bảng.

### Danh sách AuditAction (giá trị chuỗi; * = routine, còn lại important)
auth: `auth.register`, `auth.email_confirmed`, `auth.login`*, `auth.login_failed`*, `auth.logout`*, `auth.token_reuse`, `auth.role_switched`, `auth.oauth_linked`, `auth.oauth_unlinked`, `auth.password_reset_requested`, `auth.password_reset`.
room: `room.created|updated|deleted|reordered|service_changed`. building: `building.created|updated|deleted|reordered|rooms_created`.
contract: `contract.registered|created|updated|deleted|terminated|cancellation_requested|cancellation_withdrawn|cancellation_approved|cancellation_declined`; `contract_invitation.created|accepted|declined|revoked|approved|rejected`.
invoice: `invoice.created|sent|updated|deleted|status_changed|link_created|link_revoked`; payment: `payment.submitted|confirmed|rejected|withdrawn|recorded`, `payment_method.saved|removed`.
plan: `subscription.requested|assigned|activated|canceled|expired`, `plan.saved`.
message/report: `announcement.sent|updated|unpublished|recalled`, `report.created|replied|status_changed|deleted`.
user: `user.deleted|erased|data_exported|terms_accepted|invitations_preference_changed|profile_updated|avatar_changed|roles_changed`, `invitation.opted_out`, `landlord_application.submitted|withdrawn|approved|rejected`, `landlord.revoked|profile_updated`.
manager/lời mời: `invitation.lookup`* (meta CHỈ `{by, found}`, không lưu chuỗi đã gõ), `manager.invited|invitation_revoked|accepted|declined|revoked|updated`.
listing: `listing.created|updated|submitted|approved|rejected|unpublished|taken_down|deleted`. legal: `legal.published`. system: `setting.changed`.
(Go đã có thêm hành động riêng cho tính năng đã làm; chỉ cần đảm bảo mọi action đăng ký trong enum + có nhãn i18n `audit.actions.<khu_vực>.<tên>`.)

### Endpoint
| method path | quyền | ghi chú |
|---|---|---|
| GET /admin/audit-logs | `audit.view` (admin, super_admin) | luật 8a |
| GET /my/activity | đăng nhập | luật 8b |
| GET /rooms/{id}/history, /contracts/{id}/history, /invoices/{id}/history | policy update của đối tượng | luật 8c |

### Quyền: `audit.view` đã có trong catalog Go. Không cần thêm.

### Khác biệt khi sang Go
- Không morph, không FK (đã vậy). Dùng `pgx.CopyFrom`/multi-row insert cho bulk. Thay `AuditContext` (scoped) bằng value trong `context.Context` (actor id, role, session id, request id, ip, UA) do middleware gắn; ports `audit.Recorder` ở usecase.
- Đổi `room_id bigint` → UUID nếu Go dùng UUID cho phòng (Go dùng UUID v7).
- Lịch sử cài đặt không còn bảng riêng: đọc từ audit `setting.changed`.
- `request_id`: Laravel lấy từ middleware request-id; Go dùng id sẵn có của logger.

### Cần hỏi
1. `session_id` của dòng audit khi refresh token xoay: Laravel dùng "family" làm session; Go có `user_sessions` — dùng `user_sessions.id`?
2. Quyền xem lịch sử phòng/hợp đồng/hóa đơn của quản lý hộ (manager) — hiện chỉ theo policy `update`; manager được `update` hóa đơn? Chưa kiểm test cụ thể.
3. Admin lọc staff theo `actor_role`/`actor_id` nhưng không lọc `acting_for_id`: dòng quản lý thao tác thay một admin (không xảy ra vì staff không trộn) — bỏ qua OK?
4. `invitation.lookup` ghi 90 ngày nhưng không có route admin lọc riêng; giữ chung `tier` filter?

---

## 3. manager (giao phòng cho quản lý hộ, lời mời quản lý, quyền theo phòng, lời mời thuê do quản lý soạn)

Go đã có: `internal/domain/invitation` (Audience/Allows/Invitable/Addressee/MaskEmail), bảng `contract_invitations` (có sẵn `awaiting_approval`, `created_by_id`), `invitation_email_blocks`, role `manager` + quyền `managers.manage`, `manager_invitations.respond`. Chỉ ghi phần còn thiếu.

### Bảng/field cần thêm
- `room_managers`: `id uuid`, `landlord_id`, `room_id`, `manager_id`, `status` active|revoked (CHECK), `notify_landlord bool default true`, `assigned_by_id` (= landlord), `accepted_at`, `revoked_at?`, timestamps. **Unique từng phần `(room_id) WHERE status='active'`** (mỗi phòng một người quản lý đang hoạt động). Index `(manager_id, status)`, `(landlord_id, status, created_at)`.
- `manager_invitations`: `id`, `landlord_id`, `token_hash` (64, unique), `link_token_sealed?` (chỉ khi pending, `pkg/secretbox`), `status` pending|accepted|declined|revoked (Laravel dùng chung enum nên cho cả awaiting_approval nhưng KHÔNG dùng cho loại này), `expires_at` (= now + `INVITATION_TTL_DAYS`=7), `notify_landlord bool default true`, `recipient_id?`, `recipient_email?` (lowercase), `accepted_by_id?` (người trả lời, cả khi decline), `answered_at?`, timestamps. CHECK như `contract_invitations`.
- `manager_invitation_rooms (invitation_id, room_id)` PK kép.
- Capability: bảng nối `room_manager_capabilities (room_manager_id, capability text)` và `manager_invitation_capabilities (invitation_id, capability text)` — KHÔNG cần bảng `permissions` như Laravel (xem khác biệt). `capability` ∈ 4 mã dưới (CHECK).
- `contract_invitations` đã có `created_by_id`; cần thêm `approved_by_id?`, `approved_at?`, `approval_note?` (≤1000).
- Notification kinds mới: `manager_invitation_received`, `manager_invitation_withdrawn`, `manager_accepted`, `manager_declined`, `manager_revoked`, `manager_bill_sent`, `manager_bill_edited`, `contract_invitation_needs_approval`, `contract_invitation_approved`, `contract_invitation_rejected`.

### Danh mục capability (domain thuần; mã KHÔNG bao giờ đổi nghĩa/dùng lại; mã lạ trong DB bị bỏ qua)
| code | nhóm | rủi ro | requires |
|---|---|---|---|
| `payments.review` | payments | medium | — |
| `contracts.review_registrations` | contracts | medium | — |
| `contracts.propose` | contracts | low | — |
| `contracts.create` | contracts | high | `contracts.propose` |
`change(current, grant, revoke)`: tập = (current ∪ grant) kèm các `requires` của từng cái; trừ `revoke`; rồi lặp loại bất kỳ cái nào mất thứ nó cần (chuỗi); kết quả sort. `set(codes)` = `change([], codes, [])`. `GET /config` trả `app.manager_capabilities = catalogue [{code,group,risk,requires}]`. — `ManagerCapability`, `ManagerCapabilityTest`.
Phần cơ bản (không cần capability): xem phòng/hợp đồng đã xác nhận của phòng được giao, nhập chỉ số, xem trước/tạo/sửa/gửi hóa đơn, xóa nháp do mình tạo.

### Luật
**Điều kiện làm quản lý** (`ManagerEligibility::canBeAsked(exists, isLandlord, isActive, roles)`): tài khoản tồn tại, KHÔNG phải chủ trọ của lời mời, `status=active`, có role `tenant`, không có role admin/super_admin. Kiểm khi **chấp nhận** → vi phạm = 422 `token` (cannot_manage). (Go `invitation.Invitable` dùng khi tra cứu người nhận.)
**Tạo lời mời** `POST /my/manager-invitations {room_ids[1..100 int distinct], replace_room_ids?[⊂room_ids], capabilities?[], notify_landlord? (mặc định true), recipient_id? uuid, email?}`:
1. Phân giải người nhận bằng `InvitationDelivery.resolve` (dùng usecase invitation hiện có: công khai / đích danh theo tài khoản / theo email; chủ trọ không mời chính mình, staff không nhận, người tắt `accepts_invitations` hoặc email bị chặn → xử như hợp đồng).
2. Mọi phòng phải thuộc chủ trọ, không thì 422 `room_ids` (room_not_owned) — phòng người khác và phòng không tồn tại cùng một lỗi.
3. Trong transaction, khóa dòng: phòng đã có quản lý `active` → 422 `room_ids` kèm TÊN phòng (tối đa 5 tên rồi " …"); phòng đang nằm trong lời mời `pending` chưa hết hạn: nếu không nằm trong `replace_room_ids` → 422 `room_ids` (rooms_pending, tên phòng); nếu có → RÚT phòng khỏi lời mời cũ (detach); lời mời cũ hết phòng → `revoked` + `answered_at`; audit `manager.invitation_revoked` mỗi phòng meta `{invitation_id, replaced:true}`.
4. Tạo lời mời `pending`, token 64 ký tự (chỉ hiện MỘT lần ở response `link`; DB lưu hash + bản mã hóa để sao chép lại), gắn phòng và capabilities (sau `change` — tự thêm requires). Audit `manager.invited` mỗi phòng meta `{invitation_id, capabilities, notify_landlord, replaced_invitation_ids}`.
5. Giao: tài khoản đích danh → notification `manager_invitation_received` url `…?invitation={id}` (KHÔNG có token) + email tới địa chỉ tài khoản; email chưa có tài khoản → email kèm link có token; công khai → không gửi gì. Response trả `delivery {app,email}`. Người nhận của lời mời CŨ bị rút phòng nhận `manager_invitation_withdrawn {landlord,count}` url `/my-rooms`.
6. Throttle 30/giờ (`manager-invitations-store`); link của chủ trọ `GET …/{id}/link` 60/phút, chỉ khi còn mở (pending & chưa hết hạn) và lời mời có `link_token`; ngược lại 404.
**Quản lý lời mời (chỉ chủ trọ tạo ra, người khác 404)**: `GET /my/manager-invitations?status=` phân trang (≤50, mặc định 10) — thứ tự `awaiting_approval`, `pending`, rồi còn lại, mới nhất trước; `POST …/{id}/regenerate` (chỉ `pending` — không thì 422 `invitation` not_pending; token mới, link cũ chết, gia hạn expires_at); `DELETE …/{id}` (chỉ `pending`; → `revoked`, audit `manager.invitation_revoked` mỗi phòng).
**Trả lời** `POST /manager-invitations/preview|accept|decline {token | invitation_id}` (quyền `manager_invitations.respond`, tức người đang ở chế độ tenant; throttle `invitations` 20/phút):
7. Tìm lời mời theo `token_hash`, hoặc theo `invitation_id` + `recipient_id = me`. Phải `pending`, `expires_at > now`, và `Allows(recipient_id, recipient_email, me, myEmail, emailVerified)`. Sai/hết hạn/đã trả lời/đã thu hồi/không dành cho mình → CÙNG MỘT lỗi 422 `token` (invalid) để link không lộ gì.
8. Accept (transaction, khóa lời mời): kiểm eligibility (luật trên); rồi `startFromInvitation`: lấy phòng còn thuộc chủ trọ & chưa xóa; nếu số phòng còn ≠ số phòng trong lời mời (kể cả đã xóa mềm) HOẶC có phòng đã có quản lý `active` → 422 `token` (rooms_changed) — **không giao nửa chừng**. Tạo một `room_managers` `active` mỗi phòng (`notify_landlord` và capabilities sao chép từ lời mời, `assigned_by_id`=landlord, `accepted_at=now`), audit `manager.accepted` mỗi phòng (actor = người nhận, `owner_id`=landlord, `target_user_id`=manager); gắn role `manager` cho tài khoản (nếu chưa); lời mời → `accepted` (`accepted_by_id`, `answered_at`). Chủ trọ nhận MỘT thông báo gộp `manager_accepted {manager,count,rooms(≤5 tên + " …")}` url `/managers`.
9. Decline: lời mời → `declined` (`accepted_by_id` = người trả lời); audit `manager.declined` mỗi phòng; chủ trọ nhận `manager_declined`.
**Thu hồi/đổi quyền** (chỉ chủ trọ; người quản lý KHÔNG tự trả phòng):
10. `DELETE /my/room-managers/{id}` hoặc `POST /my/room-managers/bulk {action:revoke, ids[1..200 uuid]}`: chỉ dòng `active` của chủ trọ (id lạ/đã thu hồi bị bỏ qua; đơn lẻ → 404); đặt `revoked`+`revoked_at`; audit `manager.revoked` mỗi phòng (một insert); với mỗi quản lý: nếu KHÔNG còn `room_managers active` nào → gỡ role `manager`, và nếu `last_active_role=manager` → null; mỗi quản lý nhận MỘT `manager_revoked {landlord,count,rooms}` url `/profile`. Bulk trả `{done:[ids], failed:[]}`.
11. `PUT /my/room-managers/{id}` `{grant?[], revoke?[], notify_landlord?}` và bulk `{action:update, ids, grant?, revoke?, notify_landlord?}`: áp `change` (tự thêm requires; revoke `propose` kéo theo bỏ `create`), audit `manager.updated` với `changes` (`notify_landlord {from,to}`, `capabilities {from:[codes],to:[codes]}`) meta `{assignment_id}`. Mã ngoài catalog → 422.
**Quyền theo phòng** (policy, KHÔNG theo role): hiệu lực chỉ khi tài khoản đang ở chế độ `manager` (chế độ tenant thì mọi hand-over vô nghĩa — không rò quyền). `managementOf(roomId)` = dòng `active` của phòng đó.
12. Phòng: chủ HOẶC quản lý → xem; sửa/xóa phòng chỉ chủ. Hợp đồng: quản lý chỉ thấy hợp đồng ĐÃ xác nhận của phòng được giao (Go: không còn `confirmed_at` → thấy hợp đồng không phải pending-chưa-duyệt? Cần hỏi 3.1) và KHÔNG phải hợp đồng của chính mình làm người thuê (`tenant_id = me` → bị loại khỏi list và 403). Quản lý không sửa/xóa/terminate hợp đồng, không duyệt hủy.
13. Hóa đơn `mayWork` = chủ phòng HOẶC (quản lý của phòng VÀ hợp đồng không phải của chính mình) — dùng cho view/send/update/create; **xung đột lợi ích không ngoại lệ**: manager không bao giờ tạo/sửa/gửi/xác nhận hóa đơn hay thanh toán của hợp đồng mình là người thuê. `update` còn đòi hóa đơn chưa `paid` và không có payment `pending|confirmed`. `delete`: phải qua `update`, rồi chủ trọ hoặc (hóa đơn `draft` VÀ `created_by_id = me`). Đánh dấu trạng thái/ghi nhận đã thu (`updateStatus`, `recordPayment`): chủ, hoặc quản lý có `payments.review`; `recordPayment` thêm điều kiện hóa đơn `sent`. Xác nhận/từ chối payment (`PaymentPolicy.review`): chủ, hoặc quản lý có `payments.review` VÀ `payer_id ≠ me`; xem payment: người trả, chủ, hoặc quản lý của phòng (không phải payer là mình); rút payment: người trả + còn `pending`.
14. Duyệt đăng ký thuê (`reviewRegistration`): hợp đồng `pending`; chủ, hoặc quản lý của phòng (không phải hợp đồng của mình) có `contracts.review_registrations`; dùng qua `POST /contracts/bulk {approve|reject}` và duyệt đơn lẻ; quản lý có quyền này cũng nhận thông báo `registration_confirmed` khi có đăng ký mới. `ContractResource.abilities {review,update,delete}` theo người xem.
15. Gửi hóa đơn (`sendInvoice` trên hợp đồng): chủ hoặc quản lý của phòng, và hợp đồng `active`.
16. Quản lý KHÔNG: sửa phòng/hợp đồng, phương thức thanh toán (`payment_methods.manage` chỉ chủ), thông báo/announcement, gói, phòng khác. `GET /my/payment-methods` với manager → 403.
17. Truy vết: mọi thao tác của quản lý ghi `actor_role=manager`, `acting_for_id`=chủ trọ (nhờ `owner_id`). Hóa đơn có `created_by_id` (+ `sent_by_id`).
18. Khi quản lý (chế độ manager) GỬI hoặc SỬA hóa đơn: nếu dòng `room_managers active` của (phòng, người đó) có `notify_landlord=true` → chủ trọ nhận `manager_bill_sent|manager_bill_edited {manager, room, number}` url `/contracts/{id}`. Chủ trọ tự làm thì không báo.
**Lời mời thuê do quản lý soạn**:
19. Quyền route `contracts.create` (role manager có) nhưng người soạn phải là chủ phòng HOẶC có `management` đang hoạt động của phòng đó với `contracts.propose`; ngược lại 422 `room_id` (room_not_owned, cùng lỗi với phòng không tồn tại). `ContractPolicy.create` chỉ cho manager có `propose` ở ≥1 phòng.
20. Chủ trọ soạn → `pending` ngay (như hiện tại). Quản lý có `propose` mà KHÔNG có `contracts.create` → `awaiting_approval`: `created_by_id` = quản lý, `link_token` null, KHÔNG gửi gì cho người nhận (delivery all false); chủ trọ nhận `contract_invitation_needs_approval {manager,room,invitation_id}` url `/contracts?invitation={id}` + email (`contract_approval`, chỉ khi có email). Quản lý có `contracts.create` → gửi ngay như chủ trọ (nhưng `created_by_id` vẫn là quản lý).
21. `POST /contract-invitations/{id}/approve {note?}` (quyền `contracts.update`; chỉ chủ phòng — người khác 404): trong transaction khóa; không còn `awaiting_approval` → 422 `invitation` (not_awaiting); nếu `contract_status=active` mà phòng đã có hợp đồng đang chạy (có người thuê) → 422 `room_id` (room_has_active); → `pending`, token MỚI, `expires_at` mới, `approved_by_id/at/note`; audit `contract_invitation.approved`; rồi giao như luật 5 (tài khoản/email); quản lý được báo `contract_invitation_approved {room, sent}` url `/contracts` (lời mời công khai: quản lý bấm "liên kết mới" để lấy link).
22. `POST …/{id}/reject {note BẮT BUỘC ≤1000}`: → `revoked` + `answered_at`, `approved_by_id`, `approval_note`; audit `…rejected` meta reason; quản lý nhận `contract_invitation_rejected {room, reason}`.
23. Danh sách `GET /contract-invitations`: người dùng ở chế độ manager chỉ thấy lời mời `created_by_id = me` (và chỉ khi còn quản lý phòng), chủ trọ thấy tất cả của mình kèm "soạn bởi"; thứ tự `awaiting_approval`→`pending`→còn lại, mới nhất trước. Quản lý chỉ làm mới (regenerate)/thu hồi lời mời của mình. Người soạn KHÔNG thể tự chấp nhận lời mời của chính mình (`created_by_id = me` hoặc `landlord_id = me` → từ chối khi accept).
24. Đăng ký thuê (`contracts/register`) của một người đang là quản lý chính phòng đó: vẫn theo luật 12–13 (không tự duyệt).

### Endpoint (gọn)
| method path | quyền | throttle |
|---|---|---|
| GET|POST /my/manager-invitations; GET …/{id}/link; POST …/{id}/regenerate; DELETE …/{id} | `managers.manage` | POST 30/giờ; link 60/phút |
| POST /manager-invitations/preview|accept|decline | `manager_invitations.respond` | `invitations` 20/phút |
| GET /my/room-managers[?view=groups][&manager_id=] ; PUT|DELETE /my/room-managers/{id}; POST /my/room-managers/bulk | `managers.manage` | — |
| GET /my/managed-rooms[?view=groups][&landlord_id=] | `rooms.view` (chế độ manager) | — |
| POST /contract-invitations/{id}/approve|reject | `contracts.update` (chỉ chủ phòng) | — |
`view=groups`: mỗi dòng một người `{person{id,name,email,avatar_thumbnail_url}, count, ids[], room_ids[], flags{notify_landlord: all|some|none}, capabilities{code: all|some|none}}`, phân trang theo người (mới giao nhất trước), kèm `busy_room_ids` (mọi phòng đang giao); phòng của một người lấy bằng `?manager_id=` (phân trang riêng). Phía quản lý nhóm theo chủ trọ (tên = `business_name` hoặc tên), không có flag. Mặc định `per_page` 10, tối đa 50.

### Quyền cần trong catalog
Go đã có `managers.manage`, `manager_invitations.respond`; role `manager` đã có `rooms.view, contracts.view, contracts.create, invoices.view|create|update|delete`. Không thêm quyền role mới: capability là DỮ LIỆU theo phòng (không phải permission). `contracts.update` của manager: KHÔNG có (chỉ chủ trọ approve/reject).

### Khác biệt khi sang Go
- Bỏ bảng `permissions` kiểu `scope=room_manager` + `manager.` prefix: dùng cột text `capability` + CHECK; domain `manager.Capability` thuần.
- Không morph, không `Notifiable`. `InvitationStatus` chia riêng cho từng loại (manager không có `awaiting_approval`).
- Gỡ/gắn role `manager` làm trong cùng transaction với `room_managers` (ports `RoleGranter`), và nhớ vô hiệu quyền cache nếu có.
- Hóa đơn/hợp đồng đã làm ở Go cần "móc" manager: `Authorizer` theo phòng nhận `ManagementOf(roomID)` + capability; chỉ hiệu lực khi active role = manager.
- `contracts.confirmed_at` đã bỏ trong Go → bỏ điều kiện `isConfirmed()` ở policy (xem Cần hỏi).

### Cần hỏi
1. Policy Laravel `managesVisibleRoom` yêu cầu hợp đồng `isConfirmed()` (đã xác nhận email, nay luôn true sau khi tạo). Go không có cột: quản lý thấy MỌI hợp đồng của phòng (cả `pending`) hay chỉ không-pending? Laravel CLAUDE.md nói "hợp đồng đã xác nhận" (= mọi hợp đồng hiện nay).
2. `ContractPolicy::create` có `Admin` trong `hasAnyRole([Landlord, Admin])` nhưng admin không có quyền `contracts.create` (route chặn) — code chết, nên bỏ?
3. Nghi bug: `RoomManagerController::index` không giới hạn `per_page` tối thiểu; `update` đơn lẻ không kiểm `grant` mã rỗng. Go dùng `ParsePage` chuẩn.
4. `invoices.delete`: quản lý xóa nháp do MÌNH tạo kể cả khi nháp được chủ gửi sau? (policy `delete` yêu cầu `draft` nên khi đã gửi thì không).
5. Khi quản lý bị thu hồi hết phòng giữa lúc có lời mời thuê `awaiting_approval` do họ soạn: Laravel không tự hủy (danh sách của quản lý ẩn khi mất phòng). Go nên tự revoke?
6. `ManagerInvitation.status` Laravel dùng chung enum có `awaiting_approval` dù không dùng — Go nên CHECK chỉ 4 trạng thái.

---

## 4. stats (thống kê chủ trọ/quản lý, admin, system)

### Bảng/field cần: không bảng mới. Đọc từ `rooms`, `contracts`, `invoices`, `payments`, `meter_readings` + `room_services(type electricity|water)`, `users`, `user_roles`, `landlord_applications`, `reports`, `subscriptions`, `plans`, `announcements`+`notifications`, `audit_logs`/`audit_activity`. Go cần `meter_readings.unit_price`, `consumption`, `read_on` (đã có ở invoice context? kiểm tra) và `plans.price/billing_interval`.

### Luật
**Tiện ích thuần (domain `statistics`, unit test không DB)**
1. `Ratio.Percent(part, whole)`: `whole>0 ? round(part/whole*100, 1) : null` (KHÔNG trả 0 khi không có mẫu số). `Ratio.Change(cur, prev)`: `prev>0 ? round((cur-prev)/prev*100,1) : null`.
2. `MonthRange.Between(from,to)`: các tháng dương lịch `YYYY-MM` từ tháng của `from` đến tháng của `to`, tăng dần; `from` sau `to` → lỗi; > 36 tháng → lỗi. Request: lỗi này thành 422 trường `from`. `Bounds(month)` = ngày đầu và ngày cuối tháng.
3. `OccupancyCalculator.ByMonth(roomSpans, rentedSpans, months, today)`: mỗi tháng `last = min(cuối tháng, today)`; nếu `last < first` thì available=occupied=0; `available` = tổng số ngày (bao gồm cả hai đầu) các khoảng tồn tại của phòng giao với [first,last]; `occupied = min(available, ngày thuê giao với [first,last])`; `rate = Percent(occupied, available)`. Khoảng `[from, to?]`: `to` null = đến hết cửa sổ; cắt theo ngày (startOfDay). — `StatisticsRulesTest`.
**Phạm vi** (`StatisticsScope`)
4. Phòng của người xem: chủ trọ (đang ở chế độ landlord) = mọi phòng của mình **kể cả đã xóa mềm**, lọc thêm `building_id` nếu có; quản lý (chế độ manager) = các phòng `managedRooms()` đang `active` (lọc building tương tự, gồm cả phòng xóa mềm khi lọc); vai trò khác → 403. Chủ trọ khác không bao giờ lọt vào.
5. Phòng tồn tại từ `rooms.created_at` (startOfDay) tới `deleted_at` (nếu có). Thuê = hợp đồng `status IN (active, ended)` VÀ `confirmed_at NOT NULL` (Go: bỏ điều kiện confirmed_at nếu cột không còn), `starts_at <= to` và (`ends_at` null hoặc `>= from`); span `[starts_at, ends_at]`, hợp đồng `ended` không có `ends_at` coi như kết thúc **hôm nay**; `active` không `ends_at` = mở. Hợp đồng đã xóa mềm bị loại.
**Ba cách đọc tiền** (theo CODE, `PropertyStatistics`; khác mô tả trong CLAUDE.md — xem Cần hỏi 4.1)
6. `billed` (đã lập hóa đơn) = tổng `invoices.total_amount` của hóa đơn `status ∈ {draft, sent, paid}`, nhóm theo **tháng của `invoices.created_at`** (không phải `period_start`), chưa xóa mềm, hợp đồng chưa xóa mềm, thuộc phòng trong phạm vi, `created_at` trong [đầu tháng from, cuối tháng to]. (Test ghi rõ: hóa đơn nháp được tính "đã lập" và "còn nợ".)
7. `collected` (đã thu) = tổng `payments.amount` có `status='confirmed'` (KHÔNG tính `pending`/`rejected`), nhóm theo tháng của `payments.paid_at` trong khoảng; join qua hóa đơn/hợp đồng chưa xóa. Thanh toán ghi nhận tay (`payer_id` null) cũng tính.
8. `outstanding` (còn phải thu) theo tháng = tổng `(total_amount − Σ payments confirmed của hóa đơn đó)` trên hóa đơn `status ∈ {draft, sent}` theo `created_at` trong tháng; mỗi tháng `max(0, ·)`.
9. `owed` (tổng nợ hiện tại, không theo khoảng): trên mọi hóa đơn `draft|sent` của phòng trong phạm vi: `outstanding = Σ remaining`; `overdue_amount = Σ remaining` của hóa đơn có `due_at < now`; `overdue_count` = số hóa đơn có `due_at < now`. (Quá hạn = `due_at` đã qua, không cần status `sent`.) Làm tròn 2 số lẻ (tiền Go là bigint VND → không cần làm tròn).
**Số liệu từng endpoint**
10. `overview`: `rooms {total, rented, available, repairing}` (đếm `rooms.status` của phòng chưa xóa trong phạm vi; Go: đếm đúng các trạng thái phòng hiện có), `occupancy_rate = Percent(rented, total)` (theo trạng thái phòng hiện tại, KHÁC occupancy theo ngày), `active_contracts` (status active, confirmed), `pending_registrations` (status pending, confirmed), `expiring_soon` (active có `ends_at` ∈ [hôm nay, hôm nay+30 ngày]), `money {outstanding, overdue_amount, overdue_count, this_month{billed, collected, collection_rate}, previous_month{billed, collected}, billed_change, collected_change}`. `collection_rate = Percent(max(0, billed − outstanding của THÁNG ĐÓ), billed)` (phần hóa đơn tháng này đã trả; tiền thu cho hóa đơn cũ KHÔNG làm tăng tỉ lệ). `*_change = Ratio.Change(tháng này, tháng trước)` (tháng trước = tháng dương lịch trước tháng của today).
11. `revenue?from&to`: `months[{month, billed, collected, outstanding}]` đủ mọi tháng (tháng trống = 0), `totals` = tổng cộng ba cột. Mặc định khoảng = 12 tháng gần nhất (from = `to − 11 tháng` đầu tháng; to = hôm nay).
12. `occupancy`: `months[{month, occupied_days, available_days, rate}]` theo luật 3.
13. `rooms?sort=billed|collected|occupancy&limit(1..100, mặc định 50; dashboard 10)`: mỗi phòng (kể cả đã xóa) `{room_id, building_id, name, billed, collected, occupied_days, available_days, occupancy_rate}` trong CẢ khoảng; available/occupied tính tới `min(cuối khoảng, hôm nay)`; sắp giảm dần theo khóa (null xếp cuối = −1), cắt `limit`.
14. `buildings?limit`: gom `rooms` rows theo `building_id` (phòng không thuộc tòa nhà = một dòng `building_id:null, name:null`); mỗi dòng `{building_id,name,rooms (đếm phòng CÒN tồn tại), rented (status rented, còn tồn tại), billed, collected (tổng), occupied_days, available_days, occupancy_rate}`; sắp theo `billed` giảm; không nhận `building_id` filter.
15. `utilities`: mỗi tháng `{month, electricity{consumption,cost}, water{consumption,cost}}` từ `meter_readings` (`read_on` trong khoảng) join `room_services.type ∈ {electricity,water}`: `consumption = Σ consumption`, `cost = Σ consumption × unit_price`; tháng không có số đọc = 0.
16. `dashboard?from&to&sort&limit&building_id`: MỘT câu trả lời gộp `{overview, revenue, occupancy, rooms, utilities}` (tính từ cùng danh sách phòng, kết quả phải bằng các endpoint riêng — test `gives the front-page figures in one answer…`).
17. `export` (xlsx): Laravel có `GET /stats/property/export` (throttle 20/giờ `stats-export`, `Cache-Control: no-store`). Bỏ qua ở Go trừ khi yêu cầu (xem Cần hỏi 4.3).
**Admin / System** (chỉ số tổng hợp, không dữ liệu cá nhân)
18. `GET /stats/admin/overview` (`stats.admin`): `users{total (chưa xóa), active, new_this_month, new_previous_month, change (Ratio.Change), by_role{tenant,landlord,manager,admin,super_admin → số user chưa xóa có role đó (user nhiều role đếm ở mỗi role)}}`; `applications{by_status{pending_review,approved,rejected}, average_review_hours (trung bình (reviewed_at − created_at) theo giờ, 1 số lẻ, trên tối đa 200 đơn đã duyệt trong 90 ngày gần nhất; null nếu không có), oldest_waiting_days (null nếu không có đơn chờ)}`; `reports{by_status{open,in_progress,resolved}, oldest_open_days, created_this_month}`; `subscriptions{pending_requests}` (subscriptions status pending); `announcements{sent_this_month (chưa thu hồi, sent_at >= đầu tháng), recent_read_rate (Percent(đã đọc, tổng người nhận) trên 5 tin gần nhất chưa thu hồi: reached = Σ recipient_count; read = số notification có announcement_id thuộc 5 tin và read_at not null)}`.
19. `GET /stats/admin/growth?from&to` (`stats.admin`): `months[{month, users (chưa xóa), applications, reports}]` đếm theo tháng của `created_at`; cùng giới hạn khoảng.
20. `GET /stats/system/overview` (`stats.system`, CHỈ super_admin): `staff{admins, super_admins}`; `plans[{code,name,audience,price,billing_interval,subscribers (subscriptions active)}]` theo `display_order`; `monthly_recurring_revenue = Σ price × subscribers / (billing_interval=='year' ? 12 : 1)`; `paid_subscribers` = Σ subscribers của gói price>0; `setting_changes` = 5 thay đổi cài đặt gần nhất (mục 1, luật 21); `audit_last_30_days{important, routine}` (đếm dòng 30 ngày gần nhất); `failed_jobs` (Go: không có bảng failed_jobs → bỏ hoặc thay bằng chỉ số của worker — Cần hỏi 4.4).
**Cache**
21. Chỉ cho thống kê chủ trọ/quản lý (admin/system/tin gửi KHÔNG cache). TTL `STATS_CACHE_SECONDS`=900 (0 = tắt; test = 0). Khóa = ngày hôm nay + tên + userId + ACTIVE ROLE + building_id|all + md5(params) (ngày nằm trong khóa vì "quá hạn" và "tháng này" đổi theo ngày). Gắn tag theo MỖI chủ trọ có phòng trong câu trả lời (quản lý nhiều chủ → nhiều tag); chỉ bỏ cache khi tag của chủ trọ đó bị flush. Nếu store không hỗ trợ tag (Go: dùng key-set Redis per landlord) hoặc không có phòng thì tính lại mỗi lần (KHÔNG cache sai).
22. Invalidate (xóa mọi cache của landlord) khi saved/deleted: `Room`, `RoomManager`, `Building` (landlord_id trực tiếp), `Contract` (qua phòng), `Invoice` (qua hợp đồng), `Payment` (qua hóa đơn→hợp đồng), `MeterReading` (qua room_service→phòng). Test: `shows a payment in the cached overview as soon as it is confirmed`, `drops only the cached figures of the landlord whose data changed`, `removes an answer cached for several landlords when any one of them changes`, `works the figures out again on a new day`.

### Endpoint
| method path | quyền | ghi chú |
|---|---|---|
| GET /stats/property/dashboard|overview|revenue|occupancy|rooms|buildings|utilities | `stats.view` (landlord; manager theo phòng được giao) | tham số `from,to,sort,limit,building_id` |
| GET /stats/property/export | `stats.view` | 20/giờ; tùy chọn |
| GET /stats/admin/overview, /growth | `stats.admin` | — |
| GET /stats/system/overview | `stats.system` | chỉ super_admin |

### Quyền: `stats.view`, `stats.admin`, `stats.system` đã có. Lưu ý Laravel role `manager` KHÔNG có `stats.view` trong `ROLE_PERMISSIONS` dù code scope hỗ trợ quản lý — Go catalog cũng không cấp cho manager (CLAUDE.md nói "quản lý ở bước sau"). Giữ như vậy.

### Khác biệt khi sang Go
- Tính bằng SQL `GROUP BY` (không lặp từng dòng); tháng gom bằng `to_char(ts AT TIME ZONE 'Asia/Ho_Chi_Minh','YYYY-MM')` — Cần hỏi múi giờ (Laravel dùng múi giờ app).
- Tiền là `bigint` VND: bỏ `round(…,2)`; JSON số nguyên.
- Cache: Redis, khóa theo landlord (SADD các khóa vào set `stats:landlord:{id}`), xóa set khi có thay đổi — gọi từ usecase qua hook sau commit, không dùng observer.

### Cần hỏi
1. **Hai nguồn mâu thuẫn**: CLAUDE.md nói `billed` = hóa đơn `sent|paid` theo `period_start`, không tính nháp; CODE+test tính `draft|sent|paid` theo `created_at`, nháp được tính là còn nợ. Go theo cái nào? (Đề xuất theo code/test vì test khẳng định.)
2. `occupancy_rate` ở overview là theo trạng thái phòng hiện tại (rented/total) còn ở `occupancy` theo ngày — hai khái niệm khác nhau cùng tên; giữ?
3. Có làm export xlsx không (Go không có thư viện Excel; có thể đổi thành CSV)?
4. `failed_jobs` của system overview không có tương đương ở Go; dùng gì (số job lỗi của worker, hay bỏ)?
5. `overdue_count/amount` tính cả hóa đơn `draft` quá `due_at` (vì UNPAID gồm draft); có hợp lý khi nháp chưa gửi mà vẫn "quá hạn"? Nghi bug nhỏ.
6. Admin stats `users.by_role` đếm user nhiều role nhiều lần, tổng ≠ `total` — chấp nhận?

---

## 5. report (báo cáo + hội thoại)

### Bảng/field
- `reports`: `id uuid`, `reporter_id` (FK users), `type` room|feature, `room_id?` (FK, SET NULL khi xóa phòng), `subject ≤150`, `body ≤5000`, `status` open|in_progress|resolved (default open), `created_at/updated_at`, `deleted_at?` (xóa mềm của tác giả). CHECK `(type='room') = (room_id IS NOT NULL)` lúc tạo (Laravel không CHECK; room_id về null nếu phòng xóa). Index `(type,status)`, `(room_id,status)`, `(reporter_id)`.
- `report_replies`: `id uuid`, `report_id` (cascade), `author_id`, `body ≤5000`, timestamps; index `(report_id, created_at)`.
- (Laravel cũng có realtime: event `ReportUpdated` kênh private `reports.{id}`, `ReportTyping`. Xem Khác biệt.)

### Luật
1. Tạo `POST /reports {type, room_id?, subject, body}` (quyền `reports.create`): `type=room` → chỉ role **tenant** (đang dùng), `room_id` bắt buộc và phải là phòng người này đang thuê hoặc đã thuê: tồn tại hợp đồng `tenant_id = me`, `room_id`, `status ∈ {active, ended}` (hợp đồng `pending`/`rejected` KHÔNG đủ) → ngược lại 422 `room_id` (room_not_yours). `type=feature` → tenant hoặc landlord (role đang dùng). Landlord (hoặc role khác) tạo `room` → 403. Trạng thái đầu `open`; `room_id` bị bỏ khi `type=feature`. — `StoreReportRequest`, test "lets tenants and landlords report a feature…".
2. Người nhận (addressed): `room` → landlord sở hữu phòng (`rooms.landlord_id`) VÀ đang ở role landlord; `feature` → role `admin` (đang dùng). Super_admin: mọi hành động đều được (`ReportPolicy::before` trả true) — kể cả xem/trả lời/đổi status (xem Cần hỏi 5.1).
3. Thông báo khi tạo: `report_received {subject, reporter, room?, report_id}` url `/reports?open={id}` tới chủ phòng (type room) hoặc TOÀN BỘ admin + super_admin (type feature; khác với đơn chủ trọ: ở đây có super_admin). Audit `report.created` meta `{report_id,type}` references `room_id`, `owner_id`=chủ phòng, `target_user_id`=tác giả.
4. Xem một báo cáo (`view`): tác giả HOẶC người nhận; nếu báo cáo đã xóa mềm thì tác giả KHÔNG còn xem được (403), người nhận vẫn xem (kèm `deleted:true`).
5. Hội thoại `POST /reports/{id}/replies {body ≤5000}`: cần `reply` = chưa xóa mềm VÀ `view`. Tác giả và người nhận nhắn không giới hạn. Báo bên kia: tác giả nhắn → `report_replied` tới người nhận (chủ phòng hoặc mọi admin+super_admin); người nhận nhắn → `report_answered` tới tác giả. Audit `report.replied` chỉ meta `{length}` (KHÔNG lưu nội dung). Response trả báo cáo mới (kèm `replies_count`).
6. Đổi trạng thái `PUT /reports/{id}/status {status}` (quyền route `reports.update`; policy `manage` = chưa xóa mềm VÀ là người nhận): chỉ người nhận, không phải tác giả (tác giả không đổi được). Cho phép đặt bất kỳ trong 3 trạng thái, không ràng buộc thứ tự. Audit `report.status_changed` `changes.status{from,to}`. Khi chuyển sang `resolved` tác giả nhận `report_answered` (author rỗng). Đặt cùng giá trị cũ vẫn chạy (ghi audit rỗng + vẫn báo nếu resolved — xem Cần hỏi 5.2).
7. Xóa `DELETE /reports/{id}`: chỉ tác giả có role tenant (đang dùng) và báo cáo chưa xóa → xóa MỀM; tin nhắn giữ nguyên. Chủ phòng/admin → 403; super_admin → được (before). Sau xóa: tác giả không thấy trong `GET /reports`; người nhận vẫn thấy trong inbox, chỉ đọc (reply/status → 403). Audit `report.deleted`.
8. Danh sách: `GET /reports` (của tôi; quyền `reports.view`; KHÔNG gồm đã xóa) và `GET /reports/inbox` (cần xử lý; quyền `reports.update`; GỒM đã xóa mềm; `addressedTo(user)`: super_admin = tất cả; admin → `type=feature`; landlord → `type=room` thuộc phòng của mình; người có cả hai role (không xảy ra vì staff không trộn) lấy OR; role khác → rỗng). Lọc `status`, `type`, `search` (một từ trong `lower(subject)` hoặc `lower(body)`, ≤100 ký tự); sắp `created_at DESC, id DESC`; `per_page` ≤100 mặc định 10.
9. `GET /reports/{id}` không phân trang, chỉ trả `replies_count`; `GET /reports/{id}/replies?page&per_page(≤50, mặc định 20)`: mới nhất trước (`created_at DESC, id DESC`), web đảo lại. Mỗi reply kèm tác giả và `role` (để web dẫn tới `/tenants/{id}` hoặc `/landlords/{id}`; admin không có hồ sơ).
10. `POST /reports/{id}/typing` (throttle 60/phút tên `reports-typing`; quyền `reply`): chỉ phát sự kiện "đang gõ" tới thành viên khác, không lưu. Tùy chọn ở Go.
11. Route `show/replies/reply/typing/status` dùng binding có cả báo cáo đã xóa mềm (withTrashed) rồi policy quyết định.

### Endpoint
| method path | quyền route | policy |
|---|---|---|
| GET /reports | reports.view | của tôi |
| GET /reports/inbox | reports.update | addressedTo |
| POST /reports | reports.create | luật 1 |
| GET /reports/{id}, GET …/replies | reports.view | view |
| POST …/replies | reports.view | reply |
| POST …/typing | reports.view, 60/phút | reply |
| PUT …/status | reports.update | manage |
| DELETE /reports/{id} | reports.view | delete |

### Quyền: catalog Go có `reports.*`: tenant `reports.create|view`; landlord/admin/super_admin `reports.manage` (= mọi hành động kể cả `reports.update`). Khớp Laravel (landlord `reports.*`, tenant create+view). Không thêm.

### Khác biệt khi sang Go
- Realtime: Laravel dùng Reverb/websocket. Go chưa có kênh; có thể thay bằng thông báo + polling, hoặc SSE sau; không chặn mục này.
- Gỡ morph/Notifiable; `NotificationType` mới: `report_received`, `report_replied`, `report_answered` (trước đây có thể đã có ở notification context của Go — kiểm tra).
- Nội dung `body` phải escape khi hiển thị (do web), không HTML.

### Cần hỏi
1. `before()` của super_admin trả true cho MỌI ability: super admin xóa báo cáo của người khác, trả lời báo cáo đã xóa mềm. Cố ý hay lỗi? Go nên chỉ cho super_admin xem + đổi status (như admin) thay vì mọi thứ?
2. Đặt `status` trùng giá trị cũ vẫn ghi audit và (khi `resolved`) vẫn báo lại tác giả — nên bỏ qua khi không đổi?
3. `room_id` của báo cáo `room` là `ON DELETE SET NULL` → báo cáo mất phòng thì không còn người nhận (`addressedTo` theo `room`) — người nhận không thấy nữa. Giữ?
4. Người nhận loại `room` yêu cầu chủ phòng đang ở role landlord; nếu chủ trọ bị thu hồi role thì báo cáo cũ "mồ côi". Chấp nhận?
5. Báo cáo `feature` của tenant tới ĐỒNG THỜI admin và super_admin (khác đơn chủ trọ chỉ báo admin): đúng ý muốn?

---

## 6. social (đăng nhập Google/Zalo, liên kết/hủy liên kết)

### Bảng/field
- `oauth_accounts`: `id`, `user_id` (FK), `provider` (google|zalo; Laravel enum còn `facebook` nhưng KHÔNG đăng ký), `provider_user_id`, `unlinked_at timestamptz?` (null = đang liên kết), `created_at`. Unique `(provider, provider_user_id)` và unique `(user_id, provider)` (mỗi tài khoản tối đa MỘT tài khoản dịch vụ mỗi nhà cung cấp; hủy liên kết = đặt `unlinked_at`, không xóa dòng).
- `users.email` nullable (tài khoản Zalo không email), `users.password_hash` nullable (đã nullable ở Go), `email_verified_at`.
- State OAuth và mã đăng nhập một lần: lưu Redis (không DB), xem luật 2–3.

### Luật
**Nhà cung cấp** (`SocialProvider`: `provider()`, `vouchesForEmail()`, `isConfigured()`, `authorizationUrl(state, verifier)`, `profileFromCallback(request, verifier) → SocialProfile{provider, id, name, email?, emailVerified, avatarUrl?}`)
1. Chỉ nhà cung cấp ĐÃ CÓ khóa cấu hình mới bật/truy cập được (không có → route 404; `GET /config` trả `auth.social_providers`). Google: `vouchesForEmail=true`, bắt buộc có id và email, `emailVerified=true`, scope mặc định; Zalo: `vouchesForEmail=false`, email null, `emailVerified=false`, tên rỗng → "Zalo user". Zalo: OAuth v4 `https://oauth.zaloapp.com/v4/permission` (`app_id, redirect_uri, code_challenge, code_challenge_method=S256, state`), đổi code tại `…/v4/access_token` (header `secret_key`, body `app_id, code, grant_type=authorization_code, code_verifier`), lấy hồ sơ `GET https://graph.zalo.me/v2.0/me?fields=id,name,picture` header `access_token`; thiếu `access_token`/`id` → lỗi → `social_failed`. Env: `ZALO_APP_ID`, `ZALO_APP_SECRET`, `ZALO_CALLBACK_URL`; Google: `GOOGLE_CLIENT_ID/SECRET/REDIRECT`.
2. **State**: `begin()` sinh `verifier = random(64)` và `state = random(40 chữ-số)`; lưu `{provider, intent: login|link, user_id?, redirect, code_verifier}` trong Redis key `social_state:{sha256(state)}`, TTL 10 phút, DÙNG MỘT LẦN (`pull` = lấy rồi xóa). `state` không đúng `^[A-Za-z0-9]{40}$`, không có, đã dùng, hoặc `state.provider ≠ route provider` → kết quả lỗi `social_failed` (chặn login CSRF). PKCE: `challenge = base64url(sha256(verifier))` không padding; `verifier` hợp lệ `^[A-Za-z0-9\-._~]{43,128}$`. — `Domain/Auth/Pkce`, `PkceTest`, `SocialStateStoreTest`.
3. **Redirect an toàn**: `redirect` (query của route login) bỏ `/` đầu rồi phải khớp `^[A-Za-z0-9/_-]+$`, ngược lại dùng mặc định `dashboard` (không cho host ngoài). Link luôn quay về `profile`.
4. **Kết thúc đăng nhập (intent=login)**: callback KHÔNG đặt token trên URL. Thành công → sinh mã `random(48)`, lưu Redis `social_login:{sha256(code)}` = `{user_id, via: provider}` TTL `SOCIAL_LOGIN_CODE_TTL_SECONDS`=60, redirect `CLIENT_URL/{redirect}?code=…`. Lỗi → redirect `CLIENT_URL/login?error=<mã>`. `POST /auth/social/exchange {code}` (throttle `auth`): `pull` mã (một lần); sai/hết hạn/đã dùng hoặc user không còn → 401 `auth.social_code_invalid`; đúng → mở phiên (như login mật khẩu, audit `auth.login` meta `via`=provider). Cache lưu mảng thuần (không đối tượng).
5. **Tìm/tạo tài khoản** (`login(profile)`):
   a. Có `oauth_accounts (provider, provider_user_id)` mà `unlinked_at` NOT NULL → lỗi `link_unlinked` (không đăng nhập, kể cả cùng email).
   b. User = chủ của dòng đó (kể cả đã xóa mềm); nếu chưa có và `profile.email` có & `emailVerified` → tìm user theo email (kể cả đã xóa mềm).
   c. User đã xóa mềm → `account_inactive` (không khôi phục, không tạo lại). Không có user → tạo tài khoản mới: role **tenant** duy nhất, `status=active`, tên = profile.name, `email` = profile.email chỉ khi `emailVerified` (ngược lại null), `email_verified_at=now` nếu có email, không mật khẩu, avatar tải từ `profile.avatarUrl` (lưu WebP; lỗi lưu avatar không được làm hỏng đăng nhập — Laravel để lỗi nổi lên và xóa file; Go nên bỏ qua avatar lỗi — Cần hỏi 6.3), terms: tài khoản tạo bằng social KHÔNG qua form → `needs_acceptance` sẽ hỏi lần đầu.
   d. Nếu user đã có dòng `oauth_accounts` của provider này nhưng `provider_user_id` khác: `unlinked_at` null → `link_provider_taken`; đã hủy → `link_unlinked`. (Mỗi tài khoản một dịch vụ mỗi loại: một Google khác không mở được tài khoản này.)
   e. Chưa có dòng → tạo dòng liên kết. Nếu provider chứng thực email trùng email tài khoản và tài khoản CHƯA xác minh email → đặt `email_verified_at=now` và XÓA mật khẩu (`password=null`: mật khẩu do người khác đặt trên địa chỉ này không còn giá trị).
   f. Cuối: user `status != active` → `account_inactive`.
   Test: `creates a tenant account on first Google login…`, `links Google to an existing account instead of duplicating it`, `does not sign a disabled account in…`, `does not recreate a deleted account…`.
6. **Liên kết (intent=link)**: `POST /my/oauth-accounts/{provider}` (throttle 20/giờ `oauth-link`) trả `{url}` (state intent=link, user_id, redirect `profile`); callback xong redirect `CLIENT_URL/profile?linked={provider}` hoặc `?link_error={mã}&provider=`. State link thiếu user hoặc user không còn → `social_failed`. Trình tự `link(user, profile)`:
   1. User có role admin/super_admin → `link_staff` (staff chỉ đăng nhập bằng mật khẩu).
   2. `ProviderLink.emailRefusal(vouchesForEmail, accountEmail, serviceEmail)`: dịch vụ KHÔNG chứng thực email (Zalo) → nil (liên kết tự do); ngược lại tài khoản không có email (null/rỗng) → `link_no_email`; email khác (so không phân biệt hoa thường, trim) → `link_email_mismatch`. **Liên kết không bao giờ đổi email của tài khoản.**
   3. Tài khoản dịch vụ này (`provider, provider_user_id`) đã mở tài khoản KHÁC → `link_elsewhere`.
   4. `ProviderLink.decide(existingId?, existingActive, serviceAccountId)`: không có dòng → CREATE; cùng id → đang bật ALREADY (no-op, không audit) / đã hủy RELINK (xóa `unlinked_at`); id khác → đang bật TAKEN (`link_provider_taken`) / đã hủy REPLACE (thay `provider_user_id`, xóa `unlinked_at`). Audit `auth.oauth_linked` meta `{provider, again: decision≠CREATE}` (actor = user). Lưu ý: Google đã hủy liên kết thì CHỈ liên kết lại được bằng mật khẩu đăng nhập rồi liên kết ở hồ sơ.
7. **Hủy liên kết** `DELETE /my/oauth-accounts/{provider}`: `SignInMethods{hasPassword, linkedProviders}.canUnlink(p)` = p đang liên kết VÀ (`hasPassword` HOẶC số liên kết đang bật > 1) — không thì 422 `provider` (social_last_method: không gỡ cách đăng nhập cuối). OK → đặt `unlinked_at=now` (không xóa dòng); audit `auth.oauth_unlinked` meta `{provider}`. Sau khi hủy, đăng nhập bằng dịch vụ đó bị từ chối (`link_unlinked`) cho tới khi tự liên kết lại.
8. `GET /my/oauth-accounts`: `{has_password, providers[{provider, linked, linked_at(created_at), can_unlink, unlinked, requires_matching_email = vouchesForEmail}]}` chỉ gồm provider ĐÃ BẬT.
9. Throttle: `social-auth` = 30/phút theo IP cho `GET /auth/{provider}` và `/callback` (ĐỪNG dùng limiter `auth` có bộ đếm theo email). Mã lỗi redirect đầy đủ: `social_failed, account_inactive, link_unlinked, link_provider_taken, link_elsewhere, link_staff, link_email_mismatch, link_no_email`.

### Endpoint
| method path | quyền | throttle |
|---|---|---|
| GET /auth/{google|zalo}[?redirect=] | công khai | social-auth 30/phút/IP |
| GET /auth/{provider}/callback | công khai | social-auth |
| POST /auth/social/exchange {code} | công khai | auth |
| GET /my/oauth-accounts | đăng nhập | — |
| POST /my/oauth-accounts/{provider} | đăng nhập | 20/giờ |
| DELETE /my/oauth-accounts/{provider} | đăng nhập | — |

### Quyền: không quyền mới (chỉ cần đăng nhập).

### Khác biệt khi sang Go
- Go đã có `user_sessions` + refresh token riêng: `exchange` phát cặp token như login; ghi `via` vào audit/session.
- Bảng `oauth_accounts` mới (Go chưa có). Giữ `unlinked_at` thay vì xóa.
- Cache state/code bằng Redis `GETDEL` (atomic). Bắt buộc PKCE cho Zalo (Google dùng thư viện OAuth chuẩn; nếu tự cài hãy thêm PKCE + `state`).
- Nhật ký `auth.login` dùng `via`. Thêm nhà cung cấp = một struct cài interface + đăng ký, không đổi bảng.
- Email người dùng Google đã `emailVerified=true` ⇒ Go set `email_verified_at` luôn (Go yêu cầu xác minh email cho đăng ký thường).

### Cần hỏi
1. `login()` KHÔNG chặn staff đăng nhập bằng Google khi email Google trùng email của admin/super_admin (chỉ chặn khi *liên kết*; thực tế tự gộp theo email rồi tạo `oauth_accounts` cho admin). Mâu thuẫn với ý "staff chỉ dùng mật khẩu". Go nên chặn?
2. Đăng nhập Google tự gộp vào tài khoản đã có theo email, rồi XÓA mật khẩu nếu email chưa xác minh — có chủ ý (chống chiếm email) nhưng người dùng mất mật khẩu; xác nhận.
3. Tải avatar từ URL dịch vụ khi tạo tài khoản: lỗi mạng có làm hỏng đăng nhập? (Laravel `storeFromUrl` — chưa kiểm xử lý lỗi.) Go nên best-effort và chống SSRF (chỉ https, host của Google/Zalo, giới hạn kích thước).
4. Facebook có trong enum nhưng không có provider; bỏ.
5. Tài khoản social tạo mới chưa có `terms_version` ⇒ hỏi đồng ý ở lần mở đầu (đúng như CLAUDE.md "tài khoản Google lần đầu cũng bị hỏi một lần") — Go đảm bảo `terms_version` null thì `needs_acceptance=true`.

---

## 7. nợ nhỏ

### 7.1 Giới hạn đổi mật khẩu theo tài khoản
- **Laravel KHÔNG có endpoint đổi mật khẩu khi đang đăng nhập** (không tìm thấy route/test `current_password`): chỉ có quên/đặt lại mật khẩu (`/auth/forgot-password`, `/auth/reset-password`, throttle `auth` = 10/phút theo IP + 5/phút theo `IP|email`). Đặt lại: token băm lưu bảng, hết hạn `password_reset_ttl_hours`; sai token/email → 422 `token`; thành công → đặt mật khẩu, thu hồi MỌI phiên (`revokeAllFor`), xóa token, audit `auth.password_reset`. Quên mật khẩu: audit `auth.password_reset_requested`.
- Go đã có `ChangePassword(current,next)` (sai mật khẩu hiện tại → `ErrCurrentPasswordWrong`, trùng → `ErrNewPasswordSame`, đóng các phiên khác). **Chưa có nguồn Laravel cho giới hạn theo tài khoản** → đề xuất (không phải luật cũ): throttle `5 lần / 15 phút / user_id` trên `PUT /profile/password` (và đếm riêng cho sai mật khẩu hiện tại: 5 lần sai/giờ rồi 429 `retry_after`), vì bộ giới hạn IP không chặn kẻ đã cầm token đoán mật khẩu cũ. Tên limiter phải RIÊNG (bài học Laravel: `throttle:N,M` không tên dùng chung bộ đếm giữa các route → luôn đặt tên).
- Cần hỏi 7.1: quyết định ngưỡng và có audit `auth.password_changed` (Laravel không có action này).

### 7.2 Rehash khi đăng nhập
- **Laravel không làm rehash tường minh**: `AuthService::authenticate` chỉ `Hash::check` rồi cho vào (không `Hash::needsRehash`), không có test. Cast `password` của User dùng hashing mặc định của Laravel.
- Luật đề xuất cho Go (hợp lý với argon2id đang dùng): sau khi `Verify` đúng, nếu hash lưu dùng tham số khác hiện hành (memory 19456 KiB, time 2, threads 1, hoặc không phải argon2id) → băm lại bằng tham số mới và `UpdatePassword` (cùng transaction ngắn, lỗi cập nhật chỉ log, không làm hỏng đăng nhập). Không rehash khi mật khẩu sai. Hasher cần `NeedsRehash(hash) bool` (parse tham số trong chuỗi PHC). Cần hỏi 7.2: xác nhận làm.

### 7.3 Dọn lời mời hết hạn (`invitations:prune`, hằng ngày 00:30, không chạy chồng; Go: thêm `invitations-prune` vào worker)
Với CẢ `contract_invitations` và `manager_invitations`, `cutoff = now − INVITATION_RETENTION_DAYS (7)`:
1. Lời mời `pending` có `expires_at <= now` mà còn `link_token` → đặt `link_token`(sealed) NULL (xóa bí mật của liên kết đã hết hạn; ĐẾM `cleared`). Lưu ý: ở Go CHECK `status='pending' OR link_token_sealed IS NULL` — vẫn còn `pending` nên hợp lệ.
2. XÓA hẳn: (a) `pending` có `expires_at < cutoff`; (b) `declined` hoặc `revoked` có `updated_at < cutoff`. KHÔNG xóa `accepted` (còn tham chiếu `contract_id`/`room_managers`) và KHÔNG xóa `awaiting_approval` (Go: `awaiting_approval` quá hạn? Cần hỏi 7.3).
3. In ra `{cleared, deleted}`. Idempotent. Với `manager_invitation_rooms` dùng ON DELETE CASCADE.
4. Không liên quan: `notifications-prune`, refresh token prune (đã có ở Go) và `invitation_email_blocks` (giữ vĩnh viễn).
- Cần hỏi 7.3: lời mời `awaiting_approval` không bao giờ hết hạn trong code cũ (chờ chủ trọ vô thời hạn) — giữ?

### 7.4 Email đa ngôn ngữ
- Laravel: web gửi cookie `i18n_redirected` (vi|en) hoặc header `Accept-Language`; middleware `SetLocale` chọn `vi|en` (mặc định `APP_LOCALE=en`, fallback en) cho từng request. KHÔNG có cột ngôn ngữ trên `users` (không `HasLocalePreference`). Mail đều là queued Mailable, tiêu đề/nội dung dịch bằng khóa `mail.*` (`lang/{vi,en}/mail.php`) lúc render — nên ngôn ngữ email = ngôn ngữ của request gây ra việc gửi nếu Laravel giữ locale qua hàng đợi, còn job/lệnh theo lịch (nhắc hóa đơn, hóa đơn tự động) dùng locale mặc định `en` (Cần hỏi 7.4).
- Danh sách email: xác minh email, đặt lại mật khẩu, đơn chủ trọ (nhận/duyệt), bài đăng (nộp/duyệt/gỡ), thông báo hợp đồng (đăng ký thuê, kết thúc), hóa đơn, thanh toán được duyệt, lời mời (`InvitationMail` kinds: contract/manager/contract_approval…; có link "Từ chối nhận lời mời" opt-out).
- Go hiện: `VerificationMailer.SendVerification(..., lang)` có tham số ngôn ngữ, còn `invitation` mailer gửi tiếng Việt cố định ("no account language is known yet"). Đề xuất: lưu `users.language` (vi|en, mặc định lấy lúc đăng ký/đổi qua `Accept-Language`/cookie) và truyền `lang` vào mọi mail gửi cho tài khoản; mail tới địa chỉ chưa có tài khoản (lời mời theo email) lấy ngôn ngữ của NGƯỜI GỬI (chủ trọ); bản dịch dùng `pkg/i18n` (khóa `mail.<loại>.<…>`), thiếu khóa → fallback `vi`.
- Cần hỏi 7.4: Go dùng cột `users.language` (cần migration) hay chỉ theo ngôn ngữ request? Mail chạy ở worker (nền) không có request → phải lưu ngôn ngữ vào dữ liệu job/outbox.

---

## Quyết định của chủ dự án (2026-11-24) — thắng mọi chỗ khác trong file

Đồng ý toàn bộ đề xuất cho các mục "Cần hỏi":
- admin: `roles: []` bị cấm (422); xóa người dùng thu hồi mọi phiên; staff không đăng nhập/liên kết bằng mạng xã hội.
- report: super_admin chỉ xem + đổi status (như admin), không xóa/trả lời hộ; đặt status trùng giá trị cũ thì không audit, không báo lại.
- stats: "đã lập hóa đơn" = hóa đơn `sent|paid` theo `period_start`, KHÔNG tính nháp (theo CLAUDE.md); không xlsx (CSV hoặc bỏ); bỏ `failed_jobs`.
- manager: CHECK lời mời quản lý chỉ 4 trạng thái; quản lý mất hết phòng → lời mời thuê `awaiting_approval` do họ soạn tự bị thu hồi; quản lý thấy mọi hợp đồng của phòng được giao.
- social: bỏ Facebook; avatar tải từ dịch vụ chỉ https + host Google/Zalo + giới hạn kích thước, best-effort (lỗi không làm hỏng đăng nhập).
- nợ nhỏ: đổi mật khẩu giới hạn 5 lần/15 phút/tài khoản + rehash argon2id khi tham số khác; thêm `users.language` (migration) cho email của lệnh định kỳ.
- Các "Cần hỏi" còn lại không nêu ở đây: theo đề xuất trong chính mục đó.
