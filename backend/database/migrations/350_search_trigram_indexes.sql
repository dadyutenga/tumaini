-- =====================================================================
-- 350_search_trigram_indexes.sql — indexes for global search (GET /search).
--
-- The search matches part of a name, code or number and tolerates a misspelt name, which only a
-- trigram index can serve. products.name and customers.full_name already have one (030, 040).
-- =====================================================================
SET search_path = app, public;

CREATE INDEX products_sku_trgm        ON app.products    USING gin (sku gin_trgm_ops);
CREATE INDEX products_barcode_trgm    ON app.products    USING gin (barcode gin_trgm_ops);
CREATE INDEX customers_phone_trgm     ON app.customers   USING gin (phone gin_trgm_ops);
CREATE INDEX sales_receipt_no_trgm    ON app.sales       USING gin (receipt_no gin_trgm_ops);
CREATE INDEX invoices_invoice_no_trgm ON app.invoices    USING gin (invoice_no gin_trgm_ops);
CREATE INDEX quotations_no_trgm       ON app.quotations  USING gin (quotation_no gin_trgm_ops);
CREATE INDEX projects_project_no_trgm ON app.projects    USING gin (project_no gin_trgm_ops);
CREATE INDEX design_jobs_job_no_trgm  ON app.design_jobs USING gin (job_no gin_trgm_ops);
