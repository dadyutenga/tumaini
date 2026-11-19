-- =====================================================================
-- 130_reference_data.sql — state machines, permission catalogue,
-- retention schedule placeholders
-- =====================================================================
SET search_path = app, public;

INSERT INTO app.state_transitions (entity, from_status, to_status) VALUES
-- sale
('sale','*new*','draft'),
('sale','draft','held'), ('sale','held','draft'),
('sale','draft','pending_approval'), ('sale','pending_approval','draft'),
('sale','draft','completed'), ('sale','held','completed'), ('sale','pending_approval','completed'),
('sale','completed','voided'),
-- sale return
('sale_return','*new*','requested'),
('sale_return','requested','approved'), ('sale_return','requested','rejected'), ('sale_return','requested','cancelled'),
('sale_return','approved','completed'), ('sale_return','approved','cancelled'),
-- purchase
('purchase','*new*','draft'), ('purchase','*new*','ordered'),
('purchase','draft','ordered'), ('purchase','draft','cancelled'), ('purchase','ordered','cancelled'),
('purchase','ordered','partially_received'), ('purchase','ordered','received'),
('purchase','partially_received','received'),
-- stock documents
('stock_adjustment','*new*','draft'), ('stock_adjustment','draft','posted'), ('stock_adjustment','draft','cancelled'),
('stock_transfer','*new*','draft'), ('stock_transfer','draft','dispatched'), ('stock_transfer','draft','cancelled'),
('stock_transfer','dispatched','received'),
('stock_count','*new*','in_progress'), ('stock_count','in_progress','completed'), ('stock_count','in_progress','cancelled'),
-- quotation
('quotation','*new*','draft'), ('quotation','draft','sent'), ('quotation','sent','accepted'),
('quotation','sent','rejected'), ('quotation','sent','expired'), ('quotation','draft','expired'),
('quotation','accepted','converted'), ('quotation','sent','draft'),
-- project
('project','*new*','not_started'), ('project','not_started','in_progress'), ('project','in_progress','on_hold'),
('project','on_hold','in_progress'), ('project','in_progress','completed'),
('project','not_started','cancelled'), ('project','in_progress','cancelled'), ('project','on_hold','cancelled'),
-- project stage (any reasonable move incl. reopening a done stage, which is audited)
('project_stage','*new*','not_started'),
('project_stage','not_started','in_progress'), ('project_stage','not_started','done'), ('project_stage','not_started','stuck'),
('project_stage','in_progress','done'), ('project_stage','in_progress','stuck'), ('project_stage','in_progress','not_started'),
('project_stage','stuck','in_progress'), ('project_stage','stuck','done'),
('project_stage','done','in_progress'),
-- client document
('client_document','*new*','missing'), ('client_document','*new*','received'),
('client_document','missing','received'), ('client_document','received','rejected'),
('client_document','rejected','received'), ('client_document','received','returned'),
-- invoice
('invoice','*new*','draft'), ('invoice','draft','issued'), ('invoice','issued','partially_paid'),
('invoice','issued','paid'), ('invoice','partially_paid','paid'), ('invoice','partially_paid','issued'),
('invoice','paid','partially_paid'), ('invoice','paid','issued'), ('invoice','draft','void'), ('invoice','issued','void'),
-- design job
('design_job','*new*','new_brief'),
('design_job','new_brief','in_design'), ('design_job','in_design','internal_review'),
('design_job','internal_review','in_design'), ('design_job','internal_review','with_client'),
('design_job','in_design','with_client'), ('design_job','with_client','in_design'),
('design_job','with_client','approved'), ('design_job','in_design','approved'), ('design_job','internal_review','approved'),
('design_job','approved','printing'), ('design_job','approved','delivered'), ('design_job','printing','delivered'),
('design_job','new_brief','cancelled'), ('design_job','in_design','cancelled'), ('design_job','internal_review','cancelled'),
('design_job','with_client','cancelled'),
-- design version
('design_version','*new*','draft'), ('design_version','draft','sent_to_client'), ('design_version','draft','superseded'),
('design_version','sent_to_client','approved'), ('design_version','sent_to_client','changes_requested'),
('design_version','sent_to_client','superseded'), ('design_version','draft','approved'),
-- print order
('print_order','*new*','sent_to_print'), ('print_order','sent_to_print','ready'), ('print_order','ready','delivered'),
('print_order','sent_to_print','cancelled'),
-- task
('task','*new*','open'), ('task','open','in_progress'), ('task','in_progress','open'), ('task','open','done'),
('task','in_progress','done'), ('task','done','open'), ('task','open','cancelled'), ('task','in_progress','cancelled'),
-- renewal
('renewal','*new*','upcoming'), ('renewal','upcoming','renewed'), ('renewal','upcoming','lapsed'),
('renewal','lapsed','renewed'), ('renewal','upcoming','cancelled'),
-- expense
('expense','*new*','submitted'), ('expense','*new*','approved'),
('expense','submitted','approved'), ('expense','submitted','rejected'), ('expense','submitted','cancelled'),
('expense','approved','paid'), ('expense','approved','cancelled'),
-- payment (received & made)
('payment','*new*','posted'), ('payment','posted','voided'),
-- contractor bill
('contractor_bill','*new*','open'), ('contractor_bill','open','partially_paid'), ('contractor_bill','open','paid'),
('contractor_bill','partially_paid','paid'), ('contractor_bill','partially_paid','open'), ('contractor_bill','paid','partially_paid'),
('contractor_bill','paid','open'), ('contractor_bill','open','void'),
-- day closing
('day_closing','*new*','open'), ('day_closing','open','closed'), ('day_closing','closed','reopened'),
('day_closing','reopened','closed'),
-- payroll run
('payroll_run','*new*','draft'), ('payroll_run','draft','approved'), ('payroll_run','approved','draft'),
('payroll_run','approved','paid'), ('payroll_run','draft','cancelled'),
-- approval request
('approval_request','*new*','pending'), ('approval_request','pending','approved'), ('approval_request','pending','rejected'),
('approval_request','pending','cancelled'), ('approval_request','pending','expired'),
-- leave
('leave_request','*new*','requested'), ('leave_request','requested','approved'), ('leave_request','requested','rejected'),
('leave_request','requested','cancelled'), ('leave_request','approved','cancelled'),
-- membership
('membership','*new*','invited'), ('membership','*new*','active'), ('membership','invited','active'),
('membership','invited','revoked'), ('membership','active','suspended'), ('membership','suspended','active'),
('membership','active','revoked'), ('membership','suspended','revoked'),
-- employee advance
('employee_advance','*new*','active'), ('employee_advance','active','settled'), ('employee_advance','active','written_off'),
-- commission
('commission_entry','*new*','accrued'), ('commission_entry','accrued','included'), ('commission_entry','accrued','reversed'),
('commission_entry','included','accrued'),
-- outbound message
('outbound_message','*new*','queued'), ('outbound_message','queued','sending'), ('outbound_message','sending','sent'),
('outbound_message','sending','failed'), ('outbound_message','failed','sending'), ('outbound_message','sent','delivered'),
('outbound_message','sent','failed'), ('outbound_message','queued','cancelled'), ('outbound_message','failed','cancelled');

-- ---------- Permission catalogue (module.resource.action) ---------------
INSERT INTO app.permissions (code, module, description, is_sensitive) VALUES
('dashboard.business.view',      'dashboard', 'Full business dashboard (profit, balances)', true),
('dashboard.operations.view',    'dashboard', 'Operations dashboard', false),
('shop.sale.create',             'shop', 'Use POS / New Sale', false),
('shop.sale.view_all',           'shop', 'View all sales (otherwise own only)', false),
('shop.sale.void',               'shop', 'Void a sale directly (Admin)', true),
('shop.sale.request_void',       'shop', 'Request a sale void', false),
('shop.discount.apply',          'shop', 'Apply a discount up to own limit', false),
('shop.discount.approve',        'shop', 'Approve discounts above a seller limit', false),
('shop.return.create',           'shop', 'Start a return / refund request', false),
('shop.return.approve',          'shop', 'Approve returns and refunds up to own limit', true),
('shop.product.view',            'shop', 'View catalogue (selling price, stock)', false),
('shop.product.view_cost',       'shop', 'View buying price / cost / margin', true),
('shop.product.manage',          'shop', 'Create / edit products and categories', false),
('shop.product.price_change',    'shop', 'Change selling price directly', true),
('shop.product.price_approve',   'shop', 'Approve price change requests', true),
('shop.stock.view',              'shop', 'View stock screens', false),
('shop.stock.manage',            'shop', 'Stock in/out, adjustments, counts, transfers', false),
('shop.customer.view_all',       'shop', 'View all shop customers', false),
('shop.customer.manage',         'shop', 'Create / edit customers', false),
('shop.supplier.manage',         'shop', 'Suppliers', false),
('shop.purchase.manage',         'shop', 'Purchases and goods receipts', false),
('services.client.view_all',     'services', 'View all clients (otherwise own/assigned)', false),
('services.client.manage',       'services', 'Create / edit clients and leads', false),
('services.project.view_all',    'services', 'View all projects', false),
('services.project.manage',      'services', 'Create / edit projects, stages', false),
('services.project.assign',      'services', 'Assign / reassign jobs and tasks', false),
('services.catalog.manage',      'services', 'Service catalogue and packages', false),
('services.quotation.manage',    'services', 'Create, edit, send quotations', false),
('services.quotation.draft',     'services', 'Create draft quotations for own jobs', false),
('services.invoice.manage',      'services', 'Create, edit, send invoices', false),
('services.invoice.draft',       'services', 'Create draft invoices for own jobs', false),
('services.document.manage',     'services', 'Upload / download client documents', false),
('services.price.approve',       'services', 'Approve job price changes', false),
('services.renewal.manage',      'services', 'Renewals', false),
('services.contractor.manage',   'services', 'Contractors and bills', false),
('services.design.view_all',     'services', 'View all design jobs', false),
('services.design.manage',       'services', 'Create / edit design jobs, upload versions', false),
('services.task.view_all',       'services', 'View all tasks', false),
('services.task.manage',         'services', 'Create and update tasks', false),
('finance.account.view',         'finance', 'View accounts and balances', true),
('finance.account.manage',       'finance', 'Manage accounts, opening balances, transfers', true),
('finance.transaction.view',     'finance', 'View ledger', true),
('finance.transaction.correct',  'finance', 'Approve correction entries', true),
('finance.payment.receive',      'finance', 'Record payments received', false),
('finance.payment.view_all',     'finance', 'View all payments received', false),
('finance.payment.make',         'finance', 'Record payments made', true),
('finance.payment.void',         'finance', 'Void payments directly (Admin)', true),
('finance.expense.submit',       'finance', 'Submit expense requests', false),
('finance.expense.enter',        'finance', 'Enter expenses', false),
('finance.expense.approve',      'finance', 'Approve expenses up to own limit', true),
('finance.expense.pay',          'finance', 'Mark approved expenses paid', true),
('finance.debt.view',            'finance', 'Debts screen', false),
('finance.day.close',            'finance', 'End-of-day closing', false),
('finance.day.reopen',           'finance', 'Reopen a closed day (Admin)', true),
('hr.employee.view',             'hr', 'View employee records (no pay)', false),
('hr.employee.manage',           'hr', 'Create / edit employees', true),
('hr.attendance.manage',         'hr', 'Correct attendance', false),
('hr.leave.approve',             'hr', 'Approve / reject leave', false),
('hr.compensation.view',         'hr', 'View salaries of others', true),
('hr.compensation.manage',       'hr', 'Change salaries, allowances, advances', true),
('hr.payroll.view',              'hr', 'View payroll runs', true),
('hr.payroll.manage',            'hr', 'Generate / edit payroll', true),
('hr.payroll.approve',           'hr', 'Approve and pay payroll', true),
('reports.business.view',        'reports', 'All reports incl. profit and loss', true),
('reports.operations.view',      'reports', 'Sales, stock, projects, debts reports', false),
('reports.export',               'reports', 'Export reports', true),
('security.user.manage',         'security', 'Users, roles and permissions', true),
('security.audit.view',          'security', 'Audit log and login history', true),
('security.backup.manage',       'security', 'Download / restore backups', true),
('settings.manage',              'settings', 'All settings', true),
('notifications.all.view',       'notifications', 'All notification types', false);

-- Retention schedule: categories registered, periods deliberately NULL
-- until approved by the business / counsel (decision D-10).
INSERT INTO app.retention_policies (data_category, description, disposal_method) VALUES
('financial_records',  'Sales, invoices, payments, ledger, expenses, payroll', 'archive_then_delete'),
('tax_records',        'Documents required for TRA tax audits', 'archive_then_delete'),
('employee_records',   'Employee master, compensation, attendance, leave', 'archive_then_delete'),
('client_documents',   'Client identity / registration documents uploaded for services', 'delete'),
('customer_profiles',  'Customer / client contact data after last activity', 'anonymise'),
('design_files',       'Design versions and final assets', 'delete'),
('audit_log',          'Audit trail partitions', 'archive_then_delete'),
('auth_events',        'Login history partitions', 'delete'),
('sessions',           'Expired / revoked sessions', 'delete'),
('outbound_messages',  'Sent SMS / WhatsApp / email bodies', 'delete'),
('notifications',      'In-app notifications', 'delete'),
('idempotency_keys',   'API idempotency records (technical, 24h)', 'delete'),
('backups',            'Database backups and WAL archives', 'delete');
UPDATE app.retention_policies SET retention_period = interval '2 days', legal_basis = 'technical necessity',
       approved_by = 'architecture (technical default)', approved_at = now()
 WHERE data_category = 'idempotency_keys';
