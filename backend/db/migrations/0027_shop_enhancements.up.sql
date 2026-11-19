-- Shop enhancements: warranty period, serial/IMEI tracking, product photos, and sale item serial numbers.
ALTER TABLE products
  ADD COLUMN IF NOT EXISTS warranty_months integer NOT NULL DEFAULT 0 CHECK (warranty_months >= 0),
  ADD COLUMN IF NOT EXISTS track_serial boolean NOT NULL DEFAULT false,
  ADD COLUMN IF NOT EXISTS photo_file uuid REFERENCES files(id);

ALTER TABLE sale_items
  ADD COLUMN IF NOT EXISTS serial_number text;
