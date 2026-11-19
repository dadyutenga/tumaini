DROP FUNCTION IF EXISTS svc_today();
DROP FUNCTION IF EXISTS design_token_business(text);
DROP TABLE IF EXISTS svc_renewals, svc_tasks, svc_documents, contractor_payments, svc_costs,
  svc_payment_allocations, svc_payments, svc_invoice_installments, svc_lines;
ALTER TABLE IF EXISTS svc_quotations DROP CONSTRAINT IF EXISTS svc_quotations_invoice_fk;
DROP TABLE IF EXISTS svc_invoices, svc_quotations, design_print_orders, design_time_entries, design_feedback,
  design_versions, design_references, design_jobs, svc_project_notes, svc_stage_events, svc_project_stages,
  svc_projects, contractors, svc_package_items, svc_catalog_documents, svc_catalog_stages, svc_catalog, svc_clients;
DELETE FROM role_permissions WHERE permission_key IN (SELECT key FROM permissions WHERE module = 'services' OR key = 'reports.services');
DELETE FROM permissions WHERE module = 'services' OR key = 'reports.services';
DELETE FROM document_sequences WHERE doc_type IN ('svc_project','svc_quotation','svc_invoice','design_job','svc_cost');
