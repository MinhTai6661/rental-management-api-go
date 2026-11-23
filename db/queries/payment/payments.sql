-- name: SearchPayments :many
-- The payments a viewer sees, with their bill, its contract and room, and who paid. A landlord (as_landlord) sees the
-- payments on the bills of the confirmed contracts on their own live rooms; a tenant the payments they made. Filters are
-- optional; a NULL filter is off. A list of the payments of one bill (invoice_id, the caller has checked the viewer sees
-- the bill) is oldest first (oldest_first); the others have the newest first.
SELECT p.id, p.invoice_id, p.payer_id, p.method, p.amount_vnd, p.status, p.note, p.proof_keys, p.external_reference,
       p.reviewed_by_id, p.reviewed_at, p.review_note, p.recorded_by_id, p.paid_at, p.created_at, p.updated_at,
       i.contract_id, i.invoice_number, i.name AS invoice_name, i.payment_code, i.total_amount_vnd,
       i.status AS invoice_status, i.due_at,
       c.room_id, c.landlord_id, c.tenant_id, r.name AS room_name,
       u.full_name AS payer_full_name, u.email AS payer_email
FROM payments p
JOIN invoices i ON i.id = p.invoice_id
JOIN contracts c ON c.id = i.contract_id
JOIN rooms r ON r.id = c.room_id
LEFT JOIN users u ON u.id = p.payer_id
WHERE c.deleted_at IS NULL
  AND (NOT @scoped::boolean
       OR (@as_landlord::boolean AND c.landlord_id = @viewer_id AND c.confirmed_at IS NOT NULL AND r.deleted_at IS NULL)
       OR (NOT @as_landlord::boolean AND p.payer_id = @viewer_id))
  AND (sqlc.narg('id')::uuid IS NULL OR p.id = sqlc.narg('id')::uuid)
  AND (sqlc.narg('invoice_id')::uuid IS NULL OR p.invoice_id = sqlc.narg('invoice_id')::uuid)
  AND (sqlc.narg('status')::text IS NULL OR p.status = sqlc.narg('status')::text)
ORDER BY
  CASE WHEN @oldest_first::boolean THEN p.created_at END ASC,
  CASE WHEN @oldest_first::boolean THEN p.id END ASC,
  p.created_at DESC, p.id DESC
LIMIT @page_limit OFFSET @page_offset;

-- name: CountPayments :one
SELECT count(*)::integer
FROM payments p
JOIN invoices i ON i.id = p.invoice_id
JOIN contracts c ON c.id = i.contract_id
JOIN rooms r ON r.id = c.room_id
WHERE c.deleted_at IS NULL
  AND (NOT @scoped::boolean
       OR (@as_landlord::boolean AND c.landlord_id = @viewer_id AND c.confirmed_at IS NOT NULL AND r.deleted_at IS NULL)
       OR (NOT @as_landlord::boolean AND p.payer_id = @viewer_id))
  AND (sqlc.narg('invoice_id')::uuid IS NULL OR p.invoice_id = sqlc.narg('invoice_id')::uuid)
  AND (sqlc.narg('status')::text IS NULL OR p.status = sqlc.narg('status')::text);

-- name: LockPayment :one
SELECT id, invoice_id, payer_id, method, amount_vnd, status, note, proof_keys, external_reference, reviewed_by_id,
       reviewed_at, review_note, recorded_by_id, paid_at, created_at, updated_at
FROM payments
WHERE id = @id
FOR UPDATE;

-- name: GetPaymentProofKeys :one
SELECT proof_keys FROM payments WHERE id = @id;

-- name: InsertPayment :one
INSERT INTO payments (invoice_id, payer_id, method, amount_vnd, status, note, proof_keys, external_reference,
                      reviewed_by_id, reviewed_at, review_note, recorded_by_id, paid_at)
VALUES (@invoice_id, @payer_id, @method, @amount_vnd, @status, @note, @proof_keys, @external_reference,
        @reviewed_by_id, @reviewed_at, @review_note, @recorded_by_id, @paid_at)
RETURNING id;

-- name: UpdatePaymentReview :exec
UPDATE payments
SET status = @status, reviewed_by_id = @reviewed_by_id, reviewed_at = @reviewed_at, review_note = @review_note,
    paid_at = @paid_at, updated_at = now()
WHERE id = @id;

-- name: DeletePayment :exec
DELETE FROM payments WHERE id = @id;

-- name: SumPaymentsOfInvoices :many
-- The confirmed and the pending money of each bill (a bill with no such payment is left out).
SELECT invoice_id,
       coalesce(sum(amount_vnd) FILTER (WHERE status = 'confirmed'), 0)::bigint AS confirmed_vnd,
       coalesce(sum(amount_vnd) FILTER (WHERE status = 'pending'), 0)::bigint AS pending_vnd
FROM payments
WHERE invoice_id = ANY(@invoice_ids::uuid[]) AND status IN ('confirmed', 'pending')
GROUP BY invoice_id;

-- name: VoidConfirmedPaymentsOfInvoice :exec
UPDATE payments SET status = 'voided', updated_at = now() WHERE invoice_id = @invoice_id AND status = 'confirmed';

-- name: DeletePaymentsOfInvoice :many
DELETE FROM payments WHERE invoice_id = @invoice_id RETURNING proof_keys;
