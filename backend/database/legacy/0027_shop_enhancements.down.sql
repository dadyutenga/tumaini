ALTER TABLE sale_items
  DROP COLUMN IF EXISTS serial_number;

ALTER TABLE products
  DROP COLUMN IF EXISTS photo_file,
  DROP COLUMN IF EXISTS track_serial,
  DROP COLUMN IF EXISTS warranty_months;
