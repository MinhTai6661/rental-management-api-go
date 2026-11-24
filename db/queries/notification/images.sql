-- name: ListAnnouncementImages :many
-- The pictures of the popups, each popup's in order.
SELECT * FROM announcement_images WHERE announcement_id = ANY(@announcement_ids::uuid[]) ORDER BY announcement_id, display_order, id;

-- name: InsertAnnouncementImage :one
INSERT INTO announcement_images (announcement_id, file_key, content_type, width, height, size_bytes, link_url, display_order)
VALUES (@announcement_id::uuid, @file_key::text, @content_type::text, @width::integer, @height::integer, @size_bytes::integer,
        sqlc.narg(link_url)::text, @display_order::smallint)
RETURNING *;

-- name: UpdateAnnouncementImage :execrows
UPDATE announcement_images SET display_order = @display_order::smallint, link_url = sqlc.narg(link_url)::text
WHERE id = @id AND announcement_id = @announcement_id;

-- name: DeleteAnnouncementImage :execrows
DELETE FROM announcement_images WHERE id = @id AND announcement_id = @announcement_id;
