-- name: SearchContracts :many
-- The contracts a viewer sees, with the room, the tenant and the landlord. A landlord (as_landlord) sees the
-- confirmed contracts of their own live rooms; a tenant sees their own. Filters are optional; a NULL filter is off.
-- search_like is a lower-case LIKE pattern body already escaped with "\". sort is "" for the landlord's own order.
SELECT c.id, c.room_id, c.landlord_id, c.tenant_id, c.tenant_name, c.tenant_phone, c.status, c.price_vnd,
       c.deposit_vnd, c.occupant_count, c.starts_on, c.ends_on, c.notes, c.confirmed_at, c.display_order,
       c.cancellation_status, c.cancellation_requested_at, c.cancellation_reason, c.cancellation_response_note,
       c.created_at, c.updated_at,
       r.name AS room_name, r.status AS room_status, r.price_vnd AS room_price_vnd,
       r.minimum_rental_period AS room_minimum_rental_period, r.rental_period_unit AS room_rental_period_unit,
       r.address_detail AS room_address_detail, r.description AS room_description,
       r.area_square_meters AS room_area_square_meters, r.cover_key AS room_cover_key,
       r.cover_thumb_key AS room_cover_thumb_key, r.building_id AS room_building_id,
       r.ward_code AS ward_code, w.name AS ward_name, w.province_code AS province_code, p.name AS province_name,
       t.full_name AS tenant_full_name, t.email AS tenant_email, t.phone_number AS tenant_phone_number,
       l.full_name AS landlord_full_name, l.email AS landlord_email, l.phone_number AS landlord_phone_number,
       b.due_count, b.due_amount_vnd, b.partial AS billing_partial, b.review_count, b.overdue AS billing_overdue,
       b.next_due_at, b.unpaid_invoices
FROM contracts c
JOIN rooms r ON r.id = c.room_id
JOIN users l ON l.id = c.landlord_id
LEFT JOIN users t ON t.id = c.tenant_id
LEFT JOIN wards w ON w.code = r.ward_code
LEFT JOIN provinces p ON p.code = w.province_code
-- Where the contract stands on its bills (read only: the invoice and payment contexts own them). Only a sent bill is
-- due; money already confirmed or waiting for review is not asked twice.
LEFT JOIN LATERAL (
    -- next_due_at is the epoch when no bill is due.
    SELECT (count(*) FILTER (WHERE s.status = 'sent' AND s.open_vnd > 0))::integer AS due_count,
           (coalesce(sum(s.open_vnd) FILTER (WHERE s.status = 'sent'), 0))::bigint AS due_amount_vnd,
           coalesce(bool_or(s.status = 'sent' AND s.open_vnd > 0 AND s.counted_vnd > 0), false)::boolean AS partial,
           (coalesce(sum(s.pending_count) FILTER (WHERE s.status = 'sent'), 0))::integer AS review_count,
           coalesce(bool_or(s.status = 'sent' AND s.open_vnd > 0 AND s.due_at < now()), false)::boolean AS overdue,
           (coalesce(min(s.due_at) FILTER (WHERE s.status = 'sent' AND s.open_vnd > 0), 'epoch'::timestamptz))::timestamptz AS next_due_at,
           count(*)::integer AS unpaid_invoices
    FROM (
        SELECT i.status, i.due_at,
               greatest(0, i.total_amount_vnd - coalesce(sum(pay.amount_vnd), 0))::bigint AS open_vnd,
               coalesce(sum(pay.amount_vnd), 0)::bigint AS counted_vnd,
               count(pay.id) FILTER (WHERE pay.status = 'pending') AS pending_count
        FROM invoices i
        LEFT JOIN payments pay ON pay.invoice_id = i.id AND pay.status IN ('confirmed', 'pending')
        WHERE i.contract_id = c.id AND i.status IN ('draft', 'sent')
        GROUP BY i.id
    ) s
) b ON true
WHERE c.deleted_at IS NULL
  AND ((@as_landlord::boolean AND c.landlord_id = @viewer_id AND c.confirmed_at IS NOT NULL AND r.deleted_at IS NULL)
       OR (NOT @as_landlord::boolean AND c.tenant_id = @viewer_id))
  AND (sqlc.narg('id')::uuid IS NULL OR c.id = sqlc.narg('id')::uuid)
  AND (sqlc.narg('statuses')::text[] IS NULL OR c.status = ANY(sqlc.narg('statuses')::text[]))
  AND (sqlc.narg('room_id')::uuid IS NULL OR c.room_id = sqlc.narg('room_id')::uuid)
  AND (sqlc.narg('building_id')::uuid IS NULL OR r.building_id = sqlc.narg('building_id')::uuid)
  AND (NOT @no_building::boolean OR r.building_id IS NULL)
  AND (sqlc.narg('cancellation_status')::text IS NULL OR c.cancellation_status = sqlc.narg('cancellation_status')::text)
  AND (sqlc.narg('payment_state')::text IS NULL
       OR (sqlc.narg('payment_state')::text = 'due' AND EXISTS (SELECT 1 FROM invoices i WHERE i.contract_id = c.id AND i.status = 'sent'
               AND i.total_amount_vnd > coalesce((SELECT sum(p.amount_vnd) FROM payments p WHERE p.invoice_id = i.id AND p.status IN ('confirmed', 'pending')), 0)))
       OR (sqlc.narg('payment_state')::text = 'review' AND EXISTS (SELECT 1 FROM invoices i JOIN payments p ON p.invoice_id = i.id WHERE i.contract_id = c.id AND i.status = 'sent' AND p.status = 'pending'))
       OR (sqlc.narg('payment_state')::text = 'settled' AND NOT EXISTS (SELECT 1 FROM invoices i WHERE i.contract_id = c.id AND i.status = 'sent'
               AND i.total_amount_vnd > coalesce((SELECT sum(p.amount_vnd) FROM payments p WHERE p.invoice_id = i.id AND p.status IN ('confirmed', 'pending')), 0)) AND NOT EXISTS (SELECT 1 FROM invoices i JOIN payments p ON p.invoice_id = i.id WHERE i.contract_id = c.id AND i.status = 'sent' AND p.status = 'pending')))
  AND (NOT @has_unpaid_invoice::boolean
       OR EXISTS (SELECT 1 FROM invoices i WHERE i.contract_id = c.id AND i.status IN ('draft', 'sent')))
  AND (sqlc.narg('search_like')::text IS NULL
       OR lower(r.name) LIKE '%' || sqlc.narg('search_like')::text || '%' ESCAPE '\'
       OR lower(t.full_name) LIKE '%' || sqlc.narg('search_like')::text || '%' ESCAPE '\'
       OR lower(t.email) LIKE '%' || sqlc.narg('search_like')::text || '%' ESCAPE '\'
       OR lower(c.tenant_name) LIKE '%' || sqlc.narg('search_like')::text || '%' ESCAPE '\')
ORDER BY
  CASE WHEN @sort::text = 'created_at' THEN c.created_at END ASC,
  CASE WHEN @sort::text = '-created_at' THEN c.created_at END DESC,
  CASE WHEN @sort::text = 'starts_on' THEN c.starts_on END ASC,
  CASE WHEN @sort::text = '-starts_on' THEN c.starts_on END DESC,
  CASE WHEN @sort::text = 'ends_on' THEN c.ends_on END ASC NULLS LAST,
  CASE WHEN @sort::text = '-ends_on' THEN c.ends_on END DESC NULLS LAST,
  CASE WHEN @sort::text = '' THEN c.display_order END ASC,
  CASE WHEN @sort::text = '' THEN c.created_at END DESC,
  c.id DESC
LIMIT @page_limit OFFSET @page_offset;

-- name: CountContracts :one
SELECT count(*)::integer
FROM contracts c
JOIN rooms r ON r.id = c.room_id
LEFT JOIN users t ON t.id = c.tenant_id
WHERE c.deleted_at IS NULL
  AND ((@as_landlord::boolean AND c.landlord_id = @viewer_id AND c.confirmed_at IS NOT NULL AND r.deleted_at IS NULL)
       OR (NOT @as_landlord::boolean AND c.tenant_id = @viewer_id))
  AND (sqlc.narg('statuses')::text[] IS NULL OR c.status = ANY(sqlc.narg('statuses')::text[]))
  AND (sqlc.narg('room_id')::uuid IS NULL OR c.room_id = sqlc.narg('room_id')::uuid)
  AND (sqlc.narg('building_id')::uuid IS NULL OR r.building_id = sqlc.narg('building_id')::uuid)
  AND (NOT @no_building::boolean OR r.building_id IS NULL)
  AND (sqlc.narg('cancellation_status')::text IS NULL OR c.cancellation_status = sqlc.narg('cancellation_status')::text)
  AND (sqlc.narg('payment_state')::text IS NULL
       OR (sqlc.narg('payment_state')::text = 'due' AND EXISTS (SELECT 1 FROM invoices i WHERE i.contract_id = c.id AND i.status = 'sent'
               AND i.total_amount_vnd > coalesce((SELECT sum(p.amount_vnd) FROM payments p WHERE p.invoice_id = i.id AND p.status IN ('confirmed', 'pending')), 0)))
       OR (sqlc.narg('payment_state')::text = 'review' AND EXISTS (SELECT 1 FROM invoices i JOIN payments p ON p.invoice_id = i.id WHERE i.contract_id = c.id AND i.status = 'sent' AND p.status = 'pending'))
       OR (sqlc.narg('payment_state')::text = 'settled' AND NOT EXISTS (SELECT 1 FROM invoices i WHERE i.contract_id = c.id AND i.status = 'sent'
               AND i.total_amount_vnd > coalesce((SELECT sum(p.amount_vnd) FROM payments p WHERE p.invoice_id = i.id AND p.status IN ('confirmed', 'pending')), 0)) AND NOT EXISTS (SELECT 1 FROM invoices i JOIN payments p ON p.invoice_id = i.id WHERE i.contract_id = c.id AND i.status = 'sent' AND p.status = 'pending')))
  AND (NOT @has_unpaid_invoice::boolean
       OR EXISTS (SELECT 1 FROM invoices i WHERE i.contract_id = c.id AND i.status IN ('draft', 'sent')))
  AND (sqlc.narg('search_like')::text IS NULL
       OR lower(r.name) LIKE '%' || sqlc.narg('search_like')::text || '%' ESCAPE '\'
       OR lower(t.full_name) LIKE '%' || sqlc.narg('search_like')::text || '%' ESCAPE '\'
       OR lower(t.email) LIKE '%' || sqlc.narg('search_like')::text || '%' ESCAPE '\'
       OR lower(c.tenant_name) LIKE '%' || sqlc.narg('search_like')::text || '%' ESCAPE '\');

-- name: CountTenantContractsByStatus :many
-- How many contracts a tenant has in each status (the counters of the tabs of "my rooms").
SELECT status, count(*)::integer AS total
FROM contracts
WHERE tenant_id = @tenant_id AND deleted_at IS NULL
GROUP BY status;

-- name: GetContractForUpdate :one
SELECT id, room_id, landlord_id, tenant_id, tenant_name, tenant_phone, status, price_vnd, deposit_vnd, occupant_count,
       starts_on, ends_on, notes, confirmed_at, display_order, cancellation_status, cancellation_requested_at,
       cancellation_reason, cancellation_response_note
FROM contracts
WHERE id = @id AND deleted_at IS NULL
FOR UPDATE;

-- name: InsertContract :one
INSERT INTO contracts (room_id, landlord_id, tenant_id, tenant_name, tenant_phone, status, price_vnd, deposit_vnd,
                       occupant_count, starts_on, ends_on, notes, confirmed_at, display_order, cancellation_status,
                       cancellation_requested_at, cancellation_reason, cancellation_response_note)
VALUES (@room_id, @landlord_id, @tenant_id, @tenant_name, @tenant_phone, @status, @price_vnd, @deposit_vnd,
        @occupant_count, @starts_on, @ends_on, @notes, @confirmed_at, @display_order, @cancellation_status,
        @cancellation_requested_at, @cancellation_reason, @cancellation_response_note)
RETURNING id;

-- name: UpdateContract :exec
-- Writes every field a landlord or a tenant can change in a contract's life.
UPDATE contracts
SET tenant_name = @tenant_name, tenant_phone = @tenant_phone, status = @status, price_vnd = @price_vnd,
    deposit_vnd = @deposit_vnd, occupant_count = @occupant_count, starts_on = @starts_on, ends_on = @ends_on,
    notes = @notes, cancellation_status = @cancellation_status, cancellation_requested_at = @cancellation_requested_at,
    cancellation_reason = @cancellation_reason, cancellation_response_note = @cancellation_response_note,
    updated_at = now()
WHERE id = @id AND deleted_at IS NULL;

-- name: SoftDeleteContract :exec
UPDATE contracts SET deleted_at = now(), updated_at = now() WHERE id = @id AND deleted_at IS NULL;

-- name: CountActiveContractsOfRoom :one
-- The running contracts of a room: all of them, and those with a tenant behind them.
SELECT count(*)::integer AS total, (count(*) FILTER (WHERE tenant_id IS NOT NULL))::integer AS with_tenant
FROM contracts
WHERE room_id = @room_id AND status = 'active' AND deleted_at IS NULL;

-- name: OpenRegistrationExists :one
-- Whether the tenant already has a waiting or running contract on the room.
SELECT EXISTS (
    SELECT 1 FROM contracts
    WHERE room_id = @room_id AND tenant_id = @tenant_id AND status IN ('pending', 'active') AND deleted_at IS NULL
);

-- name: RejectOtherPendingContracts :many
-- Somebody got the room: the other waiting registrations can no longer be accepted.
UPDATE contracts SET status = 'rejected', updated_at = now()
WHERE room_id = @room_id AND status = 'pending' AND confirmed_at IS NOT NULL AND id <> @except_id AND deleted_at IS NULL
RETURNING id;

-- name: CloseBlankContracts :many
-- Ends the running contracts nobody holds on the rooms, today unless their own end date has passed already.
UPDATE contracts SET status = 'ended', ends_on = least(coalesce(ends_on, @today::date), @today::date), updated_at = now()
WHERE room_id = ANY(@room_ids::uuid[]) AND status = 'active' AND tenant_id IS NULL AND deleted_at IS NULL
RETURNING id, room_id, landlord_id;

-- name: MinContractOrder :one
-- A new contract goes above the landlord's others: one step before the smallest position in use.
SELECT coalesce(min(display_order), 0)::integer FROM contracts WHERE landlord_id = @landlord_id AND deleted_at IS NULL;

-- name: ListContractOrder :many
-- The contracts a landlord sorts (the confirmed ones on their live rooms), in their stored order, locked.
SELECT c.id, c.display_order
FROM contracts c
JOIN rooms r ON r.id = c.room_id
WHERE c.landlord_id = @landlord_id AND c.confirmed_at IS NOT NULL AND c.deleted_at IS NULL AND r.deleted_at IS NULL
ORDER BY c.display_order, c.created_at DESC, c.id DESC
FOR UPDATE OF c;

-- name: SetContractOrder :exec
UPDATE contracts c SET display_order = n.pos
FROM (SELECT unnest(@ids::uuid[]) AS id, unnest(@orders::integer[]) AS pos) AS n
WHERE c.id = n.id AND c.landlord_id = @landlord_id;

-- name: CountRunningContractsOfLandlord :one
-- Running contracts with a tenant on the landlord's rooms (a blank contract is the landlord's own bookkeeping).
SELECT count(*)::integer FROM contracts
WHERE landlord_id = @landlord_id AND status = 'active' AND tenant_id IS NOT NULL AND deleted_at IS NULL;

-- name: CountPendingRegistrationsOfTenant :one
SELECT count(*)::integer FROM contracts WHERE tenant_id = @tenant_id AND status = 'pending' AND deleted_at IS NULL;

-- name: ListRoomsWithRunningTenantContract :many
-- Which of the rooms have a running contract with a tenant (that is what locks a room).
SELECT DISTINCT room_id FROM contracts
WHERE room_id = ANY(@room_ids::uuid[]) AND status = 'active' AND tenant_id IS NOT NULL AND deleted_at IS NULL;

-- name: LockLandlordContracts :exec
-- Serialises the transactions that count a landlord's running contracts against a plan limit.
SELECT pg_advisory_xact_lock(hashtextextended('contract:' || @landlord_id::text, 0));

-- name: CountContractsOfTenantOnLandlord :one
-- Used by the demo seed to know whether the tenant already has demo contracts.
SELECT count(*)::integer FROM contracts WHERE tenant_id = @tenant_id AND landlord_id = @landlord_id AND deleted_at IS NULL;

-- name: CountTenantContractsByPaymentState :one
-- How many of the tenant's contracts in the given statuses (the running ones) are in each state of money: due (a sent bill
-- has money left to pay), waiting for review (a payment on a sent bill waits for the landlord), or settled (neither). A
-- contract can be both due and waiting. Read only: the invoice and payment contexts own the bills and the payments.
SELECT count(*)::integer AS total,
       (count(*) FILTER (WHERE s.due))::integer AS due,
       (count(*) FILTER (WHERE s.review))::integer AS review,
       (count(*) FILTER (WHERE NOT s.due AND NOT s.review))::integer AS settled
FROM (
    SELECT EXISTS (SELECT 1 FROM invoices i WHERE i.contract_id = c.id AND i.status = 'sent'
               AND i.total_amount_vnd > coalesce((SELECT sum(p.amount_vnd) FROM payments p WHERE p.invoice_id = i.id AND p.status IN ('confirmed', 'pending')), 0)) AS due,
           EXISTS (SELECT 1 FROM invoices i JOIN payments p ON p.invoice_id = i.id WHERE i.contract_id = c.id AND i.status = 'sent' AND p.status = 'pending') AS review
    FROM contracts c
    WHERE c.tenant_id = @tenant_id AND c.status = ANY(@statuses::text[]) AND c.deleted_at IS NULL
) s;
