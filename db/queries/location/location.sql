-- Provinces and wards are listed in slug order: the slug is the name without accents, so the order is the
-- Vietnamese alphabetical one whatever collation the database was created with.

-- name: ListProvinces :many
SELECT code, name
FROM provinces
ORDER BY slug;

-- name: ProvinceExists :one
SELECT EXISTS (SELECT 1 FROM provinces WHERE code = $1);

-- name: ListWardsOfProvince :many
SELECT code, name
FROM wards
WHERE province_code = $1
ORDER BY slug, code;

-- name: UpsertProvinces :exec
INSERT INTO provinces (code, name, administrative_level, slug, area_phone_code)
SELECT unnest(@codes::integer[]), unnest(@names::text[]), unnest(@administrative_levels::text[]),
       unnest(@slugs::text[]), unnest(@area_phone_codes::smallint[])
ON CONFLICT (code) DO UPDATE
SET name = EXCLUDED.name,
    administrative_level = EXCLUDED.administrative_level,
    slug = EXCLUDED.slug,
    area_phone_code = EXCLUDED.area_phone_code;

-- name: UpsertWards :exec
INSERT INTO wards (code, province_code, name, administrative_level, slug)
SELECT unnest(@codes::integer[]), unnest(@province_codes::integer[]), unnest(@names::text[]),
       unnest(@administrative_levels::text[]), unnest(@slugs::text[])
ON CONFLICT (code) DO UPDATE
SET province_code = EXCLUDED.province_code,
    name = EXCLUDED.name,
    administrative_level = EXCLUDED.administrative_level,
    slug = EXCLUDED.slug;

-- name: CountProvinces :one
SELECT count(*) FROM provinces;

-- name: CountWards :one
SELECT count(*) FROM wards;
