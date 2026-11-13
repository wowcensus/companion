ALTER TABLE devices ADD COLUMN reg_ip_hash TEXT;
CREATE INDEX devices_reg_ip ON devices (reg_ip_hash, created_at);
