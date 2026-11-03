-- +goose Up
CREATE TABLE gauges (
  name  TEXT PRIMARY KEY,
  value DOUBLE PRECISION NOT NULL
);
CREATE TABLE counters (
  name  TEXT PRIMARY KEY,
  delta BIGINT NOT NULL
);

-- +goose Down
DROP TABLE counters;
DROP TABLE gauges;
