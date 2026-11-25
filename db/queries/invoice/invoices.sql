-- name: SearchInvoices :many
-- The bills a viewer sees, with their contract, room and people. scoped = false is for the system (a share link, the
-- daily commands): no viewer. A landlord (as_landlord) sees the bills of the confirmed contracts on their own live
-- rooms; a tenant sees the bills sent to their account (never a draft, never one passed on by link). Filters are
-- optional; a NULL filter is off. The newest period comes first.
SELECT i.id, i.contract_id, i.created_by_id, i.invoice_number, i.name, i.payment_label, i.payment_code, i.period_start,
       i.period_end, i.total_amount_vnd, i.due_at, i.status, i.sent_at, i.sent_via, i.sent_by_id, i.paid_at, i.note,
       i.delivery, i.share_token_hash, i.share_token_sealed, i.share_expires_at, i.created_at, i.updated_at,
       c.room_id, c.landlord_id, c.tenant_id, c.tenant_name, c.tenant_phone, c.status AS contract_status,
       c.price_vnd AS contract_price_vnd, c.occupant_count, c.starts_on, c.ends_on,
       r.name AS room_name, r.rental_period_unit AS room_rental_period_unit,
       l.full_name AS landlord_full_name, t.full_name AS tenant_full_name, t.email AS tenant_email,
       cb.full_name AS created_by_name
FROM invoices i
JOIN contracts c ON c.id = i.contract_id
JOIN rooms r ON r.id = c.room_id
JOIN users l ON l.id = c.landlord_id
LEFT JOIN users t ON t.id = c.tenant_id
LEFT JOIN users cb ON cb.id = i.created_by_id
WHERE c.deleted_at IS NULL
  AND (NOT @scoped::boolean
       OR (@as_landlord::boolean AND c.landlord_id = @viewer_id AND c.confirmed_at IS NOT NULL AND r.deleted_at IS NULL)
       OR (@as_manager::boolean AND c.tenant_id IS DISTINCT FROM @viewer_id AND r.deleted_at IS NULL
            AND EXISTS (SELECT 1 FROM room_managers rm WHERE rm.room_id = c.room_id AND rm.manager_id = @viewer_id AND rm.status = 'active'))
       OR (NOT @as_landlord::boolean AND NOT @as_manager::boolean AND c.tenant_id = @viewer_id AND i.status <> 'draft' AND i.delivery = 'app'))
  AND (sqlc.narg('id')::uuid IS NULL OR i.id = sqlc.narg('id')::uuid)
  AND (sqlc.narg('contract_id')::uuid IS NULL OR i.contract_id = sqlc.narg('contract_id')::uuid)
  AND (sqlc.narg('share_hash')::text IS NULL OR i.share_token_hash = sqlc.narg('share_hash')::text)
ORDER BY i.period_start DESC, i.id DESC
LIMIT @page_limit OFFSET @page_offset;

-- name: CountInvoices :one
SELECT count(*)::integer
FROM invoices i
JOIN contracts c ON c.id = i.contract_id
JOIN rooms r ON r.id = c.room_id
WHERE c.deleted_at IS NULL
  AND ((@as_landlord::boolean AND c.landlord_id = @viewer_id AND c.confirmed_at IS NOT NULL AND r.deleted_at IS NULL)
       OR (@as_manager::boolean AND c.tenant_id IS DISTINCT FROM @viewer_id AND r.deleted_at IS NULL
            AND EXISTS (SELECT 1 FROM room_managers rm WHERE rm.room_id = c.room_id AND rm.manager_id = @viewer_id AND rm.status = 'active'))
       OR (NOT @as_landlord::boolean AND NOT @as_manager::boolean AND c.tenant_id = @viewer_id AND i.status <> 'draft' AND i.delivery = 'app'))
  AND i.contract_id = @contract_id;

-- name: LockInvoice :one
-- Locks a bill the viewer sees until the transaction ends (same scope as SearchInvoices).
SELECT i.id
FROM invoices i
JOIN contracts c ON c.id = i.contract_id
JOIN rooms r ON r.id = c.room_id
WHERE i.id = @id AND c.deleted_at IS NULL
  AND ((@as_landlord::boolean AND c.landlord_id = @viewer_id AND c.confirmed_at IS NOT NULL AND r.deleted_at IS NULL)
       OR (@as_manager::boolean AND c.tenant_id IS DISTINCT FROM @viewer_id AND r.deleted_at IS NULL
            AND EXISTS (SELECT 1 FROM room_managers rm WHERE rm.room_id = c.room_id AND rm.manager_id = @viewer_id AND rm.status = 'active'))
       OR (NOT @as_landlord::boolean AND NOT @as_manager::boolean AND c.tenant_id = @viewer_id AND i.status <> 'draft' AND i.delivery = 'app'))
FOR UPDATE OF i;

-- name: ListDueInvoices :many
-- Sent bills whose due date falls in one of the two windows and that have not had that reminder yet; the keyset is the
-- id. Same columns as SearchInvoices.
SELECT i.id, i.contract_id, i.created_by_id, i.invoice_number, i.name, i.payment_label, i.payment_code, i.period_start,
       i.period_end, i.total_amount_vnd, i.due_at, i.status, i.sent_at, i.sent_via, i.sent_by_id, i.paid_at, i.note,
       i.delivery, i.share_token_hash, i.share_token_sealed, i.share_expires_at, i.created_at, i.updated_at,
       c.room_id, c.landlord_id, c.tenant_id, c.tenant_name, c.tenant_phone, c.status AS contract_status,
       c.price_vnd AS contract_price_vnd, c.occupant_count, c.starts_on, c.ends_on,
       r.name AS room_name, r.rental_period_unit AS room_rental_period_unit,
       l.full_name AS landlord_full_name, t.full_name AS tenant_full_name, t.email AS tenant_email,
       cb.full_name AS created_by_name
FROM invoices i
JOIN contracts c ON c.id = i.contract_id
JOIN rooms r ON r.id = c.room_id
JOIN users l ON l.id = c.landlord_id
LEFT JOIN users t ON t.id = c.tenant_id
LEFT JOIN users cb ON cb.id = i.created_by_id
WHERE i.status = 'sent' AND c.deleted_at IS NULL AND r.deleted_at IS NULL AND i.id > @after_id
  AND ((i.due_at BETWEEN @soon_from AND @soon_to AND i.due_soon_reminded_on IS NULL)
       OR (i.due_at BETWEEN @overdue_from AND @overdue_to AND i.overdue_reminded_on IS NULL))
ORDER BY i.id
LIMIT @page_limit;

-- name: MarkDueSoonReminded :exec
UPDATE invoices SET due_soon_reminded_on = @reminded_on WHERE id = ANY (@ids::uuid[]);

-- name: MarkOverdueReminded :exec
UPDATE invoices SET overdue_reminded_on = @reminded_on WHERE id = ANY (@ids::uuid[]);

-- name: InsertInvoice :one
INSERT INTO invoices (contract_id, created_by_id, invoice_number, name, payment_label, payment_code, period_start, period_end,
                      total_amount_vnd, due_at, status, sent_at, sent_via, sent_by_id, paid_at, note, delivery)
VALUES (@contract_id, @created_by_id, @invoice_number, @name, @payment_label, @payment_code, @period_start, @period_end,
        @total_amount_vnd, @due_at, @status, @sent_at, @sent_via, @sent_by_id, @paid_at, @note, @delivery)
RETURNING id;

-- name: UpdateInvoiceContent :exec
-- What a correction changes. A moved due date clears the reminders already sent.
UPDATE invoices
SET name = @name, payment_label = @payment_label, payment_code = @payment_code, total_amount_vnd = @total_amount_vnd,
    note = @note,
    due_soon_reminded_on = CASE WHEN due_at = @due_at THEN due_soon_reminded_on END,
    overdue_reminded_on = CASE WHEN due_at = @due_at THEN overdue_reminded_on END,
    due_at = @due_at, updated_at = now()
WHERE id = @id;

-- name: UpdateInvoiceState :exec
UPDATE invoices
SET status = @status, sent_at = @sent_at, sent_via = @sent_via, sent_by_id = @sent_by_id, paid_at = @paid_at,
    delivery = @delivery, updated_at = now()
WHERE id = @id;

-- name: UpdateInvoiceShare :exec
UPDATE invoices
SET share_token_hash = @share_token_hash, share_token_sealed = @share_token_sealed, share_expires_at = @share_expires_at,
    updated_at = now()
WHERE id = @id;

-- name: DeleteInvoice :exec
DELETE FROM invoices WHERE id = @id;

-- name: InsertInvoiceItems :exec
INSERT INTO invoice_items (invoice_id, kind, description, quantity, unit_price_vnd, amount_vnd, display_order)
SELECT @invoice_id, i.kind, i.description, i.quantity::numeric(12,2), i.unit_price_vnd, i.amount_vnd, i.display_order
FROM (SELECT unnest(@kinds::text[]) AS kind, unnest(@descriptions::text[]) AS description,
             unnest(@quantities::float8[]) AS quantity, unnest(@unit_prices::bigint[]) AS unit_price_vnd,
             unnest(@amounts::bigint[]) AS amount_vnd, unnest(@orders::smallint[]) AS display_order) AS i;

-- name: DeleteInvoiceItems :exec
DELETE FROM invoice_items WHERE invoice_id = @invoice_id;

-- name: ListItemsOfInvoices :many
SELECT id, invoice_id, kind, description, quantity, unit_price_vnd, amount_vnd, display_order
FROM invoice_items
WHERE invoice_id = ANY (@invoice_ids::uuid[])
ORDER BY invoice_id, display_order;

-- name: ListBilledPeriods :many
SELECT id, period_start, invoice_number, name, sent_at, sent_via
FROM invoices
WHERE contract_id = @contract_id
ORDER BY period_start;

-- name: CountInvoicesOfContract :one
SELECT count(*)::integer FROM invoices WHERE contract_id = @contract_id;

-- name: ListInvoiceMonthCounts :many
-- How many bills the landlord has per month of their period start (the number a new bill takes in its month).
SELECT to_char(i.period_start, 'YYYY-MM')::text AS month, count(*)::integer AS total
FROM invoices i
JOIN contracts c ON c.id = i.contract_id
WHERE c.landlord_id = @landlord_id AND i.period_start BETWEEN @from_date AND @to_date
GROUP BY 1;

-- name: InvoiceNumberTaken :one
SELECT EXISTS (SELECT 1 FROM invoices WHERE invoice_number = @invoice_number);

-- name: PaymentCodeTaken :one
SELECT EXISTS (SELECT 1 FROM invoices WHERE payment_code = @payment_code AND id <> @except_id);
