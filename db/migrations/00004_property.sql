-- +goose Up
CREATE TABLE buildings (
    id                    uuid         PRIMARY KEY DEFAULT uuidv7(),
    landlord_id           uuid         NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    name                  text         NOT NULL CHECK (char_length(name) BETWEEN 1 AND 50 AND name = btrim(name)),
    ward_code             integer      REFERENCES wards (code) ON DELETE SET NULL,
    address_detail        text         NOT NULL CHECK (char_length(address_detail) BETWEEN 1 AND 500),
    description           text         CHECK (char_length(description) <= 2000),
    -- What the rooms in it start from; a room that sets none of a part follows these (rooms.inherits_*).
    price_vnd             bigint       CHECK (price_vnd BETWEEN 0 AND 1000000000000),
    area_square_meters    numeric(7,2) CHECK (area_square_meters > 0),
    minimum_rental_period smallint     CHECK (minimum_rental_period BETWEEN 1 AND 3650),
    rental_period_unit    text         CHECK (rental_period_unit IN ('month', 'day')),
    -- The cover (copied from media) so lists never read the pictures.
    cover_key             text,
    cover_thumb_key       text,
    display_order         integer      NOT NULL,
    created_at            timestamptz  NOT NULL DEFAULT now(),
    updated_at            timestamptz  NOT NULL DEFAULT now(),
    deleted_at            timestamptz,
    CHECK ((minimum_rental_period IS NULL) = (rental_period_unit IS NULL)),
    CHECK ((cover_key IS NULL) = (cover_thumb_key IS NULL))
);
-- A landlord's building names are unique whatever the case; a deleted building frees its name.
CREATE UNIQUE INDEX buildings_landlord_name_key ON buildings (landlord_id, lower(name)) WHERE deleted_at IS NULL;
CREATE INDEX buildings_landlord_order_idx ON buildings (landlord_id, display_order, id) WHERE deleted_at IS NULL;
CREATE INDEX buildings_ward_code_idx ON buildings (ward_code);

CREATE TABLE rooms (
    id                     uuid         PRIMARY KEY DEFAULT uuidv7(),
    landlord_id            uuid         NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    building_id            uuid         REFERENCES buildings (id) ON DELETE RESTRICT,
    name                   text         NOT NULL CHECK (char_length(name) BETWEEN 1 AND 50 AND name = btrim(name)),
    ward_code              integer      REFERENCES wards (code) ON DELETE SET NULL,
    address_detail         text         NOT NULL CHECK (char_length(address_detail) BETWEEN 1 AND 500),
    area_square_meters     numeric(7,2) CHECK (area_square_meters > 0),
    -- Rent of one period of rental_period_unit.
    price_vnd              bigint       NOT NULL CHECK (price_vnd BETWEEN 0 AND 1000000000000),
    minimum_rental_period  smallint     NOT NULL DEFAULT 1 CHECK (minimum_rental_period BETWEEN 1 AND 3650),
    rental_period_unit     text         NOT NULL DEFAULT 'month' CHECK (rental_period_unit IN ('month', 'day')),
    description            text         CHECK (char_length(description) <= 5000),
    status                 text         NOT NULL DEFAULT 'available' CHECK (status IN ('available', 'rented', 'maintenance')),
    inherits_price         boolean      NOT NULL DEFAULT false,
    inherits_area          boolean      NOT NULL DEFAULT false,
    inherits_rental_period boolean      NOT NULL DEFAULT false,
    inherits_description   boolean      NOT NULL DEFAULT false,
    inherits_charges       boolean      NOT NULL DEFAULT false,
    inherits_media         boolean      NOT NULL DEFAULT false,
    cover_key              text,
    cover_thumb_key        text,
    display_order          integer      NOT NULL,
    created_at             timestamptz  NOT NULL DEFAULT now(),
    updated_at             timestamptz  NOT NULL DEFAULT now(),
    deleted_at             timestamptz,
    -- A room outside any building follows nothing.
    CHECK (building_id IS NOT NULL OR NOT (inherits_price OR inherits_area OR inherits_rental_period
                                           OR inherits_description OR inherits_charges OR inherits_media)),
    CHECK ((cover_key IS NULL) = (cover_thumb_key IS NULL))
);
-- A name is taken once per building (rooms outside any building form one group); a deleted room frees it.
CREATE UNIQUE INDEX rooms_landlord_building_name_key ON rooms (landlord_id, building_id, lower(name))
    NULLS NOT DISTINCT WHERE deleted_at IS NULL;
CREATE INDEX rooms_landlord_order_idx ON rooms (landlord_id, display_order, id) WHERE deleted_at IS NULL;
CREATE INDEX rooms_landlord_status_idx ON rooms (landlord_id, status) WHERE deleted_at IS NULL;
CREATE INDEX rooms_building_idx ON rooms (building_id) WHERE deleted_at IS NULL;
CREATE INDEX rooms_ward_code_idx ON rooms (ward_code);

-- Costs on top of the rent: of a room, or of a building (the templates its rooms can follow).
CREATE TABLE charges (
    id                 uuid        PRIMARY KEY DEFAULT uuidv7(),
    room_id            uuid        REFERENCES rooms (id) ON DELETE CASCADE,
    building_id        uuid        REFERENCES buildings (id) ON DELETE CASCADE,
    -- On a room's copy of a building charge: the template it follows (a change reaches it in place, keeping its id
    -- for the meter readings that will hang on it).
    building_charge_id uuid        REFERENCES charges (id) ON DELETE SET NULL,
    name               text        NOT NULL CHECK (char_length(name) BETWEEN 1 AND 100),
    kind               text        NOT NULL CHECK (kind IN ('service', 'electricity', 'water', 'internet', 'parking', 'cleaning', 'other')),
    amount_vnd         bigint      NOT NULL CHECK (amount_vnd BETWEEN 0 AND 1000000000000),
    unit               text        NOT NULL CHECK (unit IN ('per_period', 'per_person', 'per_kwh', 'per_cubic_meter', 'one_time')),
    note               text        CHECK (char_length(note) <= 255),
    display_order      smallint    NOT NULL,
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now(),
    deleted_at         timestamptz,
    CHECK (num_nonnulls(room_id, building_id) = 1),
    CHECK (building_charge_id IS NULL OR room_id IS NOT NULL)
);
CREATE INDEX charges_room_idx ON charges (room_id, display_order, id) WHERE deleted_at IS NULL;
CREATE INDEX charges_building_idx ON charges (building_id, display_order, id) WHERE deleted_at IS NULL;
CREATE INDEX charges_building_charge_idx ON charges (building_charge_id);
CREATE UNIQUE INDEX charges_room_template_key ON charges (room_id, building_charge_id)
    WHERE deleted_at IS NULL AND building_charge_id IS NOT NULL;

-- Pictures of a room or of a building.
CREATE TABLE media (
    id            uuid        PRIMARY KEY DEFAULT uuidv7(),
    room_id       uuid        REFERENCES rooms (id) ON DELETE CASCADE,
    building_id   uuid        REFERENCES buildings (id) ON DELETE CASCADE,
    kind          text        NOT NULL DEFAULT 'photo' CHECK (kind IN ('photo', 'cover')),
    file_key      text        NOT NULL UNIQUE,
    thumb_key     text        NOT NULL UNIQUE,
    content_type  text        NOT NULL CHECK (content_type IN ('image/webp', 'image/jpeg')),
    size_bytes    integer     NOT NULL CHECK (size_bytes > 0),
    width         integer     NOT NULL CHECK (width > 0),
    height        integer     NOT NULL CHECK (height > 0),
    original_name text        CHECK (char_length(original_name) <= 255),
    display_order smallint    NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    CHECK (num_nonnulls(room_id, building_id) = 1)
);
CREATE INDEX media_room_idx ON media (room_id, display_order, id);
CREATE INDEX media_building_idx ON media (building_id, display_order, id);
-- At most one cover per owner.
CREATE UNIQUE INDEX media_room_cover_key ON media (room_id) WHERE kind = 'cover' AND room_id IS NOT NULL;
CREATE UNIQUE INDEX media_building_cover_key ON media (building_id) WHERE kind = 'cover' AND building_id IS NOT NULL;

-- +goose Down
DROP TABLE media;
DROP TABLE charges;
DROP TABLE rooms;
DROP TABLE buildings;
