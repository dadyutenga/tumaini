ALTER TABLE sales ADD COLUMN receipt_footer text NOT NULL DEFAULT '';
ALTER TABLE sales ADD COLUMN prices_include_tax boolean NOT NULL DEFAULT false;
ALTER TABLE sales ADD COLUMN converted_sale_id uuid REFERENCES sales(id);
