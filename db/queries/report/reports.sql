-- name: InsertReport :one
INSERT INTO reports (reporter_id, kind, room_id, subject, body, created_at, updated_at)
VALUES (@reporter_id, @kind, sqlc.narg('room_id'), @subject, @body, @at::timestamptz, @at::timestamptz)
RETURNING id;

-- name: GetReportDetail :one
SELECT * FROM report_details WHERE id = @id;

-- name: LockReport :one
-- The report with its owner, its row locked until the transaction ends (the joins are not).
SELECT rep.id, rep.reporter_id, rep.kind, rep.room_id, rm.landlord_id AS room_landlord_id,
       rep.subject, rep.body, rep.status, rep.created_at, rep.deleted_at
FROM reports rep
LEFT JOIN rooms rm ON rm.id = rep.room_id
WHERE rep.id = @id
FOR UPDATE OF rep;

-- name: SetReportStatus :exec
UPDATE reports SET status = @status, updated_at = @at::timestamptz WHERE id = @id;

-- name: SoftDeleteReport :exec
UPDATE reports SET deleted_at = @at::timestamptz, updated_at = @at::timestamptz WHERE id = @id AND deleted_at IS NULL;

-- name: ListMyReports :many
-- The author's reports that are not deleted, newest first.
SELECT * FROM report_details d
WHERE d.reporter_id = @author_id AND d.deleted_at IS NULL
  AND (sqlc.narg('status')::text IS NULL OR d.status = sqlc.narg('status')::text)
  AND (sqlc.narg('kind')::text IS NULL OR d.kind = sqlc.narg('kind')::text)
  AND (sqlc.narg('search')::text IS NULL OR lower(d.subject) LIKE '%' || sqlc.narg('search')::text || '%' ESCAPE '\'
       OR lower(d.body) LIKE '%' || sqlc.narg('search')::text || '%' ESCAPE '\')
ORDER BY d.created_at DESC, d.id DESC
LIMIT @page_limit OFFSET @page_offset;

-- name: CountMyReports :one
SELECT count(*)::integer FROM report_details d
WHERE d.reporter_id = @author_id AND d.deleted_at IS NULL
  AND (sqlc.narg('status')::text IS NULL OR d.status = sqlc.narg('status')::text)
  AND (sqlc.narg('kind')::text IS NULL OR d.kind = sqlc.narg('kind')::text)
  AND (sqlc.narg('search')::text IS NULL OR lower(d.subject) LIKE '%' || sqlc.narg('search')::text || '%' ESCAPE '\'
       OR lower(d.body) LIKE '%' || sqlc.narg('search')::text || '%' ESCAPE '\');

-- name: ListReportInbox :many
-- The reports somebody has to answer (deleted ones too, they stay as history), newest first: everything, every
-- feature report, and/or the room reports of one landlord's rooms.
SELECT * FROM report_details d
WHERE (@everything::boolean
       OR (@features::boolean AND d.kind = 'feature')
       OR (sqlc.narg('landlord_id')::uuid IS NOT NULL AND d.kind = 'room' AND d.room_landlord_id = sqlc.narg('landlord_id')::uuid))
  AND (sqlc.narg('status')::text IS NULL OR d.status = sqlc.narg('status')::text)
  AND (sqlc.narg('kind')::text IS NULL OR d.kind = sqlc.narg('kind')::text)
  AND (sqlc.narg('search')::text IS NULL OR lower(d.subject) LIKE '%' || sqlc.narg('search')::text || '%' ESCAPE '\'
       OR lower(d.body) LIKE '%' || sqlc.narg('search')::text || '%' ESCAPE '\')
ORDER BY d.created_at DESC, d.id DESC
LIMIT @page_limit OFFSET @page_offset;

-- name: CountReportInbox :one
SELECT count(*)::integer FROM report_details d
WHERE (@everything::boolean
       OR (@features::boolean AND d.kind = 'feature')
       OR (sqlc.narg('landlord_id')::uuid IS NOT NULL AND d.kind = 'room' AND d.room_landlord_id = sqlc.narg('landlord_id')::uuid))
  AND (sqlc.narg('status')::text IS NULL OR d.status = sqlc.narg('status')::text)
  AND (sqlc.narg('kind')::text IS NULL OR d.kind = sqlc.narg('kind')::text)
  AND (sqlc.narg('search')::text IS NULL OR lower(d.subject) LIKE '%' || sqlc.narg('search')::text || '%' ESCAPE '\'
       OR lower(d.body) LIKE '%' || sqlc.narg('search')::text || '%' ESCAPE '\');

-- name: InsertReportReply :one
INSERT INTO report_replies (report_id, author_id, body, created_at)
VALUES (@report_id, @author_id, @body, @at::timestamptz)
RETURNING id;

-- name: GetReportReplyDetail :one
SELECT * FROM report_reply_details WHERE id = @id;

-- name: ListReportReplyDetails :many
-- The conversation, newest message first.
SELECT * FROM report_reply_details d WHERE d.report_id = @report_id
ORDER BY d.created_at DESC, d.id DESC
LIMIT @page_limit OFFSET @page_offset;

-- name: CountReportReplies :one
SELECT count(*)::integer FROM report_replies WHERE report_id = @report_id;

-- name: GetRoomRentedByTenant :one
-- The room, when the tenant rents it or rented it (a running or ended contract).
SELECT r.id, r.name, r.landlord_id FROM rooms r
WHERE r.id = @room_id
  AND EXISTS (SELECT 1 FROM contracts c WHERE c.room_id = r.id AND c.tenant_id = @tenant_id
              AND c.status IN ('active', 'ended') AND c.deleted_at IS NULL);

-- name: GetFirstRoomRentedFromLandlord :one
SELECT r.id, r.name, r.landlord_id FROM rooms r
WHERE r.landlord_id = @landlord_id
  AND EXISTS (SELECT 1 FROM contracts c WHERE c.room_id = r.id AND c.tenant_id = @tenant_id
              AND c.status IN ('active', 'ended') AND c.deleted_at IS NULL)
ORDER BY r.created_at, r.id LIMIT 1;
