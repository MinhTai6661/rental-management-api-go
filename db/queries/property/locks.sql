-- name: LockLandlord :exec
-- Serialises the changes that count a landlord's buildings, rooms or pictures against a plan limit.
SELECT pg_advisory_xact_lock(hashtextextended('property:' || @landlord_id::text, 0));
