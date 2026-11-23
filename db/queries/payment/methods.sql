-- name: ListLandlordPaymentMethods :many
SELECT method, config_sealed, is_active
FROM landlord_payment_methods
WHERE landlord_id = @landlord_id
ORDER BY method;

-- name: GetLandlordPaymentMethod :one
SELECT method, config_sealed, is_active
FROM landlord_payment_methods
WHERE landlord_id = @landlord_id AND method = @method;

-- name: UpsertLandlordPaymentMethod :one
-- Saves a method and says whether the row is new (xmax is 0 for an inserted row).
INSERT INTO landlord_payment_methods (landlord_id, method, config_sealed, is_active)
VALUES (@landlord_id, @method, @config_sealed, @is_active)
ON CONFLICT (landlord_id, method)
DO UPDATE SET config_sealed = EXCLUDED.config_sealed, is_active = EXCLUDED.is_active, updated_at = now()
RETURNING (xmax = 0) AS created;

-- name: DeleteLandlordPaymentMethod :execrows
DELETE FROM landlord_payment_methods WHERE landlord_id = @landlord_id AND method = @method;
