-- name: SearchInvoiceFactsForPayment :many
-- The facts about bills that payments need. scoped = false is for the system (a lock already taken, the seed). A
-- landlord (as_landlord) sees the bills of the confirmed contracts on their own live rooms; a tenant the bills sent to
-- their account (never a draft, never one passed on by link).
SELECT i.id, i.contract_id, i.invoice_number, i.name, i.payment_code, i.total_amount_vnd, i.status, i.due_at,
       c.room_id, c.landlord_id, c.tenant_id, r.name AS room_name
FROM invoices i
JOIN contracts c ON c.id = i.contract_id
JOIN rooms r ON r.id = c.room_id
WHERE c.deleted_at IS NULL
  AND (NOT @scoped::boolean
       OR (@as_landlord::boolean AND c.landlord_id = @viewer_id AND c.confirmed_at IS NOT NULL AND r.deleted_at IS NULL)
       OR (NOT @as_landlord::boolean AND c.tenant_id = @viewer_id AND i.status <> 'draft' AND i.delivery = 'app'))
  AND (sqlc.narg('id')::uuid IS NULL OR i.id = sqlc.narg('id')::uuid)
  AND (sqlc.narg('landlord_id')::uuid IS NULL OR c.landlord_id = sqlc.narg('landlord_id')::uuid)
  AND (sqlc.narg('room_name')::text IS NULL OR lower(r.name) = lower(sqlc.narg('room_name')::text))
  AND (sqlc.narg('status')::text IS NULL OR i.status = sqlc.narg('status')::text)
ORDER BY i.period_start, i.id;

-- name: LockInvoiceForPayment :one
SELECT id FROM invoices WHERE id = @id FOR UPDATE;
