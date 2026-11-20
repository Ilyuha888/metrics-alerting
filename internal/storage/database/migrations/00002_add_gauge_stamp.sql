-- +goose Up
-- Nullable: rows written before this migration, and gauges only ever written without a stamp, have none.
ALTER TABLE gauges ADD COLUMN stamp BIGINT;

-- +goose Down
ALTER TABLE gauges DROP COLUMN stamp;
