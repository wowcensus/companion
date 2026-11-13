CREATE TABLE devices (
  id           TEXT PRIMARY KEY,
  token_sha256 TEXT NOT NULL UNIQUE,
  label        TEXT,
  created_at   TEXT NOT NULL,
  last_seen_at TEXT,
  revoked_at   TEXT
);

CREATE TABLE receipts (
  id          TEXT PRIMARY KEY,
  device_id   TEXT NOT NULL REFERENCES devices(id),
  source_id   TEXT NOT NULL,
  sha256      TEXT NOT NULL,
  zip_sha256  TEXT NOT NULL,
  object_key  TEXT NOT NULL,
  zip_bytes   INTEGER NOT NULL,
  lua_bytes   INTEGER NOT NULL,
  edition     TEXT NOT NULL,
  app_version TEXT NOT NULL,
  status      TEXT NOT NULL CHECK (status IN ('stored', 'queued', 'imported', 'failed')),
  detail      TEXT,
  created_at  TEXT NOT NULL,
  queued_at   TEXT,
  finished_at TEXT,
  UNIQUE (device_id, source_id, sha256)
);
CREATE INDEX receipts_status ON receipts (status, created_at);
