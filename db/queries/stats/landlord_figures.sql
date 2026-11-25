-- Figures of one landlord's rooms. Every query takes @landlord_id and an optional @building_id; the invoices of a deleted
-- contract and of a deleted room's past are left in (a room that was deleted still earned), the deleted contracts are not.
-- Months are cut in the zone the people read dates in (Asia/Ho_Chi_Minh).

-- name: StatsRooms :many
-- Every room of the landlord that was still there at @from_day or later (the deleted ones included: they had days).
SELECT r.id, r.building_id, b.name AS building_name, r.name, r.status, r.created_at, r.deleted_at
FROM rooms r
LEFT JOIN buildings b ON b.id = r.building_id
WHERE r.landlord_id = @landlord_id
  AND (sqlc.narg('building_id')::uuid IS NULL OR r.building_id = sqlc.narg('building_id')::uuid)
  AND (r.deleted_at IS NULL OR r.deleted_at >= @from_at::timestamptz)
ORDER BY r.id;

-- name: StatsRentedSpans :many
-- The days each room was lived in: contracts that began and have not been rejected, inside the window.
SELECT c.room_id, c.starts_on, c.ends_on, c.status
FROM contracts c
JOIN rooms r ON r.id = c.room_id
WHERE r.landlord_id = @landlord_id
  AND (sqlc.narg('building_id')::uuid IS NULL OR r.building_id = sqlc.narg('building_id')::uuid)
  AND c.deleted_at IS NULL
  AND c.status IN ('active', 'ended')
  AND c.starts_on <= @to_day::date
  AND (c.ends_on IS NULL OR c.ends_on >= @from_day::date);

-- name: StatsContractCounts :one
SELECT count(*) FILTER (WHERE c.status = 'active')::int AS active,
       count(*) FILTER (WHERE c.status = 'pending')::int AS pending,
       count(*) FILTER (WHERE c.status = 'active' AND c.ends_on BETWEEN @today::date AND @soon::date)::int AS expiring_soon
FROM contracts c
JOIN rooms r ON r.id = c.room_id
WHERE r.landlord_id = @landlord_id
  AND (sqlc.narg('building_id')::uuid IS NULL OR r.building_id = sqlc.narg('building_id')::uuid)
  AND r.deleted_at IS NULL
  AND c.deleted_at IS NULL;

-- name: StatsBilledByMonth :many
-- Billed = invoices sent or paid (a draft is not billed yet), by the month the period starts.
SELECT to_char(i.period_start, 'YYYY-MM')::text AS month, COALESCE(sum(i.total_amount_vnd), 0)::bigint AS amount_vnd
FROM invoices i
JOIN contracts c ON c.id = i.contract_id AND c.deleted_at IS NULL
JOIN rooms r ON r.id = c.room_id
WHERE r.landlord_id = @landlord_id
  AND (sqlc.narg('building_id')::uuid IS NULL OR r.building_id = sqlc.narg('building_id')::uuid)
  AND i.status IN ('sent', 'paid')
  AND i.period_start BETWEEN @from_day::date AND @to_day::date
GROUP BY 1;

-- name: StatsCollectedByMonth :many
-- Collected = confirmed payments (not the ones awaiting review), by the month they were paid.
SELECT to_char(p.paid_at AT TIME ZONE 'Asia/Ho_Chi_Minh', 'YYYY-MM')::text AS month, COALESCE(sum(p.amount_vnd), 0)::bigint AS amount_vnd
FROM payments p
JOIN invoices i ON i.id = p.invoice_id
JOIN contracts c ON c.id = i.contract_id AND c.deleted_at IS NULL
JOIN rooms r ON r.id = c.room_id
WHERE r.landlord_id = @landlord_id
  AND (sqlc.narg('building_id')::uuid IS NULL OR r.building_id = sqlc.narg('building_id')::uuid)
  AND p.status = 'confirmed'
  AND p.paid_at >= @from_at::timestamptz AND p.paid_at < @before_at::timestamptz
GROUP BY 1;

-- name: StatsOutstandingByMonth :many
-- Still to collect = what the sent invoices of the month lack of their confirmed payments.
SELECT to_char(i.period_start, 'YYYY-MM')::text AS month,
       COALESCE(sum(GREATEST(i.total_amount_vnd - COALESCE(paid.amount_vnd, 0), 0)), 0)::bigint AS amount_vnd
FROM invoices i
JOIN contracts c ON c.id = i.contract_id AND c.deleted_at IS NULL
JOIN rooms r ON r.id = c.room_id
LEFT JOIN LATERAL (SELECT sum(p.amount_vnd) AS amount_vnd FROM payments p WHERE p.invoice_id = i.id AND p.status = 'confirmed') paid ON true
WHERE r.landlord_id = @landlord_id
  AND (sqlc.narg('building_id')::uuid IS NULL OR r.building_id = sqlc.narg('building_id')::uuid)
  AND i.status = 'sent'
  AND i.period_start BETWEEN @from_day::date AND @to_day::date
GROUP BY 1;

-- name: StatsOwed :one
-- What is owed now, whatever the month: the sent invoices' shortfall, and the part past its due date.
SELECT COALESCE(sum(rest.remaining), 0)::bigint AS outstanding_vnd,
       COALESCE(sum(rest.remaining) FILTER (WHERE rest.due_at < @now::timestamptz), 0)::bigint AS overdue_vnd,
       count(*) FILTER (WHERE rest.due_at < @now::timestamptz)::int AS overdue_count
FROM (
    SELECT i.due_at, GREATEST(i.total_amount_vnd - COALESCE(paid.amount_vnd, 0), 0) AS remaining
    FROM invoices i
    JOIN contracts c ON c.id = i.contract_id AND c.deleted_at IS NULL
    JOIN rooms r ON r.id = c.room_id
    LEFT JOIN LATERAL (SELECT sum(p.amount_vnd) AS amount_vnd FROM payments p WHERE p.invoice_id = i.id AND p.status = 'confirmed') paid ON true
    WHERE r.landlord_id = @landlord_id
      AND (sqlc.narg('building_id')::uuid IS NULL OR r.building_id = sqlc.narg('building_id')::uuid)
      AND i.status = 'sent'
) rest;

-- name: StatsBilledByRoom :many
SELECT r.id AS room_id, COALESCE(sum(i.total_amount_vnd), 0)::bigint AS amount_vnd
FROM invoices i
JOIN contracts c ON c.id = i.contract_id AND c.deleted_at IS NULL
JOIN rooms r ON r.id = c.room_id
WHERE r.landlord_id = @landlord_id
  AND (sqlc.narg('building_id')::uuid IS NULL OR r.building_id = sqlc.narg('building_id')::uuid)
  AND i.status IN ('sent', 'paid')
  AND i.period_start BETWEEN @from_day::date AND @to_day::date
GROUP BY r.id;

-- name: StatsCollectedByRoom :many
SELECT r.id AS room_id, COALESCE(sum(p.amount_vnd), 0)::bigint AS amount_vnd
FROM payments p
JOIN invoices i ON i.id = p.invoice_id
JOIN contracts c ON c.id = i.contract_id AND c.deleted_at IS NULL
JOIN rooms r ON r.id = c.room_id
WHERE r.landlord_id = @landlord_id
  AND (sqlc.narg('building_id')::uuid IS NULL OR r.building_id = sqlc.narg('building_id')::uuid)
  AND p.status = 'confirmed'
  AND p.paid_at >= @from_at::timestamptz AND p.paid_at < @before_at::timestamptz
GROUP BY r.id;

-- name: StatsUtilitiesByMonth :many
-- Electricity and water read in the window: what was used and what it cost (the unit price the reading was billed at).
SELECT to_char(m.read_on, 'YYYY-MM')::text AS month, ch.kind::text AS kind,
       COALESCE(sum(m.consumption), 0)::float8 AS consumption,
       COALESCE(round(sum(m.consumption * m.unit_price_vnd)), 0)::bigint AS cost_vnd
FROM meter_readings m
JOIN charges ch ON ch.id = m.charge_id
JOIN rooms r ON r.id = ch.room_id
WHERE r.landlord_id = @landlord_id
  AND (sqlc.narg('building_id')::uuid IS NULL OR r.building_id = sqlc.narg('building_id')::uuid)
  AND ch.kind IN ('electricity', 'water')
  AND m.read_on BETWEEN @from_day::date AND @to_day::date
GROUP BY 1, 2;
