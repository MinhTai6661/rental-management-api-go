-- name: UpsertMeterReadings :exec
-- The readings of a bill: one per meter. A meter that is read again on the same bill keeps its place in the history
-- (its id and the day it was first read).
INSERT INTO meter_readings (charge_id, invoice_id, read_on, reading_value, consumption, unit_price_vnd, recorded_by_id)
SELECT m.charge_id, @invoice_id, @read_on, m.reading_value::numeric(12,2), m.consumption::numeric(12,2), m.unit_price_vnd, @recorded_by_id
FROM (SELECT unnest(@charge_ids::uuid[]) AS charge_id, unnest(@reading_values::float8[]) AS reading_value,
             unnest(@consumptions::float8[]) AS consumption, unnest(@unit_prices::bigint[]) AS unit_price_vnd) AS m
ON CONFLICT (invoice_id, charge_id) DO UPDATE
SET reading_value = EXCLUDED.reading_value, consumption = EXCLUDED.consumption, unit_price_vnd = EXCLUDED.unit_price_vnd,
    recorded_by_id = EXCLUDED.recorded_by_id;

-- name: DeleteMeterReadingsNotIn :exec
-- Drops the readings of a bill for meters it no longer reads (all of them when charge_ids is empty).
DELETE FROM meter_readings WHERE invoice_id = @invoice_id AND NOT (charge_id = ANY (@charge_ids::uuid[]));

-- name: ListReadingsOfInvoices :many
SELECT m.id, m.charge_id, m.invoice_id, m.read_on, m.reading_value, m.consumption, m.unit_price_vnd,
       c.name AS charge_name, c.unit AS charge_unit
FROM meter_readings m
JOIN charges c ON c.id = m.charge_id
WHERE m.invoice_id = ANY (@invoice_ids::uuid[])
ORDER BY m.invoice_id, c.display_order, m.id;

-- name: ListLastMeterReadings :many
-- The last stored index of each meter. A bill being corrected (editing_id) counts from what came before it: its own
-- readings, and anything recorded after them, are left out.
SELECT DISTINCT ON (m.charge_id) m.charge_id, m.reading_value
FROM meter_readings m
WHERE m.charge_id = ANY (@charge_ids::uuid[])
  AND (sqlc.narg('editing_id')::uuid IS NULL
       OR (m.invoice_id <> sqlc.narg('editing_id')::uuid
           AND NOT EXISTS (SELECT 1 FROM meter_readings own
                           WHERE own.invoice_id = sqlc.narg('editing_id')::uuid AND own.charge_id = m.charge_id AND own.id <= m.id)))
ORDER BY m.charge_id, m.read_on DESC, m.id DESC;

-- name: ListMeterReadingHistory :many
-- The meter readings of a room, newest first (a removed charge's readings stay); charge_id keeps one meter.
SELECT m.id, m.charge_id, m.invoice_id, m.read_on, m.reading_value, m.consumption, m.unit_price_vnd,
       c.name AS charge_name, c.unit AS charge_unit
FROM meter_readings m
JOIN charges c ON c.id = m.charge_id
WHERE c.room_id = @room_id AND (sqlc.narg('charge_id')::uuid IS NULL OR m.charge_id = sqlc.narg('charge_id')::uuid)
ORDER BY m.read_on DESC, m.id DESC
LIMIT @page_limit OFFSET @page_offset;

-- name: CountMeterReadingHistory :one
SELECT count(*)::integer
FROM meter_readings m
JOIN charges c ON c.id = m.charge_id
WHERE c.room_id = @room_id AND (sqlc.narg('charge_id')::uuid IS NULL OR m.charge_id = sqlc.narg('charge_id')::uuid);
