-- Queries of the system settings and the legal documents.

-- name: AdminListAppSettings :many
SELECT key, value FROM app_settings ORDER BY key;

-- name: AdminUpsertAppSetting :exec
INSERT INTO app_settings (key, value, updated_by_id, created_at, updated_at)
VALUES (@key, @value, @updated_by_id, @now, @now)
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_by_id = EXCLUDED.updated_by_id, updated_at = EXCLUDED.updated_at;

-- name: AdminDeleteAppSetting :exec
DELETE FROM app_settings WHERE key = $1;

-- name: AdminInsertLegalDocument :exec
INSERT INTO legal_documents (id, kind, version, content, change_summary, published_by_id, created_at)
VALUES (@id, @kind, @version, @content, @change_summary, @published_by_id, @now);

-- name: AdminLatestLegalDocument :one
SELECT d.id, d.kind, d.version, d.content, d.change_summary, d.published_by_id, d.created_at, COALESCE(u.full_name, '')::text AS published_by_name
FROM legal_documents d LEFT JOIN users u ON u.id = d.published_by_id
WHERE d.kind = $1
ORDER BY d.created_at DESC, d.id DESC
LIMIT 1;

-- name: AdminLatestLegalVersion :one
-- The version people must accept: the newest document of any kind.
SELECT version FROM legal_documents ORDER BY created_at DESC, id DESC LIMIT 1;

-- name: AdminGetLegalDocument :one
SELECT d.id, d.kind, d.version, d.content, d.change_summary, d.published_by_id, d.created_at, COALESCE(u.full_name, '')::text AS published_by_name
FROM legal_documents d LEFT JOIN users u ON u.id = d.published_by_id
WHERE d.id = $1 AND d.kind = $2;

-- name: AdminListLegalDocuments :many
SELECT d.id, d.kind, d.version, d.content, d.change_summary, d.published_by_id, d.created_at, COALESCE(u.full_name, '')::text AS published_by_name
FROM legal_documents d LEFT JOIN users u ON u.id = d.published_by_id
WHERE d.kind = @kind
ORDER BY d.created_at DESC, d.id DESC
LIMIT @page_limit OFFSET @page_offset;

-- name: AdminCountLegalDocuments :one
SELECT count(*)::bigint FROM legal_documents WHERE kind = $1;

-- name: AdminListLegalDocumentsAfter :many
SELECT d.id, d.kind, d.version, d.content, d.change_summary, d.published_by_id, d.created_at, ''::text AS published_by_name
FROM legal_documents d
WHERE d.created_at > @since::timestamptz
ORDER BY d.created_at DESC, d.id DESC
LIMIT @page_limit;

-- name: AdminLatestLegalDocumentPerKind :many
SELECT DISTINCT ON (d.kind) d.id, d.kind, d.version, d.content, d.change_summary, d.published_by_id, d.created_at, ''::text AS published_by_name
FROM legal_documents d
ORDER BY d.kind, d.created_at DESC, d.id DESC;

-- name: AdminGetUserTerms :one
SELECT terms_accepted_at FROM users WHERE id = $1 AND deleted_at IS NULL;

-- name: AdminSetUserTerms :exec
UPDATE users SET terms_version = @version, terms_accepted_at = @now::timestamptz, updated_at = @now::timestamptz WHERE id = @id;
