ALTER TABLE receipts ADD COLUMN client_id TEXT;
ALTER TABLE devices ADD COLUMN client_id TEXT;
CREATE INDEX receipts_client ON receipts (client_id);
