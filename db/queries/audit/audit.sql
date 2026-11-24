-- name: InsertAuditLogs :exec
-- One statement for any number of entries: @entries is a JSON array of rows.
INSERT INTO audit_logs (created_at, actor_id, actor_role, acting_for_id, action, room_id, contract_id, invoice_id, payment_id,
                        target_user_id, changes, meta, ip, user_agent, session_id, request_id)
SELECT r.created_at, r.actor_id, r.actor_role, r.acting_for_id, r.action, r.room_id, r.contract_id, r.invoice_id, r.payment_id,
       r.target_user_id, r.changes, r.meta, r.ip, r.user_agent, r.session_id, r.request_id
FROM jsonb_to_recordset(@entries::jsonb) AS r(created_at timestamptz, actor_id uuid, actor_role text, acting_for_id uuid, action text,
     room_id uuid, contract_id uuid, invoice_id uuid, payment_id uuid, target_user_id uuid, changes jsonb, meta jsonb, ip text,
     user_agent text, session_id uuid, request_id text);

-- name: InsertAuditActivity :exec
-- One statement for any number of entries: @entries is a JSON array of rows.
INSERT INTO audit_activity (created_at, actor_id, actor_role, acting_for_id, action, room_id, contract_id, invoice_id, payment_id,
                        target_user_id, changes, meta, ip, user_agent, session_id, request_id)
SELECT r.created_at, r.actor_id, r.actor_role, r.acting_for_id, r.action, r.room_id, r.contract_id, r.invoice_id, r.payment_id,
       r.target_user_id, r.changes, r.meta, r.ip, r.user_agent, r.session_id, r.request_id
FROM jsonb_to_recordset(@entries::jsonb) AS r(created_at timestamptz, actor_id uuid, actor_role text, acting_for_id uuid, action text,
     room_id uuid, contract_id uuid, invoice_id uuid, payment_id uuid, target_user_id uuid, changes jsonb, meta jsonb, ip text,
     user_agent text, session_id uuid, request_id text);

-- name: SearchAuditEntries :many
-- Each tier contributes at most @branch_limit rows (page * per_page + 1, newest first) before the two are merged, so a
-- page never sorts a whole table. The names of the two people an entry mentions are joined after the page is cut.
WITH merged AS (
    (SELECT 'important'::text AS tier, id, created_at, actor_id, actor_role, acting_for_id, action, room_id, contract_id, invoice_id, payment_id, target_user_id, changes, meta, ip, user_agent, session_id, request_id
     FROM audit_logs
     WHERE @include_important::bool
          AND (sqlc.narg('actor_id')::uuid IS NULL OR actor_id = sqlc.narg('actor_id')::uuid)
          AND (sqlc.narg('actor_role')::text IS NULL OR actor_role = sqlc.narg('actor_role')::text)
          AND (sqlc.narg('action')::text IS NULL OR action = sqlc.narg('action')::text)
          AND (sqlc.narg('target_user_id')::uuid IS NULL OR target_user_id = sqlc.narg('target_user_id')::uuid)
          AND (sqlc.narg('room_id')::uuid IS NULL OR room_id = sqlc.narg('room_id')::uuid)
          AND (sqlc.narg('contract_id')::uuid IS NULL OR contract_id = sqlc.narg('contract_id')::uuid)
          AND (sqlc.narg('invoice_id')::uuid IS NULL OR invoice_id = sqlc.narg('invoice_id')::uuid)
          AND (sqlc.narg('from_at')::timestamptz IS NULL OR created_at >= sqlc.narg('from_at')::timestamptz)
          AND (sqlc.narg('before_at')::timestamptz IS NULL OR created_at < sqlc.narg('before_at')::timestamptz)
          AND (sqlc.narg('involving')::uuid IS NULL OR actor_id = sqlc.narg('involving')::uuid OR target_user_id = sqlc.narg('involving')::uuid)
          AND (NOT @exclude_settings::bool OR action <> 'setting.changed')
          AND (NOT @exclude_staff::bool OR (
                (actor_role IS NULL OR actor_role NOT IN ('admin', 'super_admin'))
            AND (actor_id IS NULL OR actor_id NOT IN (SELECT user_id FROM user_roles WHERE role_code IN ('admin', 'super_admin')))
            AND (target_user_id IS NULL OR target_user_id NOT IN (SELECT user_id FROM user_roles WHERE role_code IN ('admin', 'super_admin')))))
     ORDER BY created_at DESC, id DESC
     LIMIT @branch_limit)
    UNION ALL
    (SELECT 'routine'::text AS tier, id, created_at, actor_id, actor_role, acting_for_id, action, room_id, contract_id, invoice_id, payment_id, target_user_id, changes, meta, ip, user_agent, session_id, request_id
     FROM audit_activity
     WHERE @include_routine::bool
          AND (sqlc.narg('actor_id')::uuid IS NULL OR actor_id = sqlc.narg('actor_id')::uuid)
          AND (sqlc.narg('actor_role')::text IS NULL OR actor_role = sqlc.narg('actor_role')::text)
          AND (sqlc.narg('action')::text IS NULL OR action = sqlc.narg('action')::text)
          AND (sqlc.narg('target_user_id')::uuid IS NULL OR target_user_id = sqlc.narg('target_user_id')::uuid)
          AND (sqlc.narg('room_id')::uuid IS NULL OR room_id = sqlc.narg('room_id')::uuid)
          AND (sqlc.narg('contract_id')::uuid IS NULL OR contract_id = sqlc.narg('contract_id')::uuid)
          AND (sqlc.narg('invoice_id')::uuid IS NULL OR invoice_id = sqlc.narg('invoice_id')::uuid)
          AND (sqlc.narg('from_at')::timestamptz IS NULL OR created_at >= sqlc.narg('from_at')::timestamptz)
          AND (sqlc.narg('before_at')::timestamptz IS NULL OR created_at < sqlc.narg('before_at')::timestamptz)
          AND (sqlc.narg('involving')::uuid IS NULL OR actor_id = sqlc.narg('involving')::uuid OR target_user_id = sqlc.narg('involving')::uuid)
          AND (NOT @exclude_settings::bool OR action <> 'setting.changed')
          AND (NOT @exclude_staff::bool OR (
                (actor_role IS NULL OR actor_role NOT IN ('admin', 'super_admin'))
            AND (actor_id IS NULL OR actor_id NOT IN (SELECT user_id FROM user_roles WHERE role_code IN ('admin', 'super_admin')))
            AND (target_user_id IS NULL OR target_user_id NOT IN (SELECT user_id FROM user_roles WHERE role_code IN ('admin', 'super_admin')))))
     ORDER BY created_at DESC, id DESC
     LIMIT @branch_limit)
)
SELECT m.tier, m.id, m.created_at, m.actor_id, m.actor_role, m.acting_for_id, m.action, m.room_id, m.contract_id, m.invoice_id, m.payment_id, m.target_user_id, m.changes, m.meta, m.ip, m.user_agent, m.session_id, m.request_id, a.full_name AS actor_name, t.full_name AS target_name
FROM (SELECT * FROM merged ORDER BY created_at DESC, id DESC LIMIT @page_limit OFFSET @page_offset) AS m
LEFT JOIN users a ON a.id = m.actor_id
LEFT JOIN users t ON t.id = m.target_user_id
ORDER BY m.created_at DESC, m.id DESC;

-- name: ScrubAuditLogsOfUser :exec
UPDATE audit_logs SET ip = NULL, user_agent = NULL, session_id = NULL
WHERE actor_id = @user_id AND (ip IS NOT NULL OR user_agent IS NOT NULL OR session_id IS NOT NULL);

-- name: ScrubAuditActivityOfUser :exec
UPDATE audit_activity SET ip = NULL, user_agent = NULL, session_id = NULL
WHERE actor_id = @user_id AND (ip IS NOT NULL OR user_agent IS NOT NULL OR session_id IS NOT NULL);

-- name: AuditRoomOwner :one
-- Who owns a room, a contract or an invoice (read-only views of the other contexts' tables, for the history routes).
SELECT landlord_id FROM rooms WHERE id = @id;

-- name: AuditContractOwner :one
SELECT landlord_id FROM contracts WHERE id = @id;

-- name: AuditInvoiceOwner :one
SELECT c.landlord_id FROM invoices i JOIN contracts c ON c.id = i.contract_id WHERE i.id = @id;
