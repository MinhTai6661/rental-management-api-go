-- +goose Up
-- Reference data of the Vietnamese administrative divisions (loaded by `rental-management seed`).
CREATE TABLE provinces (
    code                 integer  PRIMARY KEY,
    name                 text     NOT NULL,
    administrative_level text     NOT NULL,
    slug                 text     NOT NULL UNIQUE,
    area_phone_code      smallint
);

CREATE TABLE wards (
    code                 integer PRIMARY KEY,
    province_code        integer NOT NULL REFERENCES provinces (code) ON DELETE RESTRICT,
    name                 text    NOT NULL,
    administrative_level text    NOT NULL,
    slug                 text    NOT NULL
);

-- The wards of a province are listed in slug order (see db/queries/location).
CREATE INDEX wards_province_code_slug_idx ON wards (province_code, slug);

-- +goose Down
DROP TABLE wards;
DROP TABLE provinces;
