# BMS Data Dictionary

Generated from the live PostgreSQL 16 catalog after all migrations (2026-11-28). Every table in schemas iam, app, audit. Monetary domains: money/money_nn/money_pos = numeric(18,2); qty = numeric(14,3); pct = numeric(5,2) 0–100.

## app.access_review_items

_(see spec section 5)_

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| access_review_id | uuid | NO |  | PK → access_reviews |  |
| membership_id | uuid | NO |  | PK → memberships |  |
| role_code | text | NO |  |  |  |
| overrides | text[] | NO | '{}'::text[] |  |  |
| decision | text | yes |  |  |  |
| note | text | yes |  |  |  |
| decided_at | timestamp with time zone | yes |  |  |  |

**Constraints:** `access_review_items_decision_check` CHECK ((decision = ANY (ARRAY['keep'::text, 'change'::text, 'revoke'::text]))); `access_review_items_note_check` CHECK ((length(note) <= 1000))

**Indexes:** `CREATE UNIQUE INDEX access_review_items_pkey ON app.access_review_items USING btree (access_review_id, membership_id)`


## app.access_reviews

_(see spec section 5)_

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| period_label | text | NO |  |  |  |
| status | app.access_review_status | NO | 'open'::app.access_review_status |  |  |
| reviewer_id | uuid | NO |  | → memberships |  |
| started_at | timestamp with time zone | NO | now() |  |  |
| completed_at | timestamp with time zone | yes |  |  |  |

**Constraints:** `access_reviews_check` CHECK (((status <> 'completed'::app.access_review_status) OR (completed_at IS NOT NULL))); `access_reviews_period_label_check` CHECK ((length(period_label) <= 40)); `access_reviews_tenant_id_id_key` UNIQUE (id)

**Indexes:** `CREATE UNIQUE INDEX access_reviews_pkey ON app.access_reviews USING btree (id)`; `CREATE UNIQUE INDEX access_reviews_tenant_id_id_key ON app.access_reviews USING btree (id)`


## app.account_transfers

_(see spec section 5)_

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| transfer_no | text | NO |  |  |  |
| from_account_id | uuid | NO |  | → financial_accounts |  |
| to_account_id | uuid | NO |  | → financial_accounts |  |
| amount | app.money_pos | NO |  |  |  |
| location_id | uuid | NO |  | → locations |  |
| business_date | date | NO |  |  |  |
| note | text | yes |  |  |  |
| created_at | timestamp with time zone | NO | now() |  |  |
| created_by | uuid | yes | app.current_membership_id() | → memberships |  |

**Constraints:** `account_transfers_check` CHECK ((from_account_id <> to_account_id)); `account_transfers_note_check` CHECK ((length(note) <= 1000)); `account_transfers_tenant_id_id_key` UNIQUE (id); `account_transfers_tenant_id_transfer_no_key` UNIQUE (transfer_no)

**Indexes:** `CREATE UNIQUE INDEX account_transfers_pkey ON app.account_transfers USING btree (id)`; `CREATE UNIQUE INDEX account_transfers_tenant_id_id_key ON app.account_transfers USING btree (id)`; `CREATE UNIQUE INDEX account_transfers_tenant_id_transfer_no_key ON app.account_transfers USING btree (transfer_no)`


## app.advance_repayments

_(see spec section 5)_

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| advance_id | uuid | NO |  | → employee_advances |  |
| payroll_line_id | uuid | NO |  | → payroll_lines |  |
| amount | app.money_pos | NO |  |  |  |
| created_at | timestamp with time zone | NO | now() |  |  |
| created_by | uuid | yes | app.current_membership_id() | → memberships |  |

**Constraints:** `advance_repayments_tenant_id_advance_id_payroll_line_id_key` UNIQUE (advance_id, payroll_line_id); `advance_repayments_tenant_id_id_key` UNIQUE (id)

**Indexes:** `CREATE UNIQUE INDEX advance_repayments_pkey ON app.advance_repayments USING btree (id)`; `CREATE UNIQUE INDEX advance_repayments_tenant_id_advance_id_payroll_line_id_key ON app.advance_repayments USING btree (advance_id, payroll_line_id)`; `CREATE UNIQUE INDEX advance_repayments_tenant_id_id_key ON app.advance_repayments USING btree (id)`


## app.approval_requests

_(see spec section 5)_

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| approval_type | app.approval_type | NO |  |  |  |
| status | app.approval_status | NO | 'pending'::app.approval_status |  |  |
| reason | text | NO |  |  |  |
| requested_amount | numeric(18,2) | yes |  |  |  |
| requested_percent | numeric(5,2) | yes |  |  |  |
| current_amount | numeric(18,2) | yes |  |  |  |
| sale_id | uuid | yes |  | → sales |  |
| project_id | uuid | yes |  | → projects |  |
| design_job_id | uuid | yes |  | → design_jobs |  |
| financial_transaction_id | uuid | yes |  | → financial_transactions |  |
| payment_received_id | uuid | yes |  | → payments_received |  |
| payment_made_id | uuid | yes |  | → payments_made |  |
| day_closing_id | uuid | yes |  | → day_closings |  |
| product_id | uuid | yes |  | → products |  |
| requested_by | uuid | NO | app.current_membership_id() | → memberships |  |
| requested_at | timestamp with time zone | NO | now() |  |  |
| decided_by | uuid | yes |  | → memberships |  |
| decided_at | timestamp with time zone | yes |  |  |  |
| decision_note | text | yes |  |  |  |
| expires_at | timestamp with time zone | yes |  |  |  |
| created_at | timestamp with time zone | NO | now() |  |  |
| created_by | uuid | yes | app.current_membership_id() | → memberships |  |
| updated_at | timestamp with time zone | NO | now() |  |  |
| updated_by | uuid | yes |  | → memberships |  |
| row_version | integer | NO | 1 |  |  |

**Constraints:** `approval_no_self_approval` CHECK (((decided_by IS NULL) OR (decided_by <> requested_by))); `approval_requests_check` CHECK ((num_nonnulls(sale_id, project_id, design_job_id, financial_transaction_id, payment_received_id, payment_made_id, day_closing_id, product_id) = 1)); `approval_requests_check1` CHECK (
CASE approval_type
    WHEN 'sale_discount'::app.approval_type THEN ((sale_id IS NOT NULL) AND (requested_percent IS NOT NULL))
    WHEN 'job_price_change'::app.approval_type THEN (((project_id IS NOT NULL) OR (design_job_id IS NOT NULL)) AND (requested_amount IS NOT NULL))
    WHEN 'sale_void'::app.approval_type THEN (sale_id IS NOT NULL)
    WHEN 'transaction_correction'::app.approval_type THEN ((financial_transaction_id IS NOT NULL) AND (requested_amount IS NOT NULL))
    WHEN 'payment_void'::app.approval_type THEN ((payment_received_id IS NOT NULL) OR (payment_made_id IS NOT NULL))
    WHEN 'day_reopen'::app.approval_type THEN (day_closing_id IS NOT NULL)
    WHEN 'product_price_change'::app.approval_type THEN ((product_id IS NOT NULL) AND (requested_amount IS NOT NULL))
    ELSE NULL::boolean
END); `approval_requests_check2` CHECK (((status = ANY (ARRAY['pending'::app.approval_status, 'cancelled'::app.approval_status, 'expired'::app.approval_status])) OR ((decided_by IS NOT NULL) AND (decided_at IS NOT NULL)))); `approval_requests_check3` CHECK (((status <> 'rejected'::app.approval_status) OR (decision_note IS NOT NULL))); `approval_requests_decision_note_check` CHECK ((length(decision_note) <= 1000)); `approval_requests_reason_check` CHECK ((length(btrim(reason)) >= 3)); `approval_requests_requested_amount_check` CHECK ((requested_amount >= (0)::numeric)); `approval_requests_requested_percent_check` CHECK (((requested_percent >= (0)::numeric) AND (requested_percent <= (100)::numeric))); `approval_requests_tenant_id_id_key` UNIQUE (id)

**Indexes:** `CREATE UNIQUE INDEX approval_requests_pkey ON app.approval_requests USING btree (id)`; `CREATE UNIQUE INDEX approval_one_pending ON app.approval_requests USING btree (approval_type, COALESCE(sale_id, project_id, design_job_id, financial_transaction_id, payment_received_id, payment_made_id, day_closing_id, product_id)) WHERE (status = 'pending'::app.approval_status)`; `CREATE INDEX approval_pending_idx ON app.approval_requests USING btree (approval_type, requested_at) WHERE (status = 'pending'::app.approval_status)`; `CREATE INDEX approval_requester_idx ON app.approval_requests USING btree (requested_by, requested_at DESC)`; `CREATE UNIQUE INDEX approval_requests_tenant_id_id_key ON app.approval_requests USING btree (id)`


## app.attachments

Typed exclusive-arc association (exactly one parent FK) instead of a polymorphic entity_type/entity_id pair, so every attachment has real referential integrity.

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| file_id | uuid | NO |  | → files |  |
| caption | text | yes |  |  |  |
| sale_id | uuid | yes |  | → sales |  |
| purchase_id | uuid | yes |  | → purchases |  |
| expense_id | uuid | yes |  | → expenses |  |
| payment_received_id | uuid | yes |  | → payments_received |  |
| payment_made_id | uuid | yes |  | → payments_made |  |
| customer_id | uuid | yes |  | → customers |  |
| supplier_id | uuid | yes |  | → suppliers |  |
| contractor_id | uuid | yes |  | → contractors |  |
| project_id | uuid | yes |  | → projects |  |
| design_job_id | uuid | yes |  | → design_jobs |  |
| task_id | uuid | yes |  | → tasks |  |
| employee_id | uuid | yes |  | → employees |  |
| contractor_bill_id | uuid | yes |  | → contractor_bills |  |
| stock_adjustment_id | uuid | yes |  | → stock_adjustments |  |
| created_at | timestamp with time zone | NO | now() |  |  |
| created_by | uuid | yes | app.current_membership_id() | → memberships |  |

**Constraints:** `attachments_caption_check` CHECK ((length(caption) <= 200)); `attachments_check` CHECK ((num_nonnulls(sale_id, purchase_id, expense_id, payment_received_id, payment_made_id, customer_id, supplier_id, contractor_id, project_id, design_job_id, task_id, employee_id, contractor_bill_id, stock_adjustment_id) = 1)); `attachments_tenant_id_id_key` UNIQUE (id)

**Indexes:** `CREATE UNIQUE INDEX attachments_pkey ON app.attachments USING btree (id)`; `CREATE UNIQUE INDEX attachments_tenant_id_id_key ON app.attachments USING btree (id)`


## app.attendance_corrections

Append-only. Spec §9: secretary corrects a record with a reason that is logged.

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| attendance_id | uuid | NO |  | → attendance_records |  |
| old_clock_in_at | timestamp with time zone | yes |  |  |  |
| new_clock_in_at | timestamp with time zone | yes |  |  |  |
| old_clock_out_at | timestamp with time zone | yes |  |  |  |
| new_clock_out_at | timestamp with time zone | yes |  |  |  |
| old_status | app.attendance_status | NO |  |  |  |
| new_status | app.attendance_status | NO |  |  |  |
| reason | text | NO |  |  |  |
| created_at | timestamp with time zone | NO | now() |  |  |
| created_by | uuid | NO | app.current_membership_id() | → memberships |  |

**Constraints:** `attendance_corrections_reason_check` CHECK ((length(btrim(reason)) >= 5)); `attendance_corrections_tenant_id_id_key` UNIQUE (id)

**Indexes:** `CREATE UNIQUE INDEX attendance_corrections_pkey ON app.attendance_corrections USING btree (id)`; `CREATE UNIQUE INDEX attendance_corrections_tenant_id_id_key ON app.attendance_corrections USING btree (id)`


## app.attendance_records

_(see spec section 5)_

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| employee_id | uuid | NO |  | → employees |  |
| work_date | date | NO |  |  |  |
| clock_in_at | timestamp with time zone | yes |  |  |  |
| clock_out_at | timestamp with time zone | yes |  |  |  |
| status | app.attendance_status | NO |  |  |  |
| source | app.attendance_source | NO | 'self'::app.attendance_source |  |  |
| minutes_late | integer | NO | 0 |  |  |
| overtime_minutes | integer | NO | 0 |  |  |
| clock_in_ip | inet | yes |  |  |  |
| is_corrected | boolean | NO | false |  |  |
| created_at | timestamp with time zone | NO | now() |  |  |
| created_by | uuid | yes | app.current_membership_id() | → memberships |  |
| updated_at | timestamp with time zone | NO | now() |  |  |
| updated_by | uuid | yes |  | → memberships |  |
| row_version | integer | NO | 1 |  |  |

**Constraints:** `attendance_records_check` CHECK (((clock_out_at IS NULL) OR ((clock_in_at IS NOT NULL) AND (clock_out_at > clock_in_at)))); `attendance_records_check1` CHECK (((status <> ALL (ARRAY['present'::app.attendance_status, 'late'::app.attendance_status, 'half_day'::app.attendance_status])) OR (clock_in_at IS NOT NULL))); `attendance_records_minutes_late_check` CHECK ((minutes_late >= 0)); `attendance_records_overtime_minutes_check` CHECK ((overtime_minutes >= 0)); `attendance_records_tenant_id_employee_id_work_date_key` UNIQUE (employee_id, work_date); `attendance_records_tenant_id_id_key` UNIQUE (id)

**Indexes:** `CREATE UNIQUE INDEX attendance_records_pkey ON app.attendance_records USING btree (id)`; `CREATE INDEX attendance_date_idx ON app.attendance_records USING btree (work_date)`; `CREATE UNIQUE INDEX attendance_records_tenant_id_employee_id_work_date_key ON app.attendance_records USING btree (employee_id, work_date)`; `CREATE UNIQUE INDEX attendance_records_tenant_id_id_key ON app.attendance_records USING btree (id)`


## app.authority_limits

Membership row overrides role row. No row for a permission holder = unlimited (Admin default).

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| role_id | uuid | yes |  | → roles |  |
| membership_id | uuid | yes |  | → memberships |  |
| limit_code | app.limit_code | NO |  |  |  |
| limit_value | numeric(18,2) | NO |  |  |  |
| created_at | timestamp with time zone | NO | now() |  |  |
| created_by | uuid | yes | app.current_membership_id() | → memberships |  |
| updated_at | timestamp with time zone | NO | now() |  |  |
| updated_by | uuid | yes |  | → memberships |  |
| row_version | integer | NO | 1 |  |  |

**Constraints:** `authority_limits_check` CHECK ((num_nonnulls(role_id, membership_id) = 1)); `authority_limits_check1` CHECK ((((limit_code)::text !~~ '%pct'::text) OR (limit_value <= (100)::numeric))); `authority_limits_limit_value_check` CHECK ((limit_value >= (0)::numeric)); `authority_limits_tenant_id_id_key` UNIQUE (id); `authority_limits_tenant_id_role_id_membership_id_limit_code_key` UNIQUE NULLS NOT DISTINCT (role_id, membership_id, limit_code)

**Indexes:** `CREATE UNIQUE INDEX authority_limits_pkey ON app.authority_limits USING btree (id)`; `CREATE UNIQUE INDEX authority_limits_tenant_id_id_key ON app.authority_limits USING btree (id)`; `CREATE UNIQUE INDEX authority_limits_tenant_id_role_id_membership_id_limit_code_key ON app.authority_limits USING btree (role_id, membership_id, limit_code) NULLS NOT DISTINCT`


## app.backup_runs

Backup screen + SOC 2 A1.2/A1.3 evidence. Platform rows (tenant_id null) are visible only to platform operators; tenant Admins see their own exports.

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| backup_kind | text | NO |  |  |  |
| status | app.backup_status | NO |  |  |  |
| started_at | timestamp with time zone | NO | now() |  |  |
| finished_at | timestamp with time zone | yes |  |  |  |
| size_bytes | bigint | yes |  |  |  |
| storage_uri | text | yes |  |  |  |
| checksum_sha256 | bytea | yes |  |  |  |
| verified_at | timestamp with time zone | yes |  |  |  |
| verified_by | text | yes |  |  |  |
| error | text | yes |  |  |  |

**Constraints:** `backup_runs_backup_kind_check` CHECK ((backup_kind = ANY (ARRAY['full'::text, 'incremental'::text, 'wal_archive_check'::text, 'restore_test'::text, 'tenant_export'::text]))); `backup_runs_check` CHECK (((status = 'running'::app.backup_status) OR (finished_at IS NOT NULL))); `backup_runs_checksum_sha256_check` CHECK ((octet_length(checksum_sha256) = 32)); `backup_runs_error_check` CHECK ((length(error) <= 2000)); `backup_runs_size_bytes_check` CHECK ((size_bytes >= 0)); `backup_runs_storage_uri_check` CHECK ((length(storage_uri) <= 500))

**Indexes:** `CREATE UNIQUE INDEX backup_runs_pkey ON app.backup_runs USING btree (id)`


## app.business_profile

_(see spec section 5)_

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | boolean | NO | true | PK |  |
| name | app.short_text | NO |  |  |  |
| timezone | text | NO | 'Africa/Dar_es_Salaam'::text |  |  |
| default_currency | app.currency | NO | 'TZS'::bpchar |  |  |
| default_locale | text | NO | 'sw'::text |  |  |
| status | text | NO | 'active'::text |  |  |
| created_at | timestamp with time zone | NO | now() |  |  |
| updated_at | timestamp with time zone | NO | now() |  |  |
| updated_by | uuid | yes |  |  |  |
| row_version | integer | NO | 1 |  |  |

**Constraints:** `business_profile_default_locale_check` CHECK ((default_locale = ANY (ARRAY['sw'::text, 'en'::text]))); `business_profile_id_check` CHECK (id); `business_profile_status_check` CHECK ((status = ANY (ARRAY['active'::text, 'suspended'::text, 'closed'::text])))

**Indexes:** `CREATE UNIQUE INDEX business_profile_pkey ON app.business_profile USING btree (id)`


## app.business_settings

Settings screens (spec §10): Business Profile, Finance and Tax, Documents, System. One row per tenant.

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| business_name | app.short_text | NO |  |  |  |
| logo_file_id | uuid | yes |  | → files |  |
| address | text | yes |  |  |  |
| phone | app.phone_e164 | yes |  |  |  |
| email | app.email | yes |  |  |  |
| tin | text | yes |  |  |  |
| vrn | text | yes |  |  |  |
| registration_no | text | yes |  |  |  |
| currency | app.currency | NO | 'TZS'::bpchar |  |  |
| date_format | text | NO | 'DD/MM/YYYY'::text |  |  |
| session_idle_minutes | smallint | NO | 30 |  |  |
| session_warning_seconds | smallint | NO | 60 |  |  |
| session_absolute_hours | smallint | NO | 12 |  |  |
| lockout_threshold | smallint | NO | 5 |  |  |
| lockout_minutes | smallint | NO | 15 |  |  |
| low_stock_default | numeric(14,3) | NO | 5 |  |  |
| allow_negative_stock | boolean | NO | false |  |  |
| approval_link_ttl_days | smallint | NO | 14 |  |  |
| renewal_reminder_days | smallint[] | NO | '{30,7}'::smallint[] |  |  |
| debt_reminder_days | smallint[] | NO | '{0,7,30}'::smallint[] |  |  |
| prices_include_tax | boolean | NO | true |  |  |
| require_sod_payroll | boolean | NO | true |  |  |
| receipt_footer | text | yes |  |  |  |
| invoice_footer | text | yes |  |  |  |
| quotation_terms | text | yes |  |  |  |
| payment_instructions | text | yes |  |  |  |
| updated_at | timestamp with time zone | NO | now() |  |  |
| updated_by | uuid | yes |  | → memberships |  |
| row_version | integer | NO | 1 |  |  |

**Constraints:** `tenant_settings_approval_link_ttl_days_check` CHECK (((approval_link_ttl_days >= 1) AND (approval_link_ttl_days <= 60))); `tenant_settings_date_format_check` CHECK ((date_format = ANY (ARRAY['DD/MM/YYYY'::text, 'DD MMM YYYY'::text, 'YYYY-MM-DD'::text]))); `tenant_settings_invoice_footer_check` CHECK ((length(invoice_footer) <= 1000)); `tenant_settings_lockout_minutes_check` CHECK (((lockout_minutes >= 5) AND (lockout_minutes <= 1440))); `tenant_settings_lockout_threshold_check` CHECK (((lockout_threshold >= 3) AND (lockout_threshold <= 20))); `tenant_settings_low_stock_default_check` CHECK ((low_stock_default >= (0)::numeric)); `tenant_settings_payment_instructions_check` CHECK ((length(payment_instructions) <= 1000)); `tenant_settings_quotation_terms_check` CHECK ((length(quotation_terms) <= 4000)); `tenant_settings_receipt_footer_check` CHECK ((length(receipt_footer) <= 500)); `tenant_settings_registration_no_check` CHECK ((length(registration_no) <= 50)); `tenant_settings_renewal_reminder_days_check` CHECK ((array_position(renewal_reminder_days, NULL::smallint) IS NULL)); `tenant_settings_session_absolute_hours_check` CHECK (((session_absolute_hours >= 1) AND (session_absolute_hours <= 720))); `tenant_settings_session_idle_minutes_check` CHECK (((session_idle_minutes >= 5) AND (session_idle_minutes <= 240))); `tenant_settings_session_warning_seconds_check` CHECK (((session_warning_seconds >= 15) AND (session_warning_seconds <= 600))); `tenant_settings_tin_check` CHECK ((tin ~ '^[0-9]{3}-?[0-9]{3}-?[0-9]{3}$'::text)); `tenant_settings_vrn_check` CHECK ((length(vrn) <= 20))

**Indexes:** `CREATE UNIQUE INDEX business_settings_single_row ON app.business_settings USING btree ((true))`


## app.business_units

Business A (shop) and Business B (services). Every money record carries business_unit_id for the A/B/both filter.

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| code | text | NO |  |  |  |
| name | app.short_text | NO |  |  |  |
| kind | app.business_unit_kind | NO |  |  |  |
| status | app.record_status | NO | 'active'::app.record_status |  |  |
| created_at | timestamp with time zone | NO | now() |  |  |
| created_by | uuid | yes | app.current_membership_id() | → memberships |  |
| updated_at | timestamp with time zone | NO | now() |  |  |
| updated_by | uuid | yes |  | → memberships |  |
| row_version | integer | NO | 1 |  |  |
| display_name | app.short_text | yes |  |  |  |
| logo_file_id | uuid | yes |  | → files |  |
| phone | app.phone_e164 | yes |  |  |  |
| address | text | yes |  |  |  |
| tin | text | yes |  |  |  |
| receipt_header | text | yes |  |  |  |
| receipt_footer | text | yes |  |  |  |

**Constraints:** `business_units_address_check` CHECK ((length(address) <= 500)); `business_units_code_check` CHECK ((code ~ '^[A-Z0-9]{1,10}$'::text)); `business_units_receipt_footer_check` CHECK ((length(receipt_footer) <= 500)); `business_units_receipt_header_check` CHECK ((length(receipt_header) <= 500)); `business_units_tenant_id_code_key` UNIQUE (code); `business_units_tenant_id_id_key` UNIQUE (id); `business_units_tin_check` CHECK ((length(tin) <= 40))

**Indexes:** `CREATE UNIQUE INDEX business_units_pkey ON app.business_units USING btree (id)`; `CREATE UNIQUE INDEX business_units_tenant_id_code_key ON app.business_units USING btree (code)`; `CREATE UNIQUE INDEX business_units_tenant_id_id_key ON app.business_units USING btree (id)`


## app.client_documents

_(see spec section 5)_

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| customer_id | uuid | NO |  | → customers |  |
| project_id | uuid | yes |  | → projects |  |
| project_stage_id | uuid | yes |  | → project_stages |  |
| document_type_id | uuid | yes |  | → document_types |  |
| title | app.short_text | NO |  |  |  |
| status | app.client_document_status | NO | 'missing'::app.client_document_status |  |  |
| file_id | uuid | yes |  | → files |  |
| is_final_deliverable | boolean | NO | false |  |  |
| received_at | timestamp with time zone | yes |  |  |  |
| received_by | uuid | yes |  | → memberships |  |
| notes | text | yes |  |  |  |
| created_at | timestamp with time zone | NO | now() |  |  |
| created_by | uuid | yes | app.current_membership_id() | → memberships |  |
| updated_at | timestamp with time zone | NO | now() |  |  |
| updated_by | uuid | yes |  | → memberships |  |
| row_version | integer | NO | 1 |  |  |

**Constraints:** `client_documents_check` CHECK (((status <> 'received'::app.client_document_status) OR ((received_at IS NOT NULL) AND (received_by IS NOT NULL)))); `client_documents_check1` CHECK (((project_stage_id IS NULL) OR (project_id IS NOT NULL))); `client_documents_notes_check` CHECK ((length(notes) <= 1000)); `client_documents_tenant_id_id_key` UNIQUE (id)

**Indexes:** `CREATE UNIQUE INDEX client_documents_pkey ON app.client_documents USING btree (id)`; `CREATE INDEX client_documents_customer_idx ON app.client_documents USING btree (customer_id, project_id)`; `CREATE UNIQUE INDEX client_documents_required_uq ON app.client_documents USING btree (project_stage_id, document_type_id) WHERE ((project_stage_id IS NOT NULL) AND (document_type_id IS NOT NULL))`; `CREATE UNIQUE INDEX client_documents_tenant_id_id_key ON app.client_documents USING btree (id)`


## app.client_interactions

_(see spec section 5)_

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| customer_id | uuid | NO |  | → customers |  |
| project_id | uuid | yes |  | → projects |  |
| kind | app.interaction_kind | NO |  |  |  |
| summary | text | NO |  |  |  |
| occurred_at | timestamp with time zone | NO | now() |  |  |
| created_at | timestamp with time zone | NO | now() |  |  |
| created_by | uuid | NO | app.current_membership_id() | → memberships |  |

**Constraints:** `client_interactions_summary_check` CHECK (((length(btrim(summary)) >= 1) AND (length(btrim(summary)) <= 4000))); `client_interactions_tenant_id_id_key` UNIQUE (id)

**Indexes:** `CREATE UNIQUE INDEX client_interactions_pkey ON app.client_interactions USING btree (id)`; `CREATE INDEX client_interactions_idx ON app.client_interactions USING btree (customer_id, occurred_at DESC)`; `CREATE UNIQUE INDEX client_interactions_tenant_id_id_key ON app.client_interactions USING btree (id)`


## app.commission_entries

_(see spec section 5)_

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| employee_id | uuid | NO |  | → employees |  |
| commission_rule_id | uuid | NO |  | → commission_rules |  |
| sale_id | uuid | yes |  | → sales |  |
| project_id | uuid | yes |  | → projects |  |
| sale_return_id | uuid | yes |  | → sale_returns |  |
| amount | app.money | NO |  |  |  |
| earned_on | date | NO |  |  |  |
| status | app.commission_status | NO | 'accrued'::app.commission_status |  |  |
| payroll_line_id | uuid | yes |  | → payroll_lines |  |
| created_at | timestamp with time zone | NO | now() |  |  |
| created_by | uuid | yes | app.current_membership_id() | → memberships |  |

**Constraints:** `commission_entries_amount_check` CHECK (((amount)::numeric <> (0)::numeric)); `commission_entries_check` CHECK ((num_nonnulls(sale_id, project_id) = 1)); `commission_entries_check1` CHECK (((status <> 'included'::app.commission_status) OR (payroll_line_id IS NOT NULL))); `commission_entries_check2` CHECK ((((amount)::numeric > (0)::numeric) OR (sale_return_id IS NOT NULL))); `commission_entries_tenant_id_id_key` UNIQUE (id)

**Indexes:** `CREATE UNIQUE INDEX commission_entries_pkey ON app.commission_entries USING btree (id)`; `CREATE UNIQUE INDEX commission_entries_tenant_id_id_key ON app.commission_entries USING btree (id)`; `CREATE UNIQUE INDEX commission_once ON app.commission_entries USING btree (commission_rule_id, employee_id, COALESCE(sale_id, project_id)) WHERE ((sale_return_id IS NULL) AND ((amount)::numeric > (0)::numeric))`; `CREATE INDEX commission_open_idx ON app.commission_entries USING btree (employee_id) WHERE (status = 'accrued'::app.commission_status)`


## app.commission_rules

_(see spec section 5)_

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| name | app.short_text | NO |  |  |  |
| basis | app.commission_basis | NO |  |  |  |
| percent | app.pct | yes |  |  |  |
| fixed_amount | app.money_pos | yes |  |  |  |
| product_category_id | uuid | yes |  | → product_categories |  |
| service_id | uuid | yes |  | → services |  |
| employee_id | uuid | yes |  | → employees |  |
| effective_from | date | NO |  |  |  |
| effective_to | date | yes |  |  |  |
| active | boolean | NO | true |  |  |
| created_at | timestamp with time zone | NO | now() |  |  |
| created_by | uuid | yes | app.current_membership_id() | → memberships |  |
| updated_at | timestamp with time zone | NO | now() |  |  |
| updated_by | uuid | yes |  | → memberships |  |
| row_version | integer | NO | 1 |  |  |

**Constraints:** `commission_rules_check` CHECK ((((basis = 'sale_percent'::app.commission_basis) AND (percent IS NOT NULL) AND (fixed_amount IS NULL) AND (service_id IS NULL)) OR ((basis = 'project_fixed'::app.commission_basis) AND (fixed_amount IS NOT NULL) AND (percent IS NULL) AND (product_category_id IS NULL)))); `commission_rules_check1` CHECK (((effective_to IS NULL) OR (effective_to > effective_from))); `commission_rules_tenant_id_id_key` UNIQUE (id)

**Indexes:** `CREATE UNIQUE INDEX commission_rules_pkey ON app.commission_rules USING btree (id)`; `CREATE UNIQUE INDEX commission_rules_tenant_id_id_key ON app.commission_rules USING btree (id)`


## app.contractor_bills

_(see spec section 5)_

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| bill_no | text | NO |  |  |  |
| contractor_id | uuid | NO |  | → contractors |  |
| business_unit_id | uuid | NO |  | → business_units |  |
| project_id | uuid | yes |  | → projects |  |
| design_job_id | uuid | yes |  | → design_jobs |  |
| print_order_id | uuid | yes |  | → print_orders |  |
| description | app.short_text | NO |  |  |  |
| amount | app.money_pos | NO |  |  |  |
| amount_paid | app.money_nn | NO | 0 |  |  |
| balance_due | numeric(18,2) | yes | ((amount)::numeric - (amount_paid)::numeric) |  |  |
| bill_date | date | NO |  |  |  |
| due_date | date | yes |  |  |  |
| status | app.bill_status | NO | 'open'::app.bill_status |  |  |
| created_at | timestamp with time zone | NO | now() |  |  |
| created_by | uuid | yes | app.current_membership_id() | → memberships |  |
| updated_at | timestamp with time zone | NO | now() |  |  |
| updated_by | uuid | yes |  | → memberships |  |
| row_version | integer | NO | 1 |  |  |

**Constraints:** `contractor_bills_check` CHECK (((amount_paid)::numeric <= (amount)::numeric)); `contractor_bills_tenant_id_bill_no_key` UNIQUE (bill_no); `contractor_bills_tenant_id_id_key` UNIQUE (id); `contractor_bills_tenant_id_print_order_id_key` UNIQUE (print_order_id)

**Indexes:** `CREATE UNIQUE INDEX contractor_bills_pkey ON app.contractor_bills USING btree (id)`; `CREATE INDEX contractor_bills_job_idx ON app.contractor_bills USING btree (design_job_id) WHERE (design_job_id IS NOT NULL)`; `CREATE INDEX contractor_bills_open_idx ON app.contractor_bills USING btree (contractor_id) WHERE (status = ANY (ARRAY['open'::app.bill_status, 'partially_paid'::app.bill_status]))`; `CREATE INDEX contractor_bills_project_idx ON app.contractor_bills USING btree (project_id) WHERE (project_id IS NOT NULL)`; `CREATE UNIQUE INDEX contractor_bills_tenant_id_bill_no_key ON app.contractor_bills USING btree (bill_no)`; `CREATE UNIQUE INDEX contractor_bills_tenant_id_id_key ON app.contractor_bills USING btree (id)`; `CREATE UNIQUE INDEX contractor_bills_tenant_id_print_order_id_key ON app.contractor_bills USING btree (print_order_id)`


## app.contractors

_(see spec section 5)_

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| name | app.short_text | NO |  |  |  |
| trade | app.short_text | NO |  |  |  |
| phone | app.phone_e164 | yes |  |  |  |
| email | app.email | yes |  |  |  |
| tin | text | yes |  |  |  |
| status | app.record_status | NO | 'active'::app.record_status |  |  |
| created_at | timestamp with time zone | NO | now() |  |  |
| created_by | uuid | yes | app.current_membership_id() | → memberships |  |
| updated_at | timestamp with time zone | NO | now() |  |  |
| updated_by | uuid | yes |  | → memberships |  |
| row_version | integer | NO | 1 |  |  |

**Constraints:** `contractors_tenant_id_id_key` UNIQUE (id); `contractors_tin_check` CHECK ((tin ~ '^[0-9]{3}-?[0-9]{3}-?[0-9]{3}$'::text))

**Indexes:** `CREATE UNIQUE INDEX contractors_pkey ON app.contractors USING btree (id)`; `CREATE UNIQUE INDEX contractors_name_uq ON app.contractors USING btree (lower((name)::text))`; `CREATE UNIQUE INDEX contractors_tenant_id_id_key ON app.contractors USING btree (id)`


## app.customers

Shop Customers + Services Clients and Leads (spec §6, §7). Walk-in sales have no customer row.

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| full_name | app.short_text | NO |  |  |  |
| business_name | text | yes |  |  |  |
| phone | app.phone_e164 | yes |  |  |  |
| email | app.email | yes |  |  |  |
| address | text | yes |  |  |  |
| national_id_ciphertext | bytea | yes |  |  |  |
| national_id_key_id | text | yes |  |  |  |
| national_id_hmac | bytea | yes |  |  |  |
| national_id_last4 | text | yes |  |  |  |
| is_shop_customer | boolean | NO | false |  |  |
| client_status | app.client_status | yes |  |  |  |
| lead_source_id | uuid | yes |  | → lead_sources |  |
| needs_summary | text | yes |  |  |  |
| notes | text | yes |  |  |  |
| credit_limit | app.money_nn | yes |  |  |  |
| registered_by | uuid | NO | app.current_membership_id() | → memberships |  |
| assigned_to | uuid | yes |  | → memberships |  |
| archived_at | timestamp with time zone | yes |  |  |  |
| created_at | timestamp with time zone | NO | now() |  |  |
| created_by | uuid | yes | app.current_membership_id() | → memberships |  |
| updated_at | timestamp with time zone | NO | now() |  |  |
| updated_by | uuid | yes |  | → memberships |  |
| row_version | integer | NO | 1 |  |  |

**Constraints:** `customers_address_check` CHECK ((length(address) <= 500)); `customers_business_name_check` CHECK ((length(business_name) <= 200)); `customers_check` CHECK ((is_shop_customer OR (client_status IS NOT NULL))); `customers_check1` CHECK (((national_id_ciphertext IS NULL) = (national_id_key_id IS NULL))); `customers_check2` CHECK (((national_id_ciphertext IS NULL) OR (national_id_hmac IS NOT NULL))); `customers_national_id_hmac_check` CHECK ((octet_length(national_id_hmac) = 32)); `customers_national_id_last4_check` CHECK ((national_id_last4 ~ '^[A-Z0-9]{4}$'::text)); `customers_needs_summary_check` CHECK ((length(needs_summary) <= 2000)); `customers_notes_check` CHECK ((length(notes) <= 4000)); `customers_tenant_id_id_key` UNIQUE (id)

**Indexes:** `CREATE UNIQUE INDEX customers_pkey ON app.customers USING btree (id)`; `CREATE INDEX customers_assigned_idx ON app.customers USING btree (assigned_to) WHERE (assigned_to IS NOT NULL)`; `CREATE INDEX customers_client_status_idx ON app.customers USING btree (client_status) WHERE (client_status IS NOT NULL)`; `CREATE INDEX customers_name_trgm ON app.customers USING gin (full_name public.gin_trgm_ops)`; `CREATE UNIQUE INDEX customers_natid_uq ON app.customers USING btree (national_id_hmac) WHERE ((national_id_hmac IS NOT NULL) AND (archived_at IS NULL))`; `CREATE INDEX customers_phone_trgm ON app.customers USING gin (phone public.gin_trgm_ops)`; `CREATE UNIQUE INDEX customers_phone_uq ON app.customers USING btree (phone) WHERE ((phone IS NOT NULL) AND (archived_at IS NULL))`; `CREATE INDEX customers_registered_idx ON app.customers USING btree (registered_by)`; `CREATE UNIQUE INDEX customers_tenant_id_id_key ON app.customers USING btree (id)`


## app.day_closing_lines

_(see spec section 5)_

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| day_closing_id | uuid | NO |  | PK → day_closings |  |
| account_id | uuid | NO |  | PK → financial_accounts |  |
| expected_amount | app.money | NO |  |  |  |
| counted_amount | app.money | NO |  |  |  |

**Constraints:**

**Indexes:** `CREATE UNIQUE INDEX day_closing_lines_pkey ON app.day_closing_lines USING btree (day_closing_id, account_id)`


## app.day_closings

_(see spec section 5)_

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| location_id | uuid | NO |  | → locations |  |
| business_date | date | NO |  |  |  |
| status | app.day_status | NO | 'open'::app.day_status |  |  |
| expected_cash | app.money | yes |  |  |  |
| counted_cash | app.money | yes |  |  |  |
| difference | numeric(18,2) | yes | ((counted_cash)::numeric - (expected_cash)::numeric) |  |  |
| note | text | yes |  |  |  |
| closed_by | uuid | yes |  | → memberships |  |
| closed_at | timestamp with time zone | yes |  |  |  |
| reopened_by | uuid | yes |  | → memberships |  |
| reopened_at | timestamp with time zone | yes |  |  |  |
| reopen_reason | text | yes |  |  |  |
| created_at | timestamp with time zone | NO | now() |  |  |
| created_by | uuid | yes | app.current_membership_id() | → memberships |  |
| updated_at | timestamp with time zone | NO | now() |  |  |
| updated_by | uuid | yes |  | → memberships |  |
| row_version | integer | NO | 1 |  |  |
| reopen_approval_id | uuid | yes |  | → approval_requests |  |

**Constraints:** `day_closings_check` CHECK (((status <> 'closed'::app.day_status) OR ((closed_by IS NOT NULL) AND (closed_at IS NOT NULL) AND (counted_cash IS NOT NULL) AND (expected_cash IS NOT NULL)))); `day_closings_check1` CHECK (((status <> 'closed'::app.day_status) OR ((counted_cash)::numeric = (expected_cash)::numeric) OR (note IS NOT NULL))); `day_closings_check2` CHECK (((status <> 'reopened'::app.day_status) OR ((reopened_by IS NOT NULL) AND (reopen_reason IS NOT NULL)))); `day_closings_note_check` CHECK ((length(note) <= 2000)); `day_closings_reopen_reason_check` CHECK ((length(reopen_reason) <= 1000)); `day_closings_tenant_id_id_key` UNIQUE (id); `day_closings_tenant_id_location_id_business_date_key` UNIQUE (location_id, business_date)

**Indexes:** `CREATE UNIQUE INDEX day_closings_pkey ON app.day_closings USING btree (id)`; `CREATE UNIQUE INDEX day_closings_tenant_id_id_key ON app.day_closings USING btree (id)`; `CREATE UNIQUE INDEX day_closings_tenant_id_location_id_business_date_key ON app.day_closings USING btree (location_id, business_date)`


## app.design_approval_links

Client approval page (spec §7A): no login, shows one version, valid 14 days (tenant setting), single decision. Token stored as SHA-256 only.

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| design_job_id | uuid | NO |  | → design_jobs |  |
| design_version_id | uuid | NO |  | → design_versions |  |
| token_hash | bytea | NO |  |  |  |
| expires_at | timestamp with time zone | NO |  |  |  |
| sent_via | app.message_channel | NO |  |  |  |
| first_opened_at | timestamp with time zone | yes |  |  |  |
| decided_at | timestamp with time zone | yes |  |  |  |
| decision | app.client_decision | yes |  |  |  |
| decision_ip | inet | yes |  |  |  |
| decision_user_agent | text | yes |  |  |  |
| revoked_at | timestamp with time zone | yes |  |  |  |
| created_at | timestamp with time zone | NO | now() |  |  |
| created_by | uuid | NO | app.current_membership_id() | → memberships |  |

**Constraints:** `design_approval_links_check` CHECK ((expires_at > created_at)); `design_approval_links_check1` CHECK (((decision IS NULL) = (decided_at IS NULL))); `design_approval_links_decision_user_agent_check` CHECK ((length(decision_user_agent) <= 512)); `design_approval_links_tenant_id_id_key` UNIQUE (id); `design_approval_links_token_hash_check` CHECK ((octet_length(token_hash) = 32)); `design_approval_links_token_hash_key` UNIQUE (token_hash)

**Indexes:** `CREATE UNIQUE INDEX design_approval_links_pkey ON app.design_approval_links USING btree (id)`; `CREATE UNIQUE INDEX design_approval_links_tenant_id_id_key ON app.design_approval_links USING btree (id)`; `CREATE UNIQUE INDEX design_approval_links_token_hash_key ON app.design_approval_links USING btree (token_hash)`; `CREATE UNIQUE INDEX design_links_one_live ON app.design_approval_links USING btree (design_version_id) WHERE ((decided_at IS NULL) AND (revoked_at IS NULL))`


## app.design_feedback

_(see spec section 5)_

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| design_job_id | uuid | NO |  | → design_jobs |  |
| design_version_id | uuid | NO |  | → design_versions |  |
| author_type | app.feedback_author | NO |  |  |  |
| author_id | uuid | yes |  | → memberships |  |
| approval_link_id | uuid | yes |  | → design_approval_links |  |
| comment | text | NO |  |  |  |
| created_at | timestamp with time zone | NO | now() |  |  |

**Constraints:** `design_feedback_check` CHECK ((((author_type = 'client'::app.feedback_author) AND (approval_link_id IS NOT NULL) AND (author_id IS NULL)) OR ((author_type = 'staff'::app.feedback_author) AND (author_id IS NOT NULL)))); `design_feedback_comment_check` CHECK (((length(btrim(comment)) >= 1) AND (length(btrim(comment)) <= 5000))); `design_feedback_tenant_id_id_key` UNIQUE (id)

**Indexes:** `CREATE UNIQUE INDEX design_feedback_pkey ON app.design_feedback USING btree (id)`; `CREATE INDEX design_feedback_job_idx ON app.design_feedback USING btree (design_job_id, created_at)`; `CREATE UNIQUE INDEX design_feedback_tenant_id_id_key ON app.design_feedback USING btree (id)`


## app.design_final_assets

_(see spec section 5)_

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| design_version_id | uuid | NO |  | → design_versions |  |
| asset_kind | app.design_asset_kind | NO |  |  |  |
| file_id | uuid | yes |  | → files |  |
| url | app.https_url | yes |  |  |  |
| created_at | timestamp with time zone | NO | now() |  |  |
| created_by | uuid | yes | app.current_membership_id() | → memberships |  |

**Constraints:** `design_final_assets_check` CHECK ((num_nonnulls(file_id, url) = 1)); `design_final_assets_check1` CHECK (((asset_kind <> 'source_link'::app.design_asset_kind) OR (url IS NOT NULL))); `design_final_assets_tenant_id_id_key` UNIQUE (id)

**Indexes:** `CREATE UNIQUE INDEX design_final_assets_pkey ON app.design_final_assets USING btree (id)`; `CREATE UNIQUE INDEX design_final_assets_tenant_id_id_key ON app.design_final_assets USING btree (id)`


## app.design_jobs

_(see spec section 5)_

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| job_no | text | NO |  |  |  |
| customer_id | uuid | NO |  | → customers |  |
| business_unit_id | uuid | NO |  | → business_units |  |
| project_id | uuid | yes |  | → projects |  |
| project_stage_id | uuid | yes |  | → project_stages |  |
| service_id | uuid | yes |  | → services |  |
| job_type | app.design_job_type | NO |  |  |  |
| title | app.short_text | NO |  |  |  |
| brief | text | NO |  |  |  |
| size_format | text | yes |  |  |  |
| medium | app.design_medium | NO | 'print'::app.design_medium |  |  |
| style_notes | text | yes |  |  |  |
| text_to_include | text | yes |  |  |  |
| deadline | date | yes |  |  |  |
| priority | app.priority | NO | 'normal'::app.priority |  |  |
| designer_id | uuid | yes |  | → memberships |  |
| price | app.money_nn | NO |  |  |  |
| revisions_included | smallint | NO | 0 |  |  |
| revisions_used | smallint | NO | 0 |  |  |
| extra_revision_fee | app.money_nn | NO | 0 |  |  |
| printing_needed | boolean | NO | false |  |  |
| status | app.design_job_status | NO | 'new_brief'::app.design_job_status |  |  |
| final_version_id | uuid | yes |  | → design_versions |  |
| approved_at | timestamp with time zone | yes |  |  |  |
| delivered_at | timestamp with time zone | yes |  |  |  |
| created_at | timestamp with time zone | NO | now() |  |  |
| created_by | uuid | yes | app.current_membership_id() | → memberships |  |
| updated_at | timestamp with time zone | NO | now() |  |  |
| updated_by | uuid | yes |  | → memberships |  |
| row_version | integer | NO | 1 |  |  |

**Constraints:** `design_jobs_brief_check` CHECK (((length(btrim(brief)) >= 1) AND (length(btrim(brief)) <= 10000))); `design_jobs_check` CHECK (((project_stage_id IS NULL) OR (project_id IS NOT NULL))); `design_jobs_check1` CHECK (((status <> ALL (ARRAY['approved'::app.design_job_status, 'printing'::app.design_job_status, 'delivered'::app.design_job_status])) OR ((final_version_id IS NOT NULL) AND (approved_at IS NOT NULL)))); `design_jobs_check2` CHECK (((status <> 'delivered'::app.design_job_status) OR (delivered_at IS NOT NULL))); `design_jobs_check3` CHECK (((status <> ALL (ARRAY['in_design'::app.design_job_status, 'internal_review'::app.design_job_status, 'with_client'::app.design_job_status])) OR (designer_id IS NOT NULL))); `design_jobs_revisions_included_check` CHECK (((revisions_included >= 0) AND (revisions_included <= 50))); `design_jobs_revisions_used_check` CHECK ((revisions_used >= 0)); `design_jobs_size_format_check` CHECK ((length(size_format) <= 100)); `design_jobs_style_notes_check` CHECK ((length(style_notes) <= 4000)); `design_jobs_tenant_id_id_key` UNIQUE (id); `design_jobs_tenant_id_job_no_key` UNIQUE (job_no); `design_jobs_text_to_include_check` CHECK ((length(text_to_include) <= 4000))

**Indexes:** `CREATE UNIQUE INDEX design_jobs_pkey ON app.design_jobs USING btree (id)`; `CREATE INDEX design_jobs_board_idx ON app.design_jobs USING btree (status, deadline)`; `CREATE INDEX design_jobs_designer_idx ON app.design_jobs USING btree (designer_id, status)`; `CREATE INDEX design_jobs_job_no_trgm ON app.design_jobs USING gin (job_no public.gin_trgm_ops)`; `CREATE INDEX design_jobs_stage_idx ON app.design_jobs USING btree (project_stage_id) WHERE (project_stage_id IS NOT NULL)`; `CREATE UNIQUE INDEX design_jobs_tenant_id_id_key ON app.design_jobs USING btree (id)`; `CREATE UNIQUE INDEX design_jobs_tenant_id_job_no_key ON app.design_jobs USING btree (job_no)`


## app.design_time_logs

_(see spec section 5)_

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| design_job_id | uuid | NO |  | → design_jobs |  |
| membership_id | uuid | NO | app.current_membership_id() | → memberships |  |
| started_at | timestamp with time zone | NO | now() |  |  |
| ended_at | timestamp with time zone | yes |  |  |  |

**Constraints:** `design_time_logs_check` CHECK (((ended_at IS NULL) OR (ended_at > started_at))); `design_time_logs_tenant_id_id_key` UNIQUE (id)

**Indexes:** `CREATE UNIQUE INDEX design_time_logs_pkey ON app.design_time_logs USING btree (id)`; `CREATE UNIQUE INDEX design_time_logs_tenant_id_id_key ON app.design_time_logs USING btree (id)`; `CREATE UNIQUE INDEX design_time_one_running ON app.design_time_logs USING btree (membership_id) WHERE (ended_at IS NULL)`


## app.design_versions

_(see spec section 5)_

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| design_job_id | uuid | NO |  | → design_jobs |  |
| version_no | smallint | NO |  |  |  |
| preview_file_id | uuid | NO |  | → files |  |
| working_file_url | app.https_url | yes |  |  |  |
| status | app.design_version_status | NO | 'draft'::app.design_version_status |  |  |
| sent_at | timestamp with time zone | yes |  |  |  |
| decided_at | timestamp with time zone | yes |  |  |  |
| created_at | timestamp with time zone | NO | now() |  |  |
| created_by | uuid | NO | app.current_membership_id() | → memberships |  |
| updated_at | timestamp with time zone | NO | now() |  |  |
| updated_by | uuid | yes |  | → memberships |  |
| row_version | integer | NO | 1 |  |  |

**Constraints:** `design_versions_tenant_id_design_job_id_version_no_key` UNIQUE (design_job_id, version_no); `design_versions_tenant_id_id_key` UNIQUE (id); `design_versions_version_no_check` CHECK ((version_no > 0))

**Indexes:** `CREATE UNIQUE INDEX design_versions_pkey ON app.design_versions USING btree (id)`; `CREATE UNIQUE INDEX design_versions_one_approved ON app.design_versions USING btree (design_job_id) WHERE (status = 'approved'::app.design_version_status)`; `CREATE UNIQUE INDEX design_versions_tenant_id_design_job_id_version_no_key ON app.design_versions USING btree (design_job_id, version_no)`; `CREATE UNIQUE INDEX design_versions_tenant_id_id_key ON app.design_versions USING btree (id)`


## app.devices

_(see spec section 5)_

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| location_id | uuid | NO |  | → locations |  |
| name | app.short_text | NO |  |  |  |
| device_type | app.device_type | NO |  |  |  |
| status | app.record_status | NO | 'active'::app.record_status |  |  |
| last_sync_at | timestamp with time zone | yes |  |  |  |
| created_at | timestamp with time zone | NO | now() |  |  |
| created_by | uuid | yes | app.current_membership_id() | → memberships |  |
| updated_at | timestamp with time zone | NO | now() |  |  |
| updated_by | uuid | yes |  | → memberships |  |
| row_version | integer | NO | 1 |  |  |

**Constraints:** `devices_tenant_id_id_key` UNIQUE (id)

**Indexes:** `CREATE UNIQUE INDEX devices_pkey ON app.devices USING btree (id)`; `CREATE UNIQUE INDEX devices_tenant_id_id_key ON app.devices USING btree (id)`


## app.document_sequences

R-0001, INV-0001, Q-0001... (spec §11). Row lock in app.next_document_number makes numbers gap-free and never repeated: a rolled-back transaction releases its number.

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| doc_type | app.doc_type | NO |  | PK |  |
| prefix | text | NO |  |  |  |
| next_value | bigint | NO | 1 |  |  |
| pad_width | smallint | NO | 4 |  |  |
| updated_at | timestamp with time zone | NO | now() |  |  |

**Constraints:** `document_sequences_next_value_check` CHECK ((next_value >= 1)); `document_sequences_pad_width_check` CHECK (((pad_width >= 3) AND (pad_width <= 10))); `document_sequences_prefix_check` CHECK ((prefix ~ '^[A-Z]{1,6}-$'::text))

**Indexes:** `CREATE UNIQUE INDEX document_sequences_pkey ON app.document_sequences USING btree (doc_type)`


## app.document_types

_(see spec section 5)_

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| name | app.short_text | NO |  |  |  |
| description | text | yes |  |  |  |
| contains_pii | boolean | NO | true |  |  |
| status | app.record_status | NO | 'active'::app.record_status |  |  |
| created_at | timestamp with time zone | NO | now() |  |  |
| created_by | uuid | yes | app.current_membership_id() | → memberships |  |

**Constraints:** `document_types_description_check` CHECK ((length(description) <= 1000)); `document_types_tenant_id_id_key` UNIQUE (id); `document_types_tenant_id_name_key` UNIQUE (name)

**Indexes:** `CREATE UNIQUE INDEX document_types_pkey ON app.document_types USING btree (id)`; `CREATE UNIQUE INDEX document_types_tenant_id_id_key ON app.document_types USING btree (id)`; `CREATE UNIQUE INDEX document_types_tenant_id_name_key ON app.document_types USING btree (name)`


## app.employee_advances

_(see spec section 5)_

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| employee_id | uuid | NO |  | → employees |  |
| kind | app.advance_kind | NO |  |  |  |
| principal | app.money_pos | NO |  |  |  |
| issued_on | date | NO |  |  |  |
| monthly_deduction | app.money_pos | NO |  |  |  |
| amount_repaid | app.money_nn | NO | 0 |  |  |
| balance | numeric(18,2) | yes | ((principal)::numeric - (amount_repaid)::numeric) |  |  |
| status | app.advance_status | NO | 'active'::app.advance_status |  |  |
| approved_by | uuid | NO |  | → memberships |  |
| payment_made_id | uuid | yes |  | → payments_made |  |
| notes | text | yes |  |  |  |
| created_at | timestamp with time zone | NO | now() |  |  |
| created_by | uuid | yes | app.current_membership_id() | → memberships |  |
| updated_at | timestamp with time zone | NO | now() |  |  |
| updated_by | uuid | yes |  | → memberships |  |
| row_version | integer | NO | 1 |  |  |

**Constraints:** `employee_advances_check` CHECK (((monthly_deduction)::numeric <= (principal)::numeric)); `employee_advances_check1` CHECK (((amount_repaid)::numeric <= (principal)::numeric)); `employee_advances_check2` CHECK (((status <> 'settled'::app.advance_status) OR ((amount_repaid)::numeric = (principal)::numeric))); `employee_advances_notes_check` CHECK ((length(notes) <= 1000)); `employee_advances_tenant_id_id_key` UNIQUE (id); `employee_advances_tenant_id_payment_made_id_key` UNIQUE (payment_made_id)

**Indexes:** `CREATE UNIQUE INDEX employee_advances_pkey ON app.employee_advances USING btree (id)`; `CREATE INDEX employee_advances_active_idx ON app.employee_advances USING btree (employee_id) WHERE (status = 'active'::app.advance_status)`; `CREATE UNIQUE INDEX employee_advances_tenant_id_id_key ON app.employee_advances USING btree (id)`; `CREATE UNIQUE INDEX employee_advances_tenant_id_payment_made_id_key ON app.employee_advances USING btree (payment_made_id)`


## app.employee_allowances

_(see spec section 5)_

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| employee_id | uuid | NO |  | → employees |  |
| name | app.short_text | NO |  |  |  |
| amount | app.money_pos | NO |  |  |  |
| effective_from | date | NO |  |  |  |
| effective_to | date | yes |  |  |  |
| created_at | timestamp with time zone | NO | now() |  |  |
| created_by | uuid | yes | app.current_membership_id() | → memberships |  |
| updated_at | timestamp with time zone | NO | now() |  |  |
| updated_by | uuid | yes |  | → memberships |  |
| row_version | integer | NO | 1 |  |  |

**Constraints:** `employee_allowances_check` CHECK (((effective_to IS NULL) OR (effective_to > effective_from))); `employee_allowances_tenant_id_id_key` UNIQUE (id)

**Indexes:** `CREATE UNIQUE INDEX employee_allowances_pkey ON app.employee_allowances USING btree (id)`; `CREATE UNIQUE INDEX employee_allowances_tenant_id_id_key ON app.employee_allowances USING btree (id)`


## app.employee_compensation

RESTRICTED. Salary history; new row per change (no in-place edits). Visible only with hr.payroll.view or to the employee themself.

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| employee_id | uuid | NO |  | → employees |  |
| effective_from | date | NO |  |  |  |
| effective_to | date | yes |  |  |  |
| basic_salary | app.money_nn | NO |  |  |  |
| pay_method | app.pay_method | NO |  |  |  |
| payout_account_ciphertext | bytea | yes |  |  |  |
| payout_key_id | text | yes |  |  |  |
| payout_account_last4 | text | yes |  |  |  |
| payout_provider | text | yes |  |  |  |
| reason | text | NO |  |  |  |
| created_at | timestamp with time zone | NO | now() |  |  |
| created_by | uuid | yes | app.current_membership_id() | → memberships |  |

**Constraints:** `employee_compensation_check` CHECK (((effective_to IS NULL) OR (effective_to > effective_from))); `employee_compensation_check1` CHECK (((pay_method = 'cash'::app.pay_method) OR (payout_account_ciphertext IS NOT NULL))); `employee_compensation_check2` CHECK (((payout_account_ciphertext IS NULL) = (payout_key_id IS NULL))); `employee_compensation_no_overlap` EXCLUDE USING gist (employee_id WITH =, daterange(effective_from, effective_to, '[)'::text) WITH &&); `employee_compensation_payout_account_last4_check` CHECK ((payout_account_last4 ~ '^[0-9]{4}$'::text)); `employee_compensation_payout_provider_check` CHECK ((length(payout_provider) <= 60)); `employee_compensation_reason_check` CHECK ((length(btrim(reason)) >= 3)); `employee_compensation_tenant_id_id_key` UNIQUE (id)

**Indexes:** `CREATE UNIQUE INDEX employee_compensation_pkey ON app.employee_compensation USING btree (id)`; `CREATE INDEX employee_compensation_no_overlap ON app.employee_compensation USING gist (employee_id, daterange(effective_from, effective_to, '[)'::text))`; `CREATE UNIQUE INDEX employee_compensation_tenant_id_id_key ON app.employee_compensation USING btree (id)`


## app.employee_pay_adjustments

_(see spec section 5)_

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| employee_id | uuid | NO |  | → employees |  |
| kind | app.pay_adjustment_kind | NO |  |  |  |
| amount | app.money_pos | NO |  |  |  |
| reason | text | NO |  |  |  |
| period_year | smallint | NO |  |  |  |
| period_month | smallint | NO |  |  |  |
| payroll_line_id | uuid | yes |  | → payroll_lines |  |
| created_at | timestamp with time zone | NO | now() |  |  |
| created_by | uuid | yes | app.current_membership_id() | → memberships |  |

**Constraints:** `employee_pay_adjustments_period_month_check` CHECK (((period_month >= 1) AND (period_month <= 12))); `employee_pay_adjustments_period_year_check` CHECK (((period_year >= 2020) AND (period_year <= 2100))); `employee_pay_adjustments_reason_check` CHECK ((length(btrim(reason)) >= 3)); `employee_pay_adjustments_tenant_id_id_key` UNIQUE (id)

**Indexes:** `CREATE UNIQUE INDEX employee_pay_adjustments_pkey ON app.employee_pay_adjustments USING btree (id)`; `CREATE UNIQUE INDEX employee_pay_adjustments_tenant_id_id_key ON app.employee_pay_adjustments USING btree (id)`


## app.employees

HR master record. Salaries are NOT here: see app.employee_compensation (restricted RLS) so Managers can manage attendance/leave without seeing pay.

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| employee_no | text | NO |  |  |  |
| membership_id | uuid | yes |  | → memberships |  |
| full_name | app.short_text | NO |  |  |  |
| phone | app.phone_e164 | yes |  |  |  |
| email | app.email | yes |  |  |  |
| position | app.short_text | NO |  |  |  |
| location_id | uuid | yes |  | → locations |  |
| start_date | date | NO |  |  |  |
| end_date | date | yes |  |  |  |
| status | app.employee_status | NO | 'active'::app.employee_status |  |  |
| national_id_ciphertext | bytea | yes |  |  |  |
| national_id_key_id | text | yes |  |  |  |
| national_id_last4 | text | yes |  |  |  |
| photo_file_id | uuid | yes |  | → files |  |
| emergency_contact_name | text | yes |  |  |  |
| emergency_contact_phone | app.phone_e164 | yes |  |  |  |
| created_at | timestamp with time zone | NO | now() |  |  |
| created_by | uuid | yes | app.current_membership_id() | → memberships |  |
| updated_at | timestamp with time zone | NO | now() |  |  |
| updated_by | uuid | yes |  | → memberships |  |
| row_version | integer | NO | 1 |  |  |
| business_unit_id | uuid | NO |  | → business_units |  |

**Constraints:** `employees_check` CHECK (((end_date IS NULL) OR (end_date >= start_date))); `employees_check1` CHECK (((status <> 'terminated'::app.employee_status) OR (end_date IS NOT NULL))); `employees_check2` CHECK (((national_id_ciphertext IS NULL) = (national_id_key_id IS NULL))); `employees_emergency_contact_name_check` CHECK ((length(emergency_contact_name) <= 200)); `employees_national_id_last4_check` CHECK ((national_id_last4 ~ '^[A-Z0-9]{4}$'::text)); `employees_tenant_id_employee_no_key` UNIQUE (employee_no); `employees_tenant_id_id_key` UNIQUE (id); `employees_tenant_id_membership_id_key` UNIQUE (membership_id)

**Indexes:** `CREATE UNIQUE INDEX employees_pkey ON app.employees USING btree (id)`; `CREATE INDEX employees_business_unit_idx ON app.employees USING btree (business_unit_id)`; `CREATE UNIQUE INDEX employees_tenant_id_employee_no_key ON app.employees USING btree (employee_no)`; `CREATE UNIQUE INDEX employees_tenant_id_id_key ON app.employees USING btree (id)`; `CREATE UNIQUE INDEX employees_tenant_id_membership_id_key ON app.employees USING btree (membership_id)`


## app.expense_categories

_(see spec section 5)_

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| name | app.short_text | NO |  |  |  |
| is_project_cost | boolean | NO | false |  |  |
| status | app.record_status | NO | 'active'::app.record_status |  |  |
| created_at | timestamp with time zone | NO | now() |  |  |
| created_by | uuid | yes | app.current_membership_id() | → memberships |  |

**Constraints:** `expense_categories_tenant_id_id_key` UNIQUE (id); `expense_categories_tenant_id_name_key` UNIQUE (name)

**Indexes:** `CREATE UNIQUE INDEX expense_categories_pkey ON app.expense_categories USING btree (id)`; `CREATE UNIQUE INDEX expense_categories_tenant_id_id_key ON app.expense_categories USING btree (id)`; `CREATE UNIQUE INDEX expense_categories_tenant_id_name_key ON app.expense_categories USING btree (name)`


## app.expenses

_(see spec section 5)_

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| expense_no | text | NO |  |  |  |
| category_id | uuid | NO |  | → expense_categories |  |
| description | app.short_text | NO |  |  |  |
| amount | app.money_pos | NO |  |  |  |
| expense_date | date | NO |  |  |  |
| business_unit_id | uuid | NO |  | → business_units |  |
| location_id | uuid | NO |  | → locations |  |
| project_id | uuid | yes |  | → projects |  |
| design_job_id | uuid | yes |  | → design_jobs |  |
| account_id | uuid | yes |  | → financial_accounts |  |
| receipt_file_id | uuid | yes |  | → files |  |
| status | app.expense_status | NO | 'submitted'::app.expense_status |  |  |
| requested_by | uuid | NO | app.current_membership_id() | → memberships |  |
| decided_by | uuid | yes |  | → memberships |  |
| decided_at | timestamp with time zone | yes |  |  |  |
| rejection_reason | text | yes |  |  |  |
| paid_at | timestamp with time zone | yes |  |  |  |
| paid_business_date | date | yes |  |  |  |
| recurring_expense_id | uuid | yes |  | → recurring_expenses |  |
| created_at | timestamp with time zone | NO | now() |  |  |
| created_by | uuid | yes | app.current_membership_id() | → memberships |  |
| updated_at | timestamp with time zone | NO | now() |  |  |
| updated_by | uuid | yes |  | → memberships |  |
| row_version | integer | NO | 1 |  |  |

**Constraints:** `expenses_check` CHECK (((status <> ALL (ARRAY['approved'::app.expense_status, 'rejected'::app.expense_status, 'paid'::app.expense_status])) OR ((decided_by IS NOT NULL) AND (decided_at IS NOT NULL)))); `expenses_check1` CHECK (((status <> 'rejected'::app.expense_status) OR (rejection_reason IS NOT NULL))); `expenses_check2` CHECK (((status <> 'paid'::app.expense_status) OR ((account_id IS NOT NULL) AND (paid_at IS NOT NULL) AND (paid_business_date IS NOT NULL)))); `expenses_check3` CHECK (((decided_by IS NULL) OR (decided_by <> requested_by) OR (recurring_expense_id IS NOT NULL))); `expenses_rejection_reason_check` CHECK ((length(rejection_reason) <= 1000)); `expenses_tenant_id_expense_no_key` UNIQUE (expense_no); `expenses_tenant_id_id_key` UNIQUE (id)

**Indexes:** `CREATE UNIQUE INDEX expenses_pkey ON app.expenses USING btree (id)`; `CREATE INDEX expenses_job_idx ON app.expenses USING btree (design_job_id) WHERE (design_job_id IS NOT NULL)`; `CREATE INDEX expenses_project_idx ON app.expenses USING btree (project_id) WHERE (project_id IS NOT NULL)`; `CREATE UNIQUE INDEX expenses_recurring_once ON app.expenses USING btree (recurring_expense_id, expense_date) WHERE (recurring_expense_id IS NOT NULL)`; `CREATE INDEX expenses_requester_idx ON app.expenses USING btree (requested_by)`; `CREATE INDEX expenses_status_idx ON app.expenses USING btree (status, expense_date DESC)`; `CREATE UNIQUE INDEX expenses_tenant_id_expense_no_key ON app.expenses USING btree (expense_no)`; `CREATE UNIQUE INDEX expenses_tenant_id_id_key ON app.expenses USING btree (id)`


## app.files

Metadata for objects in private, encrypted object storage (key prefix = tenant_id/). Bytes never live in Postgres. Download only through short-lived signed URLs after an authorization check.

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| storage_key | text | NO |  |  |  |
| original_name | text | NO |  |  |  |
| mime_type | text | NO |  |  |  |
| size_bytes | bigint | NO |  |  |  |
| sha256 | bytea | NO |  |  |  |
| classification | app.data_class | NO | 'confidential'::app.data_class |  |  |
| scan_status | app.scan_status | NO | 'pending'::app.scan_status |  |  |
| deleted_at | timestamp with time zone | yes |  |  |  |
| created_at | timestamp with time zone | NO | now() |  |  |
| created_by | uuid | yes | app.current_membership_id() | → memberships |  |

**Constraints:** `files_mime_type_check` CHECK ((mime_type ~ '^[a-z]+/[a-z0-9.+-]+$'::text)); `files_original_name_check` CHECK (((length(original_name) >= 1) AND (length(original_name) <= 255))); `files_sha256_check` CHECK ((octet_length(sha256) = 32)); `files_size_bytes_check` CHECK (((size_bytes > 0) AND (size_bytes <= 52428800))); `files_storage_key_check` CHECK (((storage_key ~ '^[A-Za-z0-9/_.-]+$'::text) AND ((length(storage_key) >= 10) AND (length(storage_key) <= 512)))); `files_tenant_id_id_key` UNIQUE (id); `files_tenant_id_storage_key_key` UNIQUE (storage_key)

**Indexes:** `CREATE UNIQUE INDEX files_pkey ON app.files USING btree (id)`; `CREATE INDEX files_tenant_created_idx ON app.files USING btree (created_at DESC)`; `CREATE UNIQUE INDEX files_tenant_id_id_key ON app.files USING btree (id)`; `CREATE UNIQUE INDEX files_tenant_id_storage_key_key ON app.files USING btree (storage_key)`


## app.financial_accounts

Cash drawers, bank and mobile-money accounts (spec §8). Balance = opening txn + sum of ledger; never edited directly.

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| name | app.short_text | NO |  |  |  |
| kind | app.account_kind | NO |  |  |  |
| provider | text | yes |  |  |  |
| account_number_masked | text | yes |  |  |  |
| location_id | uuid | yes |  | → locations |  |
| business_unit_id | uuid | yes |  | → business_units |  |
| currency | app.currency | NO | 'TZS'::bpchar |  |  |
| current_balance | app.money | NO | 0 |  |  |
| allow_negative | boolean | NO | false |  |  |
| show_on_invoices | boolean | NO | false |  |  |
| status | app.record_status | NO | 'active'::app.record_status |  |  |
| created_at | timestamp with time zone | NO | now() |  |  |
| created_by | uuid | yes | app.current_membership_id() | → memberships |  |
| updated_at | timestamp with time zone | NO | now() |  |  |
| updated_by | uuid | yes |  | → memberships |  |
| row_version | integer | NO | 1 |  |  |

**Constraints:** `financial_accounts_account_number_masked_check` CHECK ((account_number_masked ~ '^[*0-9 -]{4,40}$'::text)); `financial_accounts_check` CHECK ((allow_negative OR ((current_balance)::numeric >= (0)::numeric))); `financial_accounts_check1` CHECK (((kind <> ALL (ARRAY['cash'::app.account_kind, 'petty_cash'::app.account_kind])) OR (location_id IS NOT NULL))); `financial_accounts_provider_check` CHECK ((length(provider) <= 60)); `financial_accounts_tenant_id_id_key` UNIQUE (id); `financial_accounts_tenant_id_name_key` UNIQUE (name)

**Indexes:** `CREATE UNIQUE INDEX financial_accounts_pkey ON app.financial_accounts USING btree (id)`; `CREATE UNIQUE INDEX financial_accounts_tenant_id_id_key ON app.financial_accounts USING btree (id)`; `CREATE UNIQUE INDEX financial_accounts_tenant_id_name_key ON app.financial_accounts USING btree (name)`


## app.financial_transactions

Append-only cash-book ledger (spec §8: transactions cannot be deleted; mistakes fixed by an approved correction entry). Posted by triggers from source documents.

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| account_id | uuid | NO |  | → financial_accounts |  |
| direction | app.txn_direction | NO |  |  |  |
| amount | app.money_pos | NO |  |  |  |
| txn_type | app.txn_type | NO |  |  |  |
| business_unit_id | uuid | NO |  | → business_units |  |
| location_id | uuid | NO |  | → locations |  |
| business_date | date | NO |  |  |  |
| occurred_at | timestamp with time zone | NO | now() |  |  |
| description | text | NO |  |  |  |
| project_id | uuid | yes |  | → projects |  |
| design_job_id | uuid | yes |  | → design_jobs |  |
| payment_received_id | uuid | yes |  | → payments_received |  |
| payment_made_id | uuid | yes |  | → payments_made |  |
| expense_id | uuid | yes |  | → expenses |  |
| account_transfer_id | uuid | yes |  | → account_transfers |  |
| payroll_line_id | uuid | yes |  | → payroll_lines |  |
| reverses_transaction_id | uuid | yes |  | → financial_transactions |  |
| approval_request_id | uuid | yes |  | → approval_requests |  |
| created_at | timestamp with time zone | NO | now() |  |  |
| created_by | uuid | yes | app.current_membership_id() | → memberships |  |

**Constraints:** `financial_transactions_check` CHECK ((num_nonnulls(payment_received_id, payment_made_id, expense_id, account_transfer_id, payroll_line_id) <= 1)); `financial_transactions_check1` CHECK (((txn_type <> ALL (ARRAY['reversal'::app.txn_type, 'correction'::app.txn_type])) OR (reverses_transaction_id IS NOT NULL))); `financial_transactions_check2` CHECK (((txn_type <> 'correction'::app.txn_type) OR (approval_request_id IS NOT NULL))); `financial_transactions_check3` CHECK (((txn_type <> 'customer_receipt'::app.txn_type) OR ((payment_received_id IS NOT NULL) AND (direction = 'in'::app.txn_direction)))); `financial_transactions_check4` CHECK (((txn_type <> 'expense'::app.txn_type) OR (expense_id IS NOT NULL))); `financial_transactions_check5` CHECK (((txn_type <> 'transfer'::app.txn_type) OR (account_transfer_id IS NOT NULL))); `financial_transactions_check6` CHECK (((txn_type <> 'salary'::app.txn_type) OR (payroll_line_id IS NOT NULL))); `financial_transactions_description_check` CHECK (((length(description) >= 1) AND (length(description) <= 500))); `financial_transactions_tenant_id_id_key` UNIQUE (id)

**Indexes:** `CREATE UNIQUE INDEX financial_transactions_pkey ON app.financial_transactions USING btree (id)`; `CREATE INDEX fin_txn_account_idx ON app.financial_transactions USING btree (account_id, occurred_at DESC)`; `CREATE UNIQUE INDEX fin_txn_correction_once ON app.financial_transactions USING btree (approval_request_id) WHERE (txn_type = 'correction'::app.txn_type)`; `CREATE INDEX fin_txn_date_idx ON app.financial_transactions USING btree (business_date, business_unit_id)`; `CREATE INDEX fin_txn_project_idx ON app.financial_transactions USING btree (project_id) WHERE (project_id IS NOT NULL)`; `CREATE UNIQUE INDEX fin_txn_reversed_once ON app.financial_transactions USING btree (reverses_transaction_id) WHERE (txn_type = 'reversal'::app.txn_type)`; `CREATE UNIQUE INDEX fin_txn_salary_once ON app.financial_transactions USING btree (payroll_line_id) WHERE (txn_type = 'salary'::app.txn_type)`; `CREATE UNIQUE INDEX financial_transactions_tenant_id_id_key ON app.financial_transactions USING btree (id)`


## app.goods_receipt_line_units

_(see spec section 5)_

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| goods_receipt_line_id | uuid | NO |  | PK → goods_receipt_lines |  |
| product_unit_id | uuid | NO |  | PK → product_units |  |

**Constraints:** `goods_receipt_line_units_tenant_id_product_unit_id_key` UNIQUE (product_unit_id)

**Indexes:** `CREATE UNIQUE INDEX goods_receipt_line_units_pkey ON app.goods_receipt_line_units USING btree (goods_receipt_line_id, product_unit_id)`; `CREATE UNIQUE INDEX goods_receipt_line_units_tenant_id_product_unit_id_key ON app.goods_receipt_line_units USING btree (product_unit_id)`


## app.goods_receipt_lines

_(see spec section 5)_

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| goods_receipt_id | uuid | NO |  | → goods_receipts |  |
| purchase_line_id | uuid | NO |  | → purchase_lines |  |
| quantity | app.qty_pos | NO |  |  |  |
| unit_cost | app.money_nn | NO |  |  |  |

**Constraints:** `goods_receipt_lines_tenant_id_goods_receipt_id_purchase_lin_key` UNIQUE (goods_receipt_id, purchase_line_id); `goods_receipt_lines_tenant_id_id_key` UNIQUE (id)

**Indexes:** `CREATE UNIQUE INDEX goods_receipt_lines_pkey ON app.goods_receipt_lines USING btree (id)`; `CREATE UNIQUE INDEX goods_receipt_lines_tenant_id_goods_receipt_id_purchase_lin_key ON app.goods_receipt_lines USING btree (goods_receipt_id, purchase_line_id)`; `CREATE UNIQUE INDEX goods_receipt_lines_tenant_id_id_key ON app.goods_receipt_lines USING btree (id)`


## app.goods_receipts

_(see spec section 5)_

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| receipt_no | text | NO |  |  |  |
| purchase_id | uuid | NO |  | → purchases |  |
| location_id | uuid | NO |  | → locations |  |
| received_at | timestamp with time zone | NO | now() |  |  |
| business_date | date | NO |  |  |  |
| notes | text | yes |  |  |  |
| created_at | timestamp with time zone | NO | now() |  |  |
| created_by | uuid | yes | app.current_membership_id() | → memberships |  |

**Constraints:** `goods_receipts_notes_check` CHECK ((length(notes) <= 1000)); `goods_receipts_tenant_id_id_key` UNIQUE (id); `goods_receipts_tenant_id_receipt_no_key` UNIQUE (receipt_no)

**Indexes:** `CREATE UNIQUE INDEX goods_receipts_pkey ON app.goods_receipts USING btree (id)`; `CREATE UNIQUE INDEX goods_receipts_tenant_id_id_key ON app.goods_receipts USING btree (id)`; `CREATE UNIQUE INDEX goods_receipts_tenant_id_receipt_no_key ON app.goods_receipts USING btree (receipt_no)`


## app.idempotency_keys

_(see spec section 5)_

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| idempotency_key | text | NO |  | PK |  |
| membership_id | uuid | NO |  | → memberships |  |
| request_method | text | NO |  |  |  |
| request_path | text | NO |  |  |  |
| request_hash | bytea | NO |  |  |  |
| response_status | smallint | yes |  |  |  |
| response_body | jsonb | yes |  |  |  |
| created_at | timestamp with time zone | NO | now() |  |  |
| expires_at | timestamp with time zone | NO | (now() + '24:00:00'::interval) |  |  |

**Constraints:** `idempotency_keys_idempotency_key_check` CHECK (((length(idempotency_key) >= 8) AND (length(idempotency_key) <= 100))); `idempotency_keys_request_hash_check` CHECK ((octet_length(request_hash) = 32))

**Indexes:** `CREATE UNIQUE INDEX idempotency_keys_pkey ON app.idempotency_keys USING btree (idempotency_key)`; `CREATE INDEX idempotency_expiry_idx ON app.idempotency_keys USING btree (expires_at)`


## app.invoice_installments

_(see spec section 5)_

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| invoice_id | uuid | NO |  | → invoices |  |
| seq | smallint | NO |  |  |  |
| label | text | NO |  |  |  |
| due_date | date | NO |  |  |  |
| amount | app.money_pos | NO |  |  |  |

**Constraints:** `invoice_installments_label_check` CHECK ((label = ANY (ARRAY['deposit'::text, 'installment'::text, 'final'::text]))); `invoice_installments_seq_check` CHECK ((seq > 0)); `invoice_installments_tenant_id_id_key` UNIQUE (id); `invoice_installments_tenant_id_invoice_id_seq_key` UNIQUE (invoice_id, seq)

**Indexes:** `CREATE UNIQUE INDEX invoice_installments_pkey ON app.invoice_installments USING btree (id)`; `CREATE UNIQUE INDEX invoice_installments_tenant_id_id_key ON app.invoice_installments USING btree (id)`; `CREATE UNIQUE INDEX invoice_installments_tenant_id_invoice_id_seq_key ON app.invoice_installments USING btree (invoice_id, seq)`


## app.invoice_lines

_(see spec section 5)_

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| invoice_id | uuid | NO |  | → invoices |  |
| line_no | smallint | NO |  |  |  |
| service_id | uuid | yes |  | → services |  |
| description | app.short_text | NO |  |  |  |
| quantity | app.qty_pos | NO | 1 |  |  |
| unit_price | app.money_nn | NO |  |  |  |
| line_discount | app.money_nn | NO | 0 |  |  |
| line_total | app.money_nn | NO |  |  |  |

**Constraints:** `invoice_lines_check` CHECK (((line_total)::numeric = (round(((quantity)::numeric * (unit_price)::numeric), 2) - (line_discount)::numeric))); `invoice_lines_line_no_check` CHECK ((line_no > 0)); `invoice_lines_tenant_id_id_key` UNIQUE (id); `invoice_lines_tenant_id_invoice_id_line_no_key` UNIQUE (invoice_id, line_no)

**Indexes:** `CREATE UNIQUE INDEX invoice_lines_pkey ON app.invoice_lines USING btree (id)`; `CREATE UNIQUE INDEX invoice_lines_tenant_id_id_key ON app.invoice_lines USING btree (id)`; `CREATE UNIQUE INDEX invoice_lines_tenant_id_invoice_id_line_no_key ON app.invoice_lines USING btree (invoice_id, line_no)`


## app.invoices

_(see spec section 5)_

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| invoice_no | text | NO |  |  |  |
| customer_id | uuid | NO |  | → customers |  |
| business_unit_id | uuid | NO |  | → business_units |  |
| project_id | uuid | yes |  | → projects |  |
| quotation_id | uuid | yes |  | → quotations |  |
| status | app.invoice_status | NO | 'draft'::app.invoice_status |  |  |
| issue_date | date | NO |  |  |  |
| due_date | date | NO |  |  |  |
| subtotal | app.money_nn | NO | 0 |  |  |
| discount_amount | app.money_nn | NO | 0 |  |  |
| tax_amount | app.money_nn | NO | 0 |  |  |
| total | app.money_nn | NO | 0 |  |  |
| amount_paid | app.money_nn | NO | 0 |  |  |
| balance_due | numeric(18,2) | yes | ((total)::numeric - (amount_paid)::numeric) |  |  |
| notes | text | yes |  |  |  |
| issued_at | timestamp with time zone | yes |  |  |  |
| voided_at | timestamp with time zone | yes |  |  |  |
| void_reason | text | yes |  |  |  |
| created_at | timestamp with time zone | NO | now() |  |  |
| created_by | uuid | yes | app.current_membership_id() | → memberships |  |
| updated_at | timestamp with time zone | NO | now() |  |  |
| updated_by | uuid | yes |  | → memberships |  |
| row_version | integer | NO | 1 |  |  |
| design_job_id | uuid | yes |  | → design_jobs |  |

**Constraints:** `invoices_check` CHECK ((due_date >= issue_date)); `invoices_check1` CHECK (((total)::numeric = (((subtotal)::numeric - (discount_amount)::numeric) + (tax_amount)::numeric))); `invoices_check2` CHECK (((discount_amount)::numeric <= (subtotal)::numeric)); `invoices_check3` CHECK (((amount_paid)::numeric <= (total)::numeric)); `invoices_check4` CHECK (((status = 'draft'::app.invoice_status) OR (issued_at IS NOT NULL))); `invoices_check5` CHECK (((status <> 'void'::app.invoice_status) OR ((voided_at IS NOT NULL) AND (void_reason IS NOT NULL) AND ((amount_paid)::numeric = (0)::numeric)))); `invoices_draft_unpaid_check` CHECK (((status <> 'draft'::app.invoice_status) OR ((amount_paid)::numeric = (0)::numeric))); `invoices_issued_unpaid_check` CHECK (((status <> 'issued'::app.invoice_status) OR ((amount_paid)::numeric = (0)::numeric))); `invoices_notes_check` CHECK ((length(notes) <= 2000)); `invoices_paid_status_check` CHECK (((status <> 'paid'::app.invoice_status) OR (((total)::numeric > (0)::numeric) AND ((amount_paid)::numeric = (total)::numeric)))); `invoices_partial_status_check` CHECK (((status <> 'partially_paid'::app.invoice_status) OR (((amount_paid)::numeric > (0)::numeric) AND ((amount_paid)::numeric < (total)::numeric)))); `invoices_tenant_id_id_key` UNIQUE (id); `invoices_tenant_id_invoice_no_key` UNIQUE (invoice_no); `invoices_void_reason_check` CHECK ((length(void_reason) <= 1000))

**Indexes:** `CREATE UNIQUE INDEX invoices_pkey ON app.invoices USING btree (id)`; `CREATE INDEX invoices_customer_idx ON app.invoices USING btree (customer_id, issue_date DESC)`; `CREATE INDEX invoices_design_job_idx ON app.invoices USING btree (design_job_id) WHERE (design_job_id IS NOT NULL)`; `CREATE INDEX invoices_invoice_no_trgm ON app.invoices USING gin (invoice_no public.gin_trgm_ops)`; `CREATE INDEX invoices_open_idx ON app.invoices USING btree (due_date) WHERE (status = ANY (ARRAY['issued'::app.invoice_status, 'partially_paid'::app.invoice_status]))`; `CREATE INDEX invoices_project_idx ON app.invoices USING btree (project_id) WHERE (project_id IS NOT NULL)`; `CREATE UNIQUE INDEX invoices_tenant_id_id_key ON app.invoices USING btree (id)`; `CREATE UNIQUE INDEX invoices_tenant_id_invoice_no_key ON app.invoices USING btree (invoice_no)`


## app.job_queue

_(see spec section 5)_

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| kind | text | NO |  |  |  |
| payload | jsonb | NO | '{}'::jsonb |  |  |
| run_at | timestamp with time zone | NO | now() |  |  |
| attempts | integer | NO | 0 |  |  |
| max_attempts | integer | NO | 5 |  |  |
| status | text | NO | 'queued'::text |  |  |
| last_error | text | yes |  |  |  |
| locked_at | timestamp with time zone | yes |  |  |  |
| created_at | timestamp with time zone | NO | now() |  |  |
| updated_at | timestamp with time zone | NO | now() |  |  |

**Constraints:** `job_queue_kind_check` CHECK ((kind ~ '^[a-z_]{3,60}$'::text)); `job_queue_last_error_check` CHECK ((length(last_error) <= 2000)); `job_queue_status_check` CHECK ((status = ANY (ARRAY['queued'::text, 'running'::text, 'done'::text, 'failed'::text])))

**Indexes:** `CREATE UNIQUE INDEX job_queue_pkey ON app.job_queue USING btree (id)`; `CREATE INDEX job_queue_kind_status_idx ON app.job_queue USING btree (kind, status)`; `CREATE INDEX job_queue_ready_idx ON app.job_queue USING btree (run_at) WHERE (status = 'queued'::text)`


## app.lead_sources

_(see spec section 5)_

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| name | app.short_text | NO |  |  |  |
| status | app.record_status | NO | 'active'::app.record_status |  |  |
| created_at | timestamp with time zone | NO | now() |  |  |
| created_by | uuid | yes | app.current_membership_id() | → memberships |  |

**Constraints:** `lead_sources_tenant_id_id_key` UNIQUE (id); `lead_sources_tenant_id_name_key` UNIQUE (name)

**Indexes:** `CREATE UNIQUE INDEX lead_sources_pkey ON app.lead_sources USING btree (id)`; `CREATE UNIQUE INDEX lead_sources_tenant_id_id_key ON app.lead_sources USING btree (id)`; `CREATE UNIQUE INDEX lead_sources_tenant_id_name_key ON app.lead_sources USING btree (name)`


## app.leave_requests

_(see spec section 5)_

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| employee_id | uuid | NO |  | → employees |  |
| leave_type_id | uuid | NO |  | → leave_types |  |
| start_date | date | NO |  |  |  |
| end_date | date | NO |  |  |  |
| days | numeric(5,1) | NO |  |  |  |
| reason | text | yes |  |  |  |
| status | app.leave_status | NO | 'requested'::app.leave_status |  |  |
| decided_by | uuid | yes |  | → memberships |  |
| decided_at | timestamp with time zone | yes |  |  |  |
| decision_note | text | yes |  |  |  |
| created_at | timestamp with time zone | NO | now() |  |  |
| created_by | uuid | yes | app.current_membership_id() | → memberships |  |
| updated_at | timestamp with time zone | NO | now() |  |  |
| updated_by | uuid | yes |  | → memberships |  |
| row_version | integer | NO | 1 |  |  |

**Constraints:** `leave_no_overlap` EXCLUDE USING gist (employee_id WITH =, daterange(start_date, end_date, '[]'::text) WITH &&) WHERE ((status = ANY (ARRAY['requested'::app.leave_status, 'approved'::app.leave_status]))); `leave_requests_check` CHECK ((end_date >= start_date)); `leave_requests_check1` CHECK (((status = ANY (ARRAY['requested'::app.leave_status, 'cancelled'::app.leave_status])) OR ((decided_by IS NOT NULL) AND (decided_at IS NOT NULL)))); `leave_requests_days_check` CHECK ((days > (0)::numeric)); `leave_requests_decision_note_check` CHECK ((length(decision_note) <= 1000)); `leave_requests_reason_check` CHECK ((length(reason) <= 1000)); `leave_requests_tenant_id_id_key` UNIQUE (id)

**Indexes:** `CREATE UNIQUE INDEX leave_requests_pkey ON app.leave_requests USING btree (id)`; `CREATE INDEX leave_no_overlap ON app.leave_requests USING gist (employee_id, daterange(start_date, end_date, '[]'::text)) WHERE (status = ANY (ARRAY['requested'::app.leave_status, 'approved'::app.leave_status]))`; `CREATE UNIQUE INDEX leave_requests_tenant_id_id_key ON app.leave_requests USING btree (id)`


## app.leave_types

_(see spec section 5)_

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| name | app.short_text | NO |  |  |  |
| is_paid | boolean | NO | true |  |  |
| annual_days | numeric(5,1) | yes |  |  |  |
| status | app.record_status | NO | 'active'::app.record_status |  |  |
| created_at | timestamp with time zone | NO | now() |  |  |
| created_by | uuid | yes | app.current_membership_id() | → memberships |  |

**Constraints:** `leave_types_annual_days_check` CHECK ((annual_days >= (0)::numeric)); `leave_types_tenant_id_id_key` UNIQUE (id); `leave_types_tenant_id_name_key` UNIQUE (name)

**Indexes:** `CREATE UNIQUE INDEX leave_types_pkey ON app.leave_types USING btree (id)`; `CREATE UNIQUE INDEX leave_types_tenant_id_id_key ON app.leave_types USING btree (id)`; `CREATE UNIQUE INDEX leave_types_tenant_id_name_key ON app.leave_types USING btree (name)`


## app.legal_holds

While an active hold exists, retention/purge jobs and privacy deletions skip the held subject.

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| customer_id | uuid | yes |  | → customers |  |
| employee_id | uuid | yes |  | → employees |  |
| project_id | uuid | yes |  | → projects |  |
| reason | text | NO |  |  |  |
| placed_at | timestamp with time zone | NO | now() |  |  |
| placed_by | uuid | NO | app.current_membership_id() | → memberships |  |
| released_at | timestamp with time zone | yes |  |  |  |
| released_by | uuid | yes |  | → memberships |  |

**Constraints:** `legal_holds_check` CHECK ((num_nonnulls(customer_id, employee_id, project_id) = 1)); `legal_holds_reason_check` CHECK ((length(btrim(reason)) >= 5)); `legal_holds_tenant_id_id_key` UNIQUE (id)

**Indexes:** `CREATE UNIQUE INDEX legal_holds_pkey ON app.legal_holds USING btree (id)`; `CREATE UNIQUE INDEX legal_holds_tenant_id_id_key ON app.legal_holds USING btree (id)`


## app.locations

Branch / shop / office / store. Needed for stock transfers (spec §2) and per-till end-of-day closing.

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| business_unit_id | uuid | NO |  | → business_units |  |
| name | app.short_text | NO |  |  |  |
| kind | app.location_kind | NO |  |  |  |
| address | text | yes |  |  |  |
| phone | app.phone_e164 | yes |  |  |  |
| is_default | boolean | NO | false |  |  |
| status | app.record_status | NO | 'active'::app.record_status |  |  |
| created_at | timestamp with time zone | NO | now() |  |  |
| created_by | uuid | yes | app.current_membership_id() | → memberships |  |
| updated_at | timestamp with time zone | NO | now() |  |  |
| updated_by | uuid | yes |  | → memberships |  |
| row_version | integer | NO | 1 |  |  |
| logo_file_id | uuid | yes |  | → files |  |

**Constraints:** `locations_tenant_id_id_key` UNIQUE (id); `locations_tenant_id_name_key` UNIQUE (name)

**Indexes:** `CREATE UNIQUE INDEX locations_pkey ON app.locations USING btree (id)`; `CREATE UNIQUE INDEX locations_one_default ON app.locations USING btree ((true)) WHERE is_default`; `CREATE UNIQUE INDEX locations_tenant_id_id_key ON app.locations USING btree (id)`; `CREATE UNIQUE INDEX locations_tenant_id_name_key ON app.locations USING btree (name)`


## app.membership_business_units

The businesses a person may work in, on top of role permissions. Admins always have all of them.

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| membership_id | uuid | NO |  | PK → memberships |  |
| business_unit_id | uuid | NO |  | PK → business_units |  |
| is_primary | boolean | NO | false |  |  |
| created_at | timestamp with time zone | NO | now() |  |  |
| created_by | uuid | yes | app.current_membership_id() | → memberships |  |

**Constraints:**

**Indexes:** `CREATE UNIQUE INDEX membership_business_units_pkey ON app.membership_business_units USING btree (membership_id, business_unit_id)`; `CREATE UNIQUE INDEX membership_business_units_one_primary ON app.membership_business_units USING btree (membership_id) WHERE is_primary`; `CREATE INDEX membership_business_units_unit_idx ON app.membership_business_units USING btree (business_unit_id)`


## app.membership_locations

Branches a person is limited to. No rows means no limit beyond the branches of their businesses.

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| membership_id | uuid | NO |  | PK → memberships |  |
| location_id | uuid | NO |  | PK → locations |  |
| created_at | timestamp with time zone | NO | now() |  |  |
| created_by | uuid | yes | app.current_membership_id() | → memberships |  |

**Constraints:**

**Indexes:** `CREATE UNIQUE INDEX membership_locations_pkey ON app.membership_locations USING btree (membership_id, location_id)`; `CREATE INDEX membership_locations_location_idx ON app.membership_locations USING btree (location_id)`


## app.membership_permission_overrides

Per-user allow/deny on top of the role (spec §2A: Admin can change any permission for any individual user).

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| membership_id | uuid | NO |  | PK → memberships |  |
| permission_code | text | NO |  | PK → permissions |  |
| effect | app.permission_effect | NO |  |  |  |
| reason | text | NO |  |  |  |
| expires_at | timestamp with time zone | yes |  |  |  |
| created_at | timestamp with time zone | NO | now() |  |  |
| created_by | uuid | yes | app.current_membership_id() | → memberships |  |

**Constraints:** `membership_permission_overrides_reason_check` CHECK ((length(btrim(reason)) >= 3))

**Indexes:** `CREATE UNIQUE INDEX membership_permission_overrides_pkey ON app.membership_permission_overrides USING btree (membership_id, permission_code)`


## app.membership_roles

_(see spec section 5)_

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| membership_id | uuid | NO |  | PK → memberships |  |
| role_id | uuid | NO |  | PK → roles |  |
| created_at | timestamp with time zone | NO | now() |  |  |
| created_by | uuid | yes | app.current_membership_id() | → memberships |  |

**Constraints:** `membership_single_role` UNIQUE (membership_id)

**Indexes:** `CREATE UNIQUE INDEX membership_roles_pkey ON app.membership_roles USING btree (membership_id, role_id)`; `CREATE UNIQUE INDEX membership_single_role ON app.membership_roles USING btree (membership_id)`


## app.memberships

A user's seat in a tenant. All actor columns (created_by, approved_by, sold_by...) reference memberships, never users, so actions are always tenant-attributable. Users are deactivated, never deleted.

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| user_id | uuid | NO |  | → users |  |
| status | app.membership_status | NO | 'invited'::app.membership_status |  |  |
| default_location_id | uuid | yes |  | → locations |  |
| joined_at | timestamp with time zone | yes |  |  |  |
| deactivated_at | timestamp with time zone | yes |  |  |  |
| deactivation_reason | text | yes |  |  |  |
| created_at | timestamp with time zone | NO | now() |  |  |
| created_by | uuid | yes | app.current_membership_id() | → memberships |  |
| updated_at | timestamp with time zone | NO | now() |  |  |
| updated_by | uuid | yes |  | → memberships |  |
| row_version | integer | NO | 1 |  |  |

**Constraints:** `memberships_check` CHECK (((status <> ALL (ARRAY['revoked'::app.membership_status, 'suspended'::app.membership_status])) OR (deactivated_at IS NOT NULL))); `memberships_tenant_id_id_key` UNIQUE (id); `memberships_tenant_id_user_id_key` UNIQUE (user_id)

**Indexes:** `CREATE UNIQUE INDEX memberships_pkey ON app.memberships USING btree (id)`; `CREATE UNIQUE INDEX memberships_tenant_id_id_key ON app.memberships USING btree (id)`; `CREATE UNIQUE INDEX memberships_tenant_id_user_id_key ON app.memberships USING btree (user_id)`


## app.message_templates

_(see spec section 5)_

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| code | text | NO |  |  |  |
| channel | app.message_channel | NO |  |  |  |
| locale | text | NO |  |  |  |
| subject | text | yes |  |  |  |
| body | text | NO |  |  |  |
| active | boolean | NO | true |  |  |
| created_at | timestamp with time zone | NO | now() |  |  |
| created_by | uuid | yes | app.current_membership_id() | → memberships |  |
| updated_at | timestamp with time zone | NO | now() |  |  |
| updated_by | uuid | yes |  | → memberships |  |
| row_version | integer | NO | 1 |  |  |

**Constraints:** `message_templates_body_check` CHECK (((length(body) >= 1) AND (length(body) <= 2000))); `message_templates_code_check` CHECK ((code ~ '^[a-z_]{3,60}$'::text)); `message_templates_locale_check` CHECK ((locale = ANY (ARRAY['sw'::text, 'en'::text]))); `message_templates_subject_check` CHECK ((length(subject) <= 200)); `message_templates_tenant_id_code_channel_locale_key` UNIQUE (code, channel, locale); `message_templates_tenant_id_id_key` UNIQUE (id)

**Indexes:** `CREATE UNIQUE INDEX message_templates_pkey ON app.message_templates USING btree (id)`; `CREATE UNIQUE INDEX message_templates_tenant_id_code_channel_locale_key ON app.message_templates USING btree (code, channel, locale)`; `CREATE UNIQUE INDEX message_templates_tenant_id_id_key ON app.message_templates USING btree (id)`


## app.notification_rule_roles

_(see spec section 5)_

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| notification_type | app.notification_type | NO |  | PK → notification_rules |  |
| role_id | uuid | NO |  | PK → roles |  |

**Constraints:**

**Indexes:** `CREATE UNIQUE INDEX notification_rule_roles_pkey ON app.notification_rule_roles USING btree (notification_type, role_id)`


## app.notification_rules

_(see spec section 5)_

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| notification_type | app.notification_type | NO |  | PK |  |
| enabled | boolean | NO | true |  |  |
| in_app | boolean | NO | true |  |  |
| sms | boolean | NO | false |  |  |
| whatsapp | boolean | NO | false |  |  |
| email | boolean | NO | false |  |  |
| updated_at | timestamp with time zone | NO | now() |  |  |
| updated_by | uuid | yes |  | → memberships |  |

**Constraints:**

**Indexes:** `CREATE UNIQUE INDEX notification_rules_pkey ON app.notification_rules USING btree (notification_type)`


## app.notifications

_(see spec section 5)_

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| recipient_id | uuid | NO |  | → memberships |  |
| notification_type | app.notification_type | NO |  |  |  |
| title | app.short_text | NO |  |  |  |
| body | text | yes |  |  |  |
| link_path | text | yes |  |  |  |
| dedupe_key | text | NO |  |  |  |
| read_at | timestamp with time zone | yes |  |  |  |
| created_at | timestamp with time zone | NO | now() |  |  |

**Constraints:** `notifications_body_check` CHECK ((length(body) <= 1000)); `notifications_dedupe_key_check` CHECK ((length(dedupe_key) <= 200)); `notifications_link_path_check` CHECK (((length(link_path) <= 301) AND (link_path ~ '^/[A-Za-z0-9/_?=&.-]*$'::text))); `notifications_tenant_id_id_key` UNIQUE (id); `notifications_tenant_id_recipient_id_dedupe_key_key` UNIQUE (recipient_id, dedupe_key)

**Indexes:** `CREATE UNIQUE INDEX notifications_pkey ON app.notifications USING btree (id)`; `CREATE INDEX notifications_inbox_idx ON app.notifications USING btree (recipient_id, created_at DESC)`; `CREATE UNIQUE INDEX notifications_tenant_id_id_key ON app.notifications USING btree (id)`; `CREATE UNIQUE INDEX notifications_tenant_id_recipient_id_dedupe_key_key ON app.notifications USING btree (recipient_id, dedupe_key)`; `CREATE INDEX notifications_unread_idx ON app.notifications USING btree (recipient_id) WHERE (read_at IS NULL)`


## app.outbound_messages

Transactional outbox for SMS/WhatsApp/email. Written in the same transaction as the business change; a worker claims rows with FOR UPDATE SKIP LOCKED and retries with backoff.

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| channel | app.message_channel | NO |  |  |  |
| recipient | text | NO |  |  |  |
| template_id | uuid | yes |  | → message_templates |  |
| subject | text | yes |  |  |  |
| body | text | NO |  |  |  |
| attachment_file_id | uuid | yes |  | → files |  |
| status | app.outbound_status | NO | 'queued'::app.outbound_status |  |  |
| attempts | smallint | NO | 0 |  |  |
| next_attempt_at | timestamp with time zone | NO | now() |  |  |
| locked_until | timestamp with time zone | yes |  |  |  |
| last_error | text | yes |  |  |  |
| provider | text | yes |  |  |  |
| provider_message_id | text | yes |  |  |  |
| idempotency_key | text | NO |  |  |  |
| sent_at | timestamp with time zone | yes |  |  |  |
| delivered_at | timestamp with time zone | yes |  |  |  |
| customer_id | uuid | yes |  | → customers |  |
| sale_id | uuid | yes |  | → sales |  |
| invoice_id | uuid | yes |  | → invoices |  |
| quotation_id | uuid | yes |  | → quotations |  |
| payment_received_id | uuid | yes |  | → payments_received |  |
| project_id | uuid | yes |  | → projects |  |
| design_job_id | uuid | yes |  | → design_jobs |  |
| renewal_id | uuid | yes |  | → renewals |  |
| payslip_id | uuid | yes |  | → payslips |  |
| created_at | timestamp with time zone | NO | now() |  |  |
| created_by | uuid | yes | app.current_membership_id() | → memberships |  |

**Constraints:** `outbound_messages_attempts_check` CHECK (((attempts >= 0) AND (attempts <= 20))); `outbound_messages_body_check` CHECK (((length(body) >= 1) AND (length(body) <= 4000))); `outbound_messages_check` CHECK ((num_nonnulls(customer_id, sale_id, invoice_id, quotation_id, payment_received_id, project_id, design_job_id, renewal_id, payslip_id) >= 1)); `outbound_messages_check1` CHECK (((status <> ALL (ARRAY['sent'::app.outbound_status, 'delivered'::app.outbound_status])) OR (sent_at IS NOT NULL))); `outbound_messages_idempotency_key_check` CHECK ((length(idempotency_key) <= 200)); `outbound_messages_last_error_check` CHECK ((length(last_error) <= 1000)); `outbound_messages_provider_check` CHECK ((length(provider) <= 40)); `outbound_messages_provider_message_id_check` CHECK ((length(provider_message_id) <= 120)); `outbound_messages_recipient_check` CHECK (((length(recipient) >= 5) AND (length(recipient) <= 254))); `outbound_messages_subject_check` CHECK ((length(subject) <= 200)); `outbound_messages_tenant_id_id_key` UNIQUE (id); `outbound_messages_tenant_id_idempotency_key_key` UNIQUE (idempotency_key)

**Indexes:** `CREATE UNIQUE INDEX outbound_messages_pkey ON app.outbound_messages USING btree (id)`; `CREATE UNIQUE INDEX outbound_messages_tenant_id_id_key ON app.outbound_messages USING btree (id)`; `CREATE UNIQUE INDEX outbound_messages_tenant_id_idempotency_key_key ON app.outbound_messages USING btree (idempotency_key)`; `CREATE INDEX outbound_queue_idx ON app.outbound_messages USING btree (next_attempt_at) WHERE (status = ANY (ARRAY['queued'::app.outbound_status, 'failed'::app.outbound_status]))`


## app.payment_allocations

Links a receipt to the sale(s)/invoice(s) it settles. Split tender = several payments allocated to one sale; one payment can settle several debts.

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| payment_received_id | uuid | NO |  | → payments_received |  |
| sale_id | uuid | yes |  | → sales |  |
| invoice_id | uuid | yes |  | → invoices |  |
| amount | app.money_pos | NO |  |  |  |
| created_at | timestamp with time zone | NO | now() |  |  |
| created_by | uuid | yes | app.current_membership_id() | → memberships |  |

**Constraints:** `payment_allocations_check` CHECK ((num_nonnulls(sale_id, invoice_id) = 1)); `payment_allocations_tenant_id_id_key` UNIQUE (id); `payment_allocations_tenant_id_payment_received_id_sale_id_i_key` UNIQUE NULLS NOT DISTINCT (payment_received_id, sale_id, invoice_id)

**Indexes:** `CREATE UNIQUE INDEX payment_allocations_pkey ON app.payment_allocations USING btree (id)`; `CREATE INDEX payment_allocations_invoice_idx ON app.payment_allocations USING btree (invoice_id) WHERE (invoice_id IS NOT NULL)`; `CREATE INDEX payment_allocations_sale_idx ON app.payment_allocations USING btree (sale_id) WHERE (sale_id IS NOT NULL)`; `CREATE UNIQUE INDEX payment_allocations_tenant_id_id_key ON app.payment_allocations USING btree (id)`; `CREATE UNIQUE INDEX payment_allocations_tenant_id_payment_received_id_sale_id_i_key ON app.payment_allocations USING btree (payment_received_id, sale_id, invoice_id) NULLS NOT DISTINCT`


## app.payment_made_allocations

_(see spec section 5)_

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| payment_made_id | uuid | NO |  | → payments_made |  |
| purchase_id | uuid | yes |  | → purchases |  |
| contractor_bill_id | uuid | yes |  | → contractor_bills |  |
| sale_return_id | uuid | yes |  | → sale_returns |  |
| amount | app.money_pos | NO |  |  |  |
| created_at | timestamp with time zone | NO | now() |  |  |
| created_by | uuid | yes | app.current_membership_id() | → memberships |  |

**Constraints:** `payment_made_allocations_check` CHECK ((num_nonnulls(purchase_id, contractor_bill_id, sale_return_id) = 1)); `payment_made_allocations_tenant_id_id_key` UNIQUE (id); `payment_made_allocations_tenant_id_payment_made_id_purchase_key` UNIQUE NULLS NOT DISTINCT (payment_made_id, purchase_id, contractor_bill_id, sale_return_id)

**Indexes:** `CREATE UNIQUE INDEX payment_made_allocations_pkey ON app.payment_made_allocations USING btree (id)`; `CREATE UNIQUE INDEX payment_made_allocations_tenant_id_id_key ON app.payment_made_allocations USING btree (id)`; `CREATE UNIQUE INDEX payment_made_allocations_tenant_id_payment_made_id_purchase_key ON app.payment_made_allocations USING btree (payment_made_id, purchase_id, contractor_bill_id, sale_return_id) NULLS NOT DISTINCT`


## app.payments_made

_(see spec section 5)_

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| payment_no | text | NO |  |  |  |
| business_unit_id | uuid | NO |  | → business_units |  |
| location_id | uuid | NO |  | → locations |  |
| account_id | uuid | NO |  | → financial_accounts |  |
| method | app.payment_method | NO |  |  |  |
| amount | app.money_pos | NO |  |  |  |
| reference | text | yes |  |  |  |
| paid_at | timestamp with time zone | NO | now() |  |  |
| business_date | date | NO |  |  |  |
| supplier_id | uuid | yes |  | → suppliers |  |
| contractor_id | uuid | yes |  | → contractors |  |
| employee_id | uuid | yes |  | → employees |  |
| customer_id | uuid | yes |  | → customers |  |
| other_payee_name | text | yes |  |  |  |
| status | app.payment_status | NO | 'posted'::app.payment_status |  |  |
| voided_at | timestamp with time zone | yes |  |  |  |
| voided_by | uuid | yes |  | → memberships |  |
| void_reason | text | yes |  |  |  |
| client_ref | uuid | yes |  |  |  |
| notes | text | yes |  |  |  |
| created_at | timestamp with time zone | NO | now() |  |  |
| created_by | uuid | yes | app.current_membership_id() | → memberships |  |
| updated_at | timestamp with time zone | NO | now() |  |  |
| updated_by | uuid | yes |  | → memberships |  |
| row_version | integer | NO | 1 |  |  |

**Constraints:** `payments_made_check` CHECK ((num_nonnulls(supplier_id, contractor_id, employee_id, customer_id, other_payee_name) = 1)); `payments_made_check1` CHECK (((status <> 'voided'::app.payment_status) OR ((voided_at IS NOT NULL) AND (voided_by IS NOT NULL) AND (void_reason IS NOT NULL)))); `payments_made_notes_check` CHECK ((length(notes) <= 1000)); `payments_made_other_payee_name_check` CHECK (((length(btrim(other_payee_name)) >= 2) AND (length(btrim(other_payee_name)) <= 200))); `payments_made_reference_check` CHECK ((reference ~ '^[A-Za-z0-9/_.-]{3,60}$'::text)); `payments_made_tenant_id_client_ref_key` UNIQUE (client_ref); `payments_made_tenant_id_id_key` UNIQUE (id); `payments_made_tenant_id_payment_no_key` UNIQUE (payment_no); `payments_made_void_reason_check` CHECK ((length(void_reason) <= 1000))

**Indexes:** `CREATE UNIQUE INDEX payments_made_pkey ON app.payments_made USING btree (id)`; `CREATE INDEX payments_made_contractor_idx ON app.payments_made USING btree (contractor_id) WHERE (contractor_id IS NOT NULL)`; `CREATE INDEX payments_made_date_idx ON app.payments_made USING btree (business_date, location_id)`; `CREATE UNIQUE INDEX payments_made_ref_uq ON app.payments_made USING btree (method, reference) WHERE ((reference IS NOT NULL) AND (status = 'posted'::app.payment_status))`; `CREATE INDEX payments_made_supplier_idx ON app.payments_made USING btree (supplier_id) WHERE (supplier_id IS NOT NULL)`; `CREATE UNIQUE INDEX payments_made_tenant_id_client_ref_key ON app.payments_made USING btree (client_ref)`; `CREATE UNIQUE INDEX payments_made_tenant_id_id_key ON app.payments_made USING btree (id)`; `CREATE UNIQUE INDEX payments_made_tenant_id_payment_no_key ON app.payments_made USING btree (payment_no)`


## app.payments_received

_(see spec section 5)_

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| receipt_no | text | NO |  |  |  |
| customer_id | uuid | yes |  | → customers |  |
| business_unit_id | uuid | NO |  | → business_units |  |
| location_id | uuid | NO |  | → locations |  |
| account_id | uuid | NO |  | → financial_accounts |  |
| method | app.payment_method | NO |  |  |  |
| purpose | app.payment_purpose | NO |  |  |  |
| amount | app.money_pos | NO |  |  |  |
| reference | text | yes |  |  |  |
| received_at | timestamp with time zone | NO | now() |  |  |
| business_date | date | NO |  |  |  |
| received_by | uuid | NO | app.current_membership_id() | → memberships |  |
| client_ref | uuid | yes |  |  |  |
| status | app.payment_status | NO | 'posted'::app.payment_status |  |  |
| voided_at | timestamp with time zone | yes |  |  |  |
| voided_by | uuid | yes |  | → memberships |  |
| void_reason | text | yes |  |  |  |
| notes | text | yes |  |  |  |
| created_at | timestamp with time zone | NO | now() |  |  |
| created_by | uuid | yes | app.current_membership_id() | → memberships |  |
| updated_at | timestamp with time zone | NO | now() |  |  |
| updated_by | uuid | yes |  | → memberships |  |
| row_version | integer | NO | 1 |  |  |

**Constraints:** `payments_received_check` CHECK (((method = 'cash'::app.payment_method) OR (reference IS NOT NULL))); `payments_received_check1` CHECK (((customer_id IS NOT NULL) OR (purpose = 'sale'::app.payment_purpose))); `payments_received_check2` CHECK (((status <> 'voided'::app.payment_status) OR ((voided_at IS NOT NULL) AND (voided_by IS NOT NULL) AND (void_reason IS NOT NULL)))); `payments_received_notes_check` CHECK ((length(notes) <= 1000)); `payments_received_reference_check` CHECK ((reference ~ '^[A-Za-z0-9/_.-]{3,60}$'::text)); `payments_received_tenant_id_client_ref_key` UNIQUE (client_ref); `payments_received_tenant_id_id_key` UNIQUE (id); `payments_received_tenant_id_receipt_no_key` UNIQUE (receipt_no); `payments_received_void_reason_check` CHECK ((length(void_reason) <= 1000))

**Indexes:** `CREATE UNIQUE INDEX payments_received_pkey ON app.payments_received USING btree (id)`; `CREATE INDEX payments_received_by_idx ON app.payments_received USING btree (received_by, business_date DESC)`; `CREATE INDEX payments_received_customer_idx ON app.payments_received USING btree (customer_id, received_at DESC)`; `CREATE INDEX payments_received_date_idx ON app.payments_received USING btree (business_date, location_id)`; `CREATE UNIQUE INDEX payments_received_ref_uq ON app.payments_received USING btree (method, reference) WHERE ((reference IS NOT NULL) AND (status = 'posted'::app.payment_status))`; `CREATE UNIQUE INDEX payments_received_tenant_id_client_ref_key ON app.payments_received USING btree (client_ref)`; `CREATE UNIQUE INDEX payments_received_tenant_id_id_key ON app.payments_received USING btree (id)`; `CREATE UNIQUE INDEX payments_received_tenant_id_receipt_no_key ON app.payments_received USING btree (receipt_no)`


## app.payroll_lines

_(see spec section 5)_

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| payroll_run_id | uuid | NO |  | → payroll_runs |  |
| employee_id | uuid | NO |  | → employees |  |
| basic_salary | app.money_nn | NO | 0 |  |  |
| allowances | app.money_nn | NO | 0 |  |  |
| commission | app.money_nn | NO | 0 |  |  |
| overtime | app.money_nn | NO | 0 |  |  |
| bonuses | app.money_nn | NO | 0 |  |  |
| deductions | app.money_nn | NO | 0 |  |  |
| advance_repayment | app.money_nn | NO | 0 |  |  |
| statutory_deductions | app.money_nn | NO | 0 |  |  |
| gross_pay | numeric(18,2) | yes | (((((basic_salary)::numeric + (allowances)::numeric) + (commission)::numeric) + (overtime)::numeric) + (bonuses)::numeric) |  |  |
| net_pay | numeric(18,2) | yes | ((((((((basic_salary)::numeric + (allowances)::numeric) + (commission)::numeric) + (overtime)::numeric) + (bonuses)::numeric) - (deductions)::numeric) - (advance_repayment)::numeric) - (statutory_deductions)::numeric) |  |  |
| pay_method | app.pay_method | NO |  |  |  |
| payment_reference | text | yes |  |  |  |
| paid_at | timestamp with time zone | yes |  |  |  |
| adjustment_note | text | yes |  |  |  |
| created_at | timestamp with time zone | NO | now() |  |  |
| created_by | uuid | yes | app.current_membership_id() | → memberships |  |
| updated_at | timestamp with time zone | NO | now() |  |  |
| updated_by | uuid | yes |  | → memberships |  |
| row_version | integer | NO | 1 |  |  |

**Constraints:** `payroll_lines_adjustment_note_check` CHECK ((length(adjustment_note) <= 1000)); `payroll_lines_check` CHECK ((((((((((basic_salary)::numeric + (allowances)::numeric) + (commission)::numeric) + (overtime)::numeric) + (bonuses)::numeric) - (deductions)::numeric) - (advance_repayment)::numeric) - (statutory_deductions)::numeric) >= (0)::numeric)); `payroll_lines_payment_reference_check` CHECK ((length(payment_reference) <= 60)); `payroll_lines_tenant_id_id_key` UNIQUE (id); `payroll_lines_tenant_id_payroll_run_id_employee_id_key` UNIQUE (payroll_run_id, employee_id)

**Indexes:** `CREATE UNIQUE INDEX payroll_lines_pkey ON app.payroll_lines USING btree (id)`; `CREATE INDEX payroll_lines_employee_idx ON app.payroll_lines USING btree (employee_id)`; `CREATE UNIQUE INDEX payroll_lines_tenant_id_id_key ON app.payroll_lines USING btree (id)`; `CREATE UNIQUE INDEX payroll_lines_tenant_id_payroll_run_id_employee_id_key ON app.payroll_lines USING btree (payroll_run_id, employee_id)`


## app.payroll_runs

_(see spec section 5)_

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| period_year | smallint | NO |  |  |  |
| period_month | smallint | NO |  |  |  |
| status | app.payroll_status | NO | 'draft'::app.payroll_status |  |  |
| account_id | uuid | yes |  | → financial_accounts |  |
| location_id | uuid | NO |  | → locations |  |
| business_unit_id | uuid | NO |  | → business_units |  |
| generated_by | uuid | NO | app.current_membership_id() | → memberships |  |
| generated_at | timestamp with time zone | NO | now() |  |  |
| approved_by | uuid | yes |  | → memberships |  |
| approved_at | timestamp with time zone | yes |  |  |  |
| paid_by | uuid | yes |  | → memberships |  |
| paid_at | timestamp with time zone | yes |  |  |  |
| paid_business_date | date | yes |  |  |  |
| notes | text | yes |  |  |  |
| created_at | timestamp with time zone | NO | now() |  |  |
| created_by | uuid | yes | app.current_membership_id() | → memberships |  |
| updated_at | timestamp with time zone | NO | now() |  |  |
| updated_by | uuid | yes |  | → memberships |  |
| row_version | integer | NO | 1 |  |  |

**Constraints:** `payroll_runs_check` CHECK (((status <> ALL (ARRAY['approved'::app.payroll_status, 'paid'::app.payroll_status])) OR ((approved_by IS NOT NULL) AND (approved_at IS NOT NULL)))); `payroll_runs_check1` CHECK (((status <> 'paid'::app.payroll_status) OR ((paid_by IS NOT NULL) AND (paid_at IS NOT NULL) AND (account_id IS NOT NULL) AND (paid_business_date IS NOT NULL)))); `payroll_runs_notes_check` CHECK ((length(notes) <= 2000)); `payroll_runs_period_month_check` CHECK (((period_month >= 1) AND (period_month <= 12))); `payroll_runs_period_year_check` CHECK (((period_year >= 2020) AND (period_year <= 2100))); `payroll_runs_tenant_id_id_key` UNIQUE (id)

**Indexes:** `CREATE UNIQUE INDEX payroll_runs_pkey ON app.payroll_runs USING btree (id)`; `CREATE UNIQUE INDEX payroll_runs_one_per_period ON app.payroll_runs USING btree (period_year, period_month) WHERE (status <> 'cancelled'::app.payroll_status)`; `CREATE UNIQUE INDEX payroll_runs_tenant_id_id_key ON app.payroll_runs USING btree (id)`


## app.payroll_statutory_items

Breakdown of payroll_lines.statutory_deductions. Rates are NOT encoded here: Tanzanian PAYE/NSSF/WCF/SDL rules need a stakeholder-approved calculator (decision D-08).

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| payroll_line_id | uuid | NO |  | → payroll_lines |  |
| code | text | NO |  |  |  |
| employee_amount | app.money_nn | NO | 0 |  |  |
| employer_amount | app.money_nn | NO | 0 |  |  |

**Constraints:** `payroll_statutory_items_code_check` CHECK ((code ~ '^[A-Z_]{2,20}$'::text)); `payroll_statutory_items_tenant_id_id_key` UNIQUE (id); `payroll_statutory_items_tenant_id_payroll_line_id_code_key` UNIQUE (payroll_line_id, code)

**Indexes:** `CREATE UNIQUE INDEX payroll_statutory_items_pkey ON app.payroll_statutory_items USING btree (id)`; `CREATE UNIQUE INDEX payroll_statutory_items_tenant_id_id_key ON app.payroll_statutory_items USING btree (id)`; `CREATE UNIQUE INDEX payroll_statutory_items_tenant_id_payroll_line_id_code_key ON app.payroll_statutory_items USING btree (payroll_line_id, code)`


## app.payslips

_(see spec section 5)_

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| payslip_no | text | NO |  |  |  |
| payroll_line_id | uuid | NO |  | → payroll_lines |  |
| file_id | uuid | yes |  | → files |  |
| issued_at | timestamp with time zone | NO | now() |  |  |
| created_at | timestamp with time zone | NO | now() |  |  |
| created_by | uuid | yes | app.current_membership_id() | → memberships |  |

**Constraints:** `payslips_tenant_id_id_key` UNIQUE (id); `payslips_tenant_id_payroll_line_id_key` UNIQUE (payroll_line_id); `payslips_tenant_id_payslip_no_key` UNIQUE (payslip_no)

**Indexes:** `CREATE UNIQUE INDEX payslips_pkey ON app.payslips USING btree (id)`; `CREATE UNIQUE INDEX payslips_tenant_id_id_key ON app.payslips USING btree (id)`; `CREATE UNIQUE INDEX payslips_tenant_id_payroll_line_id_key ON app.payslips USING btree (payroll_line_id)`; `CREATE UNIQUE INDEX payslips_tenant_id_payslip_no_key ON app.payslips USING btree (payslip_no)`


## app.permissions

Global catalogue of permission codes (module.resource.action). Seeded in 160.

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| code | text | NO |  | PK |  |
| module | text | NO |  |  |  |
| description | text | NO |  |  |  |
| is_sensitive | boolean | NO | false |  |  |

**Constraints:** `permissions_code_check` CHECK ((code ~ '^[a-z_]+(\.[a-z_]+){1,3}$'::text))

**Indexes:** `CREATE UNIQUE INDEX permissions_pkey ON app.permissions USING btree (code)`


## app.petty_cash_reconciliations

_(see spec section 5)_

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| account_id | uuid | NO |  | → financial_accounts |  |
| period_start | date | NO |  |  |  |
| period_end | date | NO |  |  |  |
| opening_balance | app.money | NO |  |  |  |
| expected_closing | app.money | NO |  |  |  |
| counted_closing | app.money | NO |  |  |  |
| difference | numeric(18,2) | yes | ((counted_closing)::numeric - (expected_closing)::numeric) |  |  |
| note | text | yes |  |  |  |
| created_at | timestamp with time zone | NO | now() |  |  |
| created_by | uuid | yes | app.current_membership_id() | → memberships |  |

**Constraints:** `petty_cash_reconciliations_check` CHECK ((period_end >= period_start)); `petty_cash_reconciliations_check1` CHECK ((((counted_closing)::numeric = (expected_closing)::numeric) OR (note IS NOT NULL))); `petty_cash_reconciliations_note_check` CHECK ((length(note) <= 1000)); `petty_cash_reconciliations_tenant_id_account_id_period_end_key` UNIQUE (account_id, period_end); `petty_cash_reconciliations_tenant_id_id_key` UNIQUE (id)

**Indexes:** `CREATE UNIQUE INDEX petty_cash_reconciliations_pkey ON app.petty_cash_reconciliations USING btree (id)`; `CREATE UNIQUE INDEX petty_cash_reconciliations_tenant_id_account_id_period_end_key ON app.petty_cash_reconciliations USING btree (account_id, period_end)`; `CREATE UNIQUE INDEX petty_cash_reconciliations_tenant_id_id_key ON app.petty_cash_reconciliations USING btree (id)`


## app.print_orders

_(see spec section 5)_

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| design_job_id | uuid | NO |  | → design_jobs |  |
| contractor_id | uuid | NO |  | → contractors |  |
| quantity | integer | NO |  |  |  |
| cost | app.money_nn | NO |  |  |  |
| due_date | date | yes |  |  |  |
| status | app.print_order_status | NO | 'sent_to_print'::app.print_order_status |  |  |
| notes | text | yes |  |  |  |
| created_at | timestamp with time zone | NO | now() |  |  |
| created_by | uuid | yes | app.current_membership_id() | → memberships |  |
| updated_at | timestamp with time zone | NO | now() |  |  |
| updated_by | uuid | yes |  | → memberships |  |
| row_version | integer | NO | 1 |  |  |

**Constraints:** `print_orders_notes_check` CHECK ((length(notes) <= 1000)); `print_orders_quantity_check` CHECK ((quantity > 0)); `print_orders_tenant_id_id_key` UNIQUE (id)

**Indexes:** `CREATE UNIQUE INDEX print_orders_pkey ON app.print_orders USING btree (id)`; `CREATE UNIQUE INDEX print_orders_tenant_id_id_key ON app.print_orders USING btree (id)`


## app.privacy_requests

_(see spec section 5)_

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| request_type | app.privacy_request_type | NO |  |  |  |
| customer_id | uuid | yes |  | → customers |  |
| employee_id | uuid | yes |  | → employees |  |
| status | app.privacy_request_status | NO | 'received'::app.privacy_request_status |  |  |
| received_at | timestamp with time zone | NO | now() |  |  |
| due_at | timestamp with time zone | NO |  |  |  |
| completed_at | timestamp with time zone | yes |  |  |  |
| outcome_note | text | yes |  |  |  |
| handled_by | uuid | yes |  | → memberships |  |
| created_at | timestamp with time zone | NO | now() |  |  |
| created_by | uuid | yes | app.current_membership_id() | → memberships |  |

**Constraints:** `privacy_requests_check` CHECK ((num_nonnulls(customer_id, employee_id) = 1)); `privacy_requests_check1` CHECK (((status <> ALL (ARRAY['completed'::app.privacy_request_status, 'rejected'::app.privacy_request_status])) OR (completed_at IS NOT NULL))); `privacy_requests_outcome_note_check` CHECK ((length(outcome_note) <= 2000)); `privacy_requests_tenant_id_id_key` UNIQUE (id)

**Indexes:** `CREATE UNIQUE INDEX privacy_requests_pkey ON app.privacy_requests USING btree (id)`; `CREATE UNIQUE INDEX privacy_requests_tenant_id_id_key ON app.privacy_requests USING btree (id)`


## app.product_categories

_(see spec section 5)_

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| parent_id | uuid | yes |  | → product_categories |  |
| name | app.short_text | NO |  |  |  |
| status | app.record_status | NO | 'active'::app.record_status |  |  |
| created_at | timestamp with time zone | NO | now() |  |  |
| created_by | uuid | yes | app.current_membership_id() | → memberships |  |
| updated_at | timestamp with time zone | NO | now() |  |  |
| updated_by | uuid | yes |  | → memberships |  |
| row_version | integer | NO | 1 |  |  |

**Constraints:** `product_categories_check` CHECK ((parent_id IS DISTINCT FROM id)); `product_categories_tenant_id_id_key` UNIQUE (id); `product_categories_tenant_id_parent_id_name_key` UNIQUE NULLS NOT DISTINCT (parent_id, name)

**Indexes:** `CREATE UNIQUE INDEX product_categories_pkey ON app.product_categories USING btree (id)`; `CREATE UNIQUE INDEX product_categories_tenant_id_id_key ON app.product_categories USING btree (id)`; `CREATE UNIQUE INDEX product_categories_tenant_id_parent_id_name_key ON app.product_categories USING btree (parent_id, name) NULLS NOT DISTINCT`


## app.product_price_changes

_(see spec section 5)_

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| product_id | uuid | NO |  | → products |  |
| old_selling_price | app.money_nn | NO |  |  |  |
| new_selling_price | app.money_nn | NO |  |  |  |
| old_buying_price | app.money_nn | NO |  |  |  |
| new_buying_price | app.money_nn | NO |  |  |  |
| reason | text | NO |  |  |  |
| effective_at | timestamp with time zone | NO | now() |  |  |
| created_at | timestamp with time zone | NO | now() |  |  |
| created_by | uuid | yes | app.current_membership_id() | → memberships |  |
| approval_request_id | uuid | yes |  | → approval_requests |  |

**Constraints:** `product_price_changes_reason_check` CHECK ((length(btrim(reason)) >= 3)); `product_price_changes_tenant_id_id_key` UNIQUE (id)

**Indexes:** `CREATE UNIQUE INDEX product_price_changes_pkey ON app.product_price_changes USING btree (id)`; `CREATE INDEX product_price_changes_product_idx ON app.product_price_changes USING btree (product_id, effective_at DESC)`; `CREATE UNIQUE INDEX product_price_changes_tenant_id_id_key ON app.product_price_changes USING btree (id)`


## app.product_units

_(see spec section 5)_

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| product_id | uuid | NO |  | → products |  |
| location_id | uuid | NO |  | → locations |  |
| serial_no | text | NO |  |  |  |
| imei | text | yes |  |  |  |
| status | app.unit_status | NO | 'in_stock'::app.unit_status |  |  |
| sold_at | timestamp with time zone | yes |  |  |  |
| warranty_expires_on | date | yes |  |  |  |
| created_at | timestamp with time zone | NO | now() |  |  |
| created_by | uuid | yes | app.current_membership_id() | → memberships |  |
| updated_at | timestamp with time zone | NO | now() |  |  |
| updated_by | uuid | yes |  | → memberships |  |
| row_version | integer | NO | 1 |  |  |

**Constraints:** `product_units_check` CHECK (((status <> 'sold'::app.unit_status) OR (sold_at IS NOT NULL))); `product_units_imei_check` CHECK ((imei ~ '^[0-9]{15}$'::text)); `product_units_serial_no_check` CHECK ((serial_no ~ '^[A-Za-z0-9-]{3,64}$'::text)); `product_units_tenant_id_id_key` UNIQUE (id); `product_units_tenant_id_product_id_serial_no_key` UNIQUE (product_id, serial_no)

**Indexes:** `CREATE UNIQUE INDEX product_units_pkey ON app.product_units USING btree (id)`; `CREATE INDEX product_units_available_idx ON app.product_units USING btree (product_id, location_id) WHERE (status = 'in_stock'::app.unit_status)`; `CREATE UNIQUE INDEX product_units_imei_uq ON app.product_units USING btree (imei) WHERE (imei IS NOT NULL)`; `CREATE UNIQUE INDEX product_units_tenant_id_id_key ON app.product_units USING btree (id)`; `CREATE UNIQUE INDEX product_units_tenant_id_product_id_serial_no_key ON app.product_units USING btree (product_id, serial_no)`


## app.products

_(see spec section 5)_

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| business_unit_id | uuid | NO |  | → business_units |  |
| category_id | uuid | yes |  | → product_categories |  |
| sku | text | yes |  |  |  |
| barcode | text | yes |  |  |  |
| name | app.short_text | NO |  |  |  |
| description | text | yes |  |  |  |
| unit_of_measure | text | NO | 'pcs'::text |  |  |
| selling_price | app.money_nn | NO |  |  |  |
| buying_price | app.money_nn | NO | 0 |  | CONFIDENTIAL: hidden from Staff (spec §2A). Staff read app.v_staff_catalog, never this table directly. |
| average_cost | numeric(18,4) | NO | 0 |  |  |
| tax_rate_id | uuid | yes |  | → tax_rates |  |
| min_stock_level | numeric(14,3) | yes |  |  |  |
| warranty_months | smallint | NO | 0 |  |  |
| tracks_serial | boolean | NO | false |  |  |
| allow_negative_stock | boolean | yes |  |  |  |
| photo_file_id | uuid | yes |  | → files |  |
| status | app.record_status | NO | 'active'::app.record_status |  |  |
| created_at | timestamp with time zone | NO | now() |  |  |
| created_by | uuid | yes | app.current_membership_id() | → memberships |  |
| updated_at | timestamp with time zone | NO | now() |  |  |
| updated_by | uuid | yes |  | → memberships |  |
| row_version | integer | NO | 1 |  |  |

**Constraints:** `products_average_cost_check` CHECK ((average_cost >= (0)::numeric)); `products_barcode_check` CHECK ((barcode ~ '^[0-9A-Za-z-]{4,48}$'::text)); `products_check` CHECK (((NOT tracks_serial) OR (unit_of_measure = 'pcs'::text))); `products_description_check` CHECK ((length(description) <= 2000)); `products_min_stock_level_check` CHECK ((min_stock_level >= (0)::numeric)); `products_sku_check` CHECK ((sku ~ '^[A-Za-z0-9._-]{1,40}$'::text)); `products_tenant_id_id_key` UNIQUE (id); `products_unit_of_measure_check` CHECK ((unit_of_measure ~ '^[a-z]{1,10}$'::text)); `products_warranty_months_check` CHECK (((warranty_months >= 0) AND (warranty_months <= 120)))

**Indexes:** `CREATE UNIQUE INDEX products_pkey ON app.products USING btree (id)`; `CREATE INDEX products_barcode_trgm ON app.products USING gin (barcode public.gin_trgm_ops)`; `CREATE UNIQUE INDEX products_barcode_uq ON app.products USING btree (barcode) WHERE (barcode IS NOT NULL)`; `CREATE INDEX products_category_idx ON app.products USING btree (category_id) WHERE (status = 'active'::app.record_status)`; `CREATE INDEX products_name_trgm ON app.products USING gin (name public.gin_trgm_ops)`; `CREATE INDEX products_sku_trgm ON app.products USING gin (sku public.gin_trgm_ops)`; `CREATE UNIQUE INDEX products_sku_uq ON app.products USING btree (sku) WHERE (sku IS NOT NULL)`; `CREATE UNIQUE INDEX products_tenant_id_id_key ON app.products USING btree (id)`


## app.project_stage_checklist

_(see spec section 5)_

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| project_stage_id | uuid | NO |  | → project_stages |  |
| seq | smallint | NO |  |  |  |
| label | app.short_text | NO |  |  |  |
| is_done | boolean | NO | false |  |  |
| done_by | uuid | yes |  | → memberships |  |
| done_at | timestamp with time zone | yes |  |  |  |

**Constraints:** `project_stage_checklist_check` CHECK (((NOT is_done) OR ((done_by IS NOT NULL) AND (done_at IS NOT NULL)))); `project_stage_checklist_seq_check` CHECK ((seq > 0)); `project_stage_checklist_tenant_id_id_key` UNIQUE (id); `project_stage_checklist_tenant_id_project_stage_id_seq_key` UNIQUE (project_stage_id, seq)

**Indexes:** `CREATE UNIQUE INDEX project_stage_checklist_pkey ON app.project_stage_checklist USING btree (id)`; `CREATE UNIQUE INDEX project_stage_checklist_tenant_id_id_key ON app.project_stage_checklist USING btree (id)`; `CREATE UNIQUE INDEX project_stage_checklist_tenant_id_project_stage_id_seq_key ON app.project_stage_checklist USING btree (project_stage_id, seq)`


## app.project_stage_history

Append-only, written by trigger on project_stages.status (spec §7: optional note, who and when).

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| project_stage_id | uuid | NO |  | → project_stages |  |
| from_status | app.stage_status | NO |  |  |  |
| to_status | app.stage_status | NO |  |  |  |
| note | text | yes |  |  |  |
| created_at | timestamp with time zone | NO | now() |  |  |
| created_by | uuid | yes | app.current_membership_id() | → memberships |  |

**Constraints:** `project_stage_history_note_check` CHECK ((length(note) <= 2000)); `project_stage_history_tenant_id_id_key` UNIQUE (id)

**Indexes:** `CREATE UNIQUE INDEX project_stage_history_pkey ON app.project_stage_history USING btree (id)`; `CREATE INDEX project_stage_history_stage_idx ON app.project_stage_history USING btree (project_stage_id, created_at)`; `CREATE UNIQUE INDEX project_stage_history_tenant_id_id_key ON app.project_stage_history USING btree (id)`


## app.project_stages

_(see spec section 5)_

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| project_id | uuid | NO |  | → projects |  |
| service_stage_id | uuid | yes |  | → service_stages |  |
| seq | smallint | NO |  |  |  |
| name | app.short_text | NO |  |  |  |
| status | app.stage_status | NO | 'not_started'::app.stage_status |  |  |
| owner_id | uuid | yes |  | → memberships |  |
| deadline | date | yes |  |  |  |
| started_at | timestamp with time zone | yes |  |  |  |
| completed_at | timestamp with time zone | yes |  |  |  |
| is_branding_stage | boolean | NO | false |  |  |
| created_at | timestamp with time zone | NO | now() |  |  |
| created_by | uuid | yes | app.current_membership_id() | → memberships |  |
| updated_at | timestamp with time zone | NO | now() |  |  |
| updated_by | uuid | yes |  | → memberships |  |
| row_version | integer | NO | 1 |  |  |

**Constraints:** `project_stages_check` CHECK (((status <> 'done'::app.stage_status) OR (completed_at IS NOT NULL))); `project_stages_seq_check` CHECK ((seq > 0)); `project_stages_tenant_id_id_key` UNIQUE (id); `project_stages_tenant_id_project_id_seq_key` UNIQUE (project_id, seq)

**Indexes:** `CREATE UNIQUE INDEX project_stages_pkey ON app.project_stages USING btree (id)`; `CREATE INDEX project_stages_deadline_idx ON app.project_stages USING btree (deadline) WHERE (status <> 'done'::app.stage_status)`; `CREATE INDEX project_stages_owner_idx ON app.project_stages USING btree (owner_id) WHERE (status <> 'done'::app.stage_status)`; `CREATE UNIQUE INDEX project_stages_tenant_id_id_key ON app.project_stages USING btree (id)`; `CREATE UNIQUE INDEX project_stages_tenant_id_project_id_seq_key ON app.project_stages USING btree (project_id, seq)`


## app.projects

_(see spec section 5)_

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| project_no | text | NO |  |  |  |
| customer_id | uuid | NO |  | → customers |  |
| service_id | uuid | NO |  | → services |  |
| quotation_id | uuid | yes |  | → quotations |  |
| business_unit_id | uuid | NO |  | → business_units |  |
| title | app.short_text | NO |  |  |  |
| status | app.project_status | NO | 'not_started'::app.project_status |  |  |
| owner_id | uuid | NO |  | → memberships |  |
| start_date | date | NO |  |  |  |
| target_end_date | date | yes |  |  |  |
| total_price | app.money_nn | NO |  |  |  |
| completed_at | timestamp with time zone | yes |  |  |  |
| cancelled_at | timestamp with time zone | yes |  |  |  |
| cancel_reason | text | yes |  |  |  |
| created_at | timestamp with time zone | NO | now() |  |  |
| created_by | uuid | yes | app.current_membership_id() | → memberships |  |
| updated_at | timestamp with time zone | NO | now() |  |  |
| updated_by | uuid | yes |  | → memberships |  |
| row_version | integer | NO | 1 |  |  |

**Constraints:** `projects_cancel_reason_check` CHECK ((length(cancel_reason) <= 1000)); `projects_check` CHECK (((target_end_date IS NULL) OR (target_end_date >= start_date))); `projects_check1` CHECK (((status <> 'completed'::app.project_status) OR (completed_at IS NOT NULL))); `projects_check2` CHECK (((status <> 'cancelled'::app.project_status) OR ((cancelled_at IS NOT NULL) AND (cancel_reason IS NOT NULL)))); `projects_tenant_id_id_key` UNIQUE (id); `projects_tenant_id_project_no_key` UNIQUE (project_no); `projects_tenant_id_quotation_id_key` UNIQUE (quotation_id)

**Indexes:** `CREATE UNIQUE INDEX projects_pkey ON app.projects USING btree (id)`; `CREATE INDEX projects_customer_idx ON app.projects USING btree (customer_id)`; `CREATE INDEX projects_owner_idx ON app.projects USING btree (owner_id) WHERE (status = ANY (ARRAY['not_started'::app.project_status, 'in_progress'::app.project_status, 'on_hold'::app.project_status]))`; `CREATE INDEX projects_project_no_trgm ON app.projects USING gin (project_no public.gin_trgm_ops)`; `CREATE INDEX projects_status_idx ON app.projects USING btree (status)`; `CREATE UNIQUE INDEX projects_tenant_id_id_key ON app.projects USING btree (id)`; `CREATE UNIQUE INDEX projects_tenant_id_project_no_key ON app.projects USING btree (project_no)`; `CREATE UNIQUE INDEX projects_tenant_id_quotation_id_key ON app.projects USING btree (quotation_id)`


## app.purchase_lines

_(see spec section 5)_

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| purchase_id | uuid | NO |  | → purchases |  |
| line_no | smallint | NO |  |  |  |
| product_id | uuid | NO |  | → products |  |
| quantity_ordered | app.qty_pos | NO |  |  |  |
| quantity_received | app.qty | NO | 0 |  |  |
| unit_cost | app.money_nn | NO |  |  |  |
| line_total | app.money_nn | NO |  |  |  |

**Constraints:** `purchase_lines_check` CHECK ((((quantity_received)::numeric >= (0)::numeric) AND ((quantity_received)::numeric <= (quantity_ordered)::numeric))); `purchase_lines_check1` CHECK (((line_total)::numeric = round(((quantity_ordered)::numeric * (unit_cost)::numeric), 2))); `purchase_lines_line_no_check` CHECK ((line_no > 0)); `purchase_lines_tenant_id_id_key` UNIQUE (id); `purchase_lines_tenant_id_purchase_id_line_no_key` UNIQUE (purchase_id, line_no)

**Indexes:** `CREATE UNIQUE INDEX purchase_lines_pkey ON app.purchase_lines USING btree (id)`; `CREATE UNIQUE INDEX purchase_lines_tenant_id_id_key ON app.purchase_lines USING btree (id)`; `CREATE UNIQUE INDEX purchase_lines_tenant_id_purchase_id_line_no_key ON app.purchase_lines USING btree (purchase_id, line_no)`


## app.purchases

_(see spec section 5)_

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| purchase_no | text | NO |  |  |  |
| supplier_id | uuid | NO |  | → suppliers |  |
| business_unit_id | uuid | NO |  | → business_units |  |
| location_id | uuid | NO |  | → locations |  |
| status | app.purchase_status | NO | 'draft'::app.purchase_status |  |  |
| order_date | date | NO |  |  |  |
| expected_date | date | yes |  |  |  |
| due_date | date | yes |  |  |  |
| subtotal | app.money_nn | NO | 0 |  |  |
| discount_amount | app.money_nn | NO | 0 |  |  |
| tax_amount | app.money_nn | NO | 0 |  |  |
| total | app.money_nn | NO | 0 |  |  |
| amount_paid | app.money_nn | NO | 0 |  |  |
| balance_due | numeric(18,2) | yes | ((total)::numeric - (amount_paid)::numeric) |  |  |
| supplier_invoice_ref | text | yes |  |  |  |
| notes | text | yes |  |  |  |
| created_at | timestamp with time zone | NO | now() |  |  |
| created_by | uuid | yes | app.current_membership_id() | → memberships |  |
| updated_at | timestamp with time zone | NO | now() |  |  |
| updated_by | uuid | yes |  | → memberships |  |
| row_version | integer | NO | 1 |  |  |

**Constraints:** `purchases_check` CHECK (((total)::numeric = (((subtotal)::numeric - (discount_amount)::numeric) + (tax_amount)::numeric))); `purchases_check1` CHECK (((amount_paid)::numeric <= (total)::numeric)); `purchases_notes_check` CHECK ((length(notes) <= 2000)); `purchases_supplier_invoice_ref_check` CHECK ((length(supplier_invoice_ref) <= 60)); `purchases_tenant_id_id_key` UNIQUE (id); `purchases_tenant_id_purchase_no_key` UNIQUE (purchase_no)

**Indexes:** `CREATE UNIQUE INDEX purchases_pkey ON app.purchases USING btree (id)`; `CREATE INDEX purchases_open_idx ON app.purchases USING btree (due_date) WHERE ((status <> 'cancelled'::app.purchase_status) AND (balance_due > (0)::numeric))`; `CREATE INDEX purchases_supplier_idx ON app.purchases USING btree (supplier_id, order_date DESC)`; `CREATE UNIQUE INDEX purchases_tenant_id_id_key ON app.purchases USING btree (id)`; `CREATE UNIQUE INDEX purchases_tenant_id_purchase_no_key ON app.purchases USING btree (purchase_no)`


## app.quotation_lines

_(see spec section 5)_

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| quotation_id | uuid | NO |  | → quotations |  |
| line_no | smallint | NO |  |  |  |
| service_id | uuid | NO |  | → services |  |
| description | app.short_text | NO |  |  |  |
| quantity | app.qty_pos | NO | 1 |  |  |
| unit_price | app.money_nn | NO |  |  |  |
| line_discount | app.money_nn | NO | 0 |  |  |
| line_total | app.money_nn | NO |  |  |  |

**Constraints:** `quotation_lines_check` CHECK (((line_total)::numeric = (round(((quantity)::numeric * (unit_price)::numeric), 2) - (line_discount)::numeric))); `quotation_lines_line_no_check` CHECK ((line_no > 0)); `quotation_lines_tenant_id_id_key` UNIQUE (id); `quotation_lines_tenant_id_quotation_id_line_no_key` UNIQUE (quotation_id, line_no)

**Indexes:** `CREATE UNIQUE INDEX quotation_lines_pkey ON app.quotation_lines USING btree (id)`; `CREATE UNIQUE INDEX quotation_lines_tenant_id_id_key ON app.quotation_lines USING btree (id)`; `CREATE UNIQUE INDEX quotation_lines_tenant_id_quotation_id_line_no_key ON app.quotation_lines USING btree (quotation_id, line_no)`


## app.quotations

_(see spec section 5)_

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| quotation_no | text | NO |  |  |  |
| customer_id | uuid | NO |  | → customers |  |
| business_unit_id | uuid | NO |  | → business_units |  |
| status | app.quotation_status | NO | 'draft'::app.quotation_status |  |  |
| issue_date | date | NO |  |  |  |
| valid_until | date | NO |  |  |  |
| subtotal | app.money_nn | NO | 0 |  |  |
| discount_amount | app.money_nn | NO | 0 |  |  |
| tax_amount | app.money_nn | NO | 0 |  |  |
| total | app.money_nn | NO | 0 |  |  |
| terms | text | yes |  |  |  |
| notes | text | yes |  |  |  |
| sent_at | timestamp with time zone | yes |  |  |  |
| decided_at | timestamp with time zone | yes |  |  |  |
| rejection_reason | text | yes |  |  |  |
| prepared_by | uuid | NO | app.current_membership_id() | → memberships |  |
| created_at | timestamp with time zone | NO | now() |  |  |
| created_by | uuid | yes | app.current_membership_id() | → memberships |  |
| updated_at | timestamp with time zone | NO | now() |  |  |
| updated_by | uuid | yes |  | → memberships |  |
| row_version | integer | NO | 1 |  |  |

**Constraints:** `quotations_check` CHECK ((valid_until >= issue_date)); `quotations_check1` CHECK (((total)::numeric = (((subtotal)::numeric - (discount_amount)::numeric) + (tax_amount)::numeric))); `quotations_check2` CHECK (((discount_amount)::numeric <= (subtotal)::numeric)); `quotations_check3` CHECK (((status <> ALL (ARRAY['sent'::app.quotation_status, 'accepted'::app.quotation_status, 'rejected'::app.quotation_status, 'converted'::app.quotation_status])) OR (sent_at IS NOT NULL))); `quotations_notes_check` CHECK ((length(notes) <= 2000)); `quotations_rejection_reason_check` CHECK ((length(rejection_reason) <= 1000)); `quotations_tenant_id_id_key` UNIQUE (id); `quotations_tenant_id_quotation_no_key` UNIQUE (quotation_no); `quotations_terms_check` CHECK ((length(terms) <= 4000))

**Indexes:** `CREATE UNIQUE INDEX quotations_pkey ON app.quotations USING btree (id)`; `CREATE INDEX quotations_customer_idx ON app.quotations USING btree (customer_id, issue_date DESC)`; `CREATE INDEX quotations_no_trgm ON app.quotations USING gin (quotation_no public.gin_trgm_ops)`; `CREATE INDEX quotations_open_idx ON app.quotations USING btree (valid_until) WHERE (status = ANY (ARRAY['draft'::app.quotation_status, 'sent'::app.quotation_status]))`; `CREATE UNIQUE INDEX quotations_tenant_id_id_key ON app.quotations USING btree (id)`; `CREATE UNIQUE INDEX quotations_tenant_id_quotation_no_key ON app.quotations USING btree (quotation_no)`


## app.recurring_expenses

_(see spec section 5)_

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| category_id | uuid | NO |  | → expense_categories |  |
| description | app.short_text | NO |  |  |  |
| amount | app.money_pos | NO |  |  |  |
| business_unit_id | uuid | NO |  | → business_units |  |
| location_id | uuid | NO |  | → locations |  |
| account_id | uuid | yes |  | → financial_accounts |  |
| day_of_month | smallint | NO |  |  |  |
| next_run_on | date | NO |  |  |  |
| active | boolean | NO | true |  |  |
| created_at | timestamp with time zone | NO | now() |  |  |
| created_by | uuid | yes | app.current_membership_id() | → memberships |  |
| updated_at | timestamp with time zone | NO | now() |  |  |
| updated_by | uuid | yes |  | → memberships |  |
| row_version | integer | NO | 1 |  |  |

**Constraints:** `recurring_expenses_day_of_month_check` CHECK (((day_of_month >= 1) AND (day_of_month <= 28))); `recurring_expenses_tenant_id_id_key` UNIQUE (id)

**Indexes:** `CREATE UNIQUE INDEX recurring_expenses_pkey ON app.recurring_expenses USING btree (id)`; `CREATE INDEX recurring_expenses_due_idx ON app.recurring_expenses USING btree (next_run_on) WHERE active`; `CREATE UNIQUE INDEX recurring_expenses_tenant_id_id_key ON app.recurring_expenses USING btree (id)`


## app.renewal_reminders

_(see spec section 5)_

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| renewal_id | uuid | NO |  | → renewals |  |
| days_before | smallint | NO |  |  |  |
| outbound_message_id | uuid | yes |  | → outbound_messages |  |
| created_at | timestamp with time zone | NO | now() |  |  |

**Constraints:** `renewal_reminders_days_before_check` CHECK (((days_before >= 0) AND (days_before <= 365))); `renewal_reminders_tenant_id_id_key` UNIQUE (id); `renewal_reminders_tenant_id_renewal_id_days_before_key` UNIQUE (renewal_id, days_before)

**Indexes:** `CREATE UNIQUE INDEX renewal_reminders_pkey ON app.renewal_reminders USING btree (id)`; `CREATE UNIQUE INDEX renewal_reminders_tenant_id_id_key ON app.renewal_reminders USING btree (id)`; `CREATE UNIQUE INDEX renewal_reminders_tenant_id_renewal_id_days_before_key ON app.renewal_reminders USING btree (renewal_id, days_before)`


## app.renewals

_(see spec section 5)_

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| customer_id | uuid | NO |  | → customers |  |
| project_id | uuid | yes |  | → projects |  |
| item_type | app.renewal_item | NO |  |  |  |
| description | app.short_text | NO |  |  |  |
| due_date | date | NO |  |  |  |
| status | app.renewal_status | NO | 'upcoming'::app.renewal_status |  |  |
| renewed_at | timestamp with time zone | yes |  |  |  |
| next_renewal_id | uuid | yes |  | → renewals |  |
| notes | text | yes |  |  |  |
| created_at | timestamp with time zone | NO | now() |  |  |
| created_by | uuid | yes | app.current_membership_id() | → memberships |  |
| updated_at | timestamp with time zone | NO | now() |  |  |
| updated_by | uuid | yes |  | → memberships |  |
| row_version | integer | NO | 1 |  |  |

**Constraints:** `renewals_check` CHECK (((status <> 'renewed'::app.renewal_status) OR (renewed_at IS NOT NULL))); `renewals_check1` CHECK ((next_renewal_id IS DISTINCT FROM id)); `renewals_notes_check` CHECK ((length(notes) <= 1000)); `renewals_tenant_id_id_key` UNIQUE (id)

**Indexes:** `CREATE UNIQUE INDEX renewals_pkey ON app.renewals USING btree (id)`; `CREATE INDEX renewals_due_idx ON app.renewals USING btree (due_date) WHERE (status = 'upcoming'::app.renewal_status)`; `CREATE UNIQUE INDEX renewals_project_item_uq ON app.renewals USING btree (project_id, item_type, due_date) WHERE (project_id IS NOT NULL)`; `CREATE UNIQUE INDEX renewals_tenant_id_id_key ON app.renewals USING btree (id)`


## app.retention_policies

_(see spec section 5)_

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| data_category | text | NO |  | PK |  |
| description | text | NO |  |  |  |
| retention_period | interval | yes |  |  |  |
| legal_basis | text | yes |  |  |  |
| disposal_method | text | NO |  |  |  |
| approved_by | text | yes |  |  |  |
| approved_at | timestamp with time zone | yes |  |  |  |

**Constraints:** `retention_policies_check` CHECK (((retention_period IS NULL) OR (approved_by IS NOT NULL))); `retention_policies_data_category_check` CHECK ((data_category ~ '^[a-z_]{3,60}$'::text)); `retention_policies_disposal_method_check` CHECK ((disposal_method = ANY (ARRAY['delete'::text, 'anonymise'::text, 'archive_then_delete'::text, 'none'::text])))

**Indexes:** `CREATE UNIQUE INDEX retention_policies_pkey ON app.retention_policies USING btree (data_category)`


## app.role_permissions

_(see spec section 5)_

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| role_id | uuid | NO |  | PK → roles |  |
| permission_code | text | NO |  | PK → permissions |  |
| created_at | timestamp with time zone | NO | now() |  |  |
| created_by | uuid | yes | app.current_membership_id() | → memberships |  |

**Constraints:**

**Indexes:** `CREATE UNIQUE INDEX role_permissions_pkey ON app.role_permissions USING btree (role_id, permission_code)`


## app.roles

_(see spec section 5)_

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| code | public.citext | NO |  |  |  |
| name | app.short_text | NO |  |  |  |
| description | text | yes |  |  |  |
| is_system | boolean | NO | false |  |  |
| rank | smallint | NO |  |  |  |
| status | app.record_status | NO | 'active'::app.record_status |  |  |
| created_at | timestamp with time zone | NO | now() |  |  |
| created_by | uuid | yes | app.current_membership_id() | → memberships |  |
| updated_at | timestamp with time zone | NO | now() |  |  |
| updated_by | uuid | yes |  | → memberships |  |
| row_version | integer | NO | 1 |  |  |

**Constraints:** `roles_code_check` CHECK ((code OPERATOR(public.~) '^[a-z][a-z0-9_]{1,40}$'::public.citext)); `roles_rank_check` CHECK (((rank >= 1) AND (rank <= 100))); `roles_tenant_id_code_key` UNIQUE (code); `roles_tenant_id_id_key` UNIQUE (id)

**Indexes:** `CREATE UNIQUE INDEX roles_pkey ON app.roles USING btree (id)`; `CREATE UNIQUE INDEX roles_tenant_id_code_key ON app.roles USING btree (code)`; `CREATE UNIQUE INDEX roles_tenant_id_id_key ON app.roles USING btree (id)`


## app.sale_line_units

_(see spec section 5)_

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| sale_line_id | uuid | NO |  | PK → sale_lines |  |
| product_unit_id | uuid | NO |  | PK → product_units |  |

**Constraints:**

**Indexes:** `CREATE UNIQUE INDEX sale_line_units_pkey ON app.sale_line_units USING btree (sale_line_id, product_unit_id)`; `CREATE INDEX sale_line_units_unit_idx ON app.sale_line_units USING btree (product_unit_id)`


## app.sale_lines

_(see spec section 5)_

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| sale_id | uuid | NO |  | → sales |  |
| line_no | smallint | NO |  |  |  |
| product_id | uuid | NO |  | → products |  |
| product_name | app.short_text | NO |  |  |  |
| quantity | app.qty_pos | NO |  |  |  |
| unit_price | app.money_nn | NO |  |  |  |
| line_discount | app.money_nn | NO | 0 |  |  |
| tax_rate_pct | app.pct | NO | 0 |  |  |
| tax_amount | app.money_nn | NO | 0 |  |  |
| line_total | app.money_nn | NO |  |  |  |
| unit_cost | numeric(18,4) | yes |  |  |  |
| returned_quantity | app.qty | NO | 0 |  |  |
| warranty_months | smallint | NO | 0 |  |  |

**Constraints:** `sale_lines_check` CHECK (((line_total)::numeric = (round(((quantity)::numeric * (unit_price)::numeric), 2) - (line_discount)::numeric))); `sale_lines_check1` CHECK (((line_discount)::numeric <= round(((quantity)::numeric * (unit_price)::numeric), 2))); `sale_lines_check2` CHECK ((((returned_quantity)::numeric >= (0)::numeric) AND ((returned_quantity)::numeric <= (quantity)::numeric))); `sale_lines_line_no_check` CHECK ((line_no > 0)); `sale_lines_tenant_id_id_key` UNIQUE (id); `sale_lines_tenant_id_sale_id_line_no_key` UNIQUE (sale_id, line_no); `sale_lines_unit_cost_check` CHECK ((unit_cost >= (0)::numeric))

**Indexes:** `CREATE UNIQUE INDEX sale_lines_pkey ON app.sale_lines USING btree (id)`; `CREATE INDEX sale_lines_product_idx ON app.sale_lines USING btree (product_id)`; `CREATE UNIQUE INDEX sale_lines_tenant_id_id_key ON app.sale_lines USING btree (id)`; `CREATE UNIQUE INDEX sale_lines_tenant_id_sale_id_line_no_key ON app.sale_lines USING btree (sale_id, line_no)`


## app.sale_return_lines

_(see spec section 5)_

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| sale_return_id | uuid | NO |  | → sale_returns |  |
| sale_line_id | uuid | NO |  | → sale_lines |  |
| quantity | app.qty_pos | NO |  |  |  |
| condition | app.item_condition | NO |  |  |  |
| restock | boolean | NO |  |  |  |
| refund_amount | app.money_nn | NO | 0 |  |  |
| product_unit_id | uuid | yes |  | → product_units |  |

**Constraints:** `sale_return_lines_check` CHECK (((NOT restock) OR (condition = 'resellable'::app.item_condition))); `sale_return_lines_tenant_id_id_key` UNIQUE (id); `sale_return_lines_tenant_id_sale_return_id_sale_line_id_key` UNIQUE (sale_return_id, sale_line_id)

**Indexes:** `CREATE UNIQUE INDEX sale_return_lines_pkey ON app.sale_return_lines USING btree (id)`; `CREATE UNIQUE INDEX sale_return_lines_tenant_id_id_key ON app.sale_return_lines USING btree (id)`; `CREATE UNIQUE INDEX sale_return_lines_tenant_id_sale_return_id_sale_line_id_key ON app.sale_return_lines USING btree (sale_return_id, sale_line_id)`


## app.sale_returns

_(see spec section 5)_

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| return_no | text | NO |  |  |  |
| sale_id | uuid | NO |  | → sales |  |
| location_id | uuid | NO |  | → locations |  |
| status | app.return_status | NO | 'requested'::app.return_status |  |  |
| resolution | app.return_resolution | NO |  |  |  |
| reason | text | NO |  |  |  |
| refund_amount | app.money_nn | NO | 0 |  |  |
| exchange_sale_id | uuid | yes |  | → sales |  |
| requested_by | uuid | NO | app.current_membership_id() | → memberships |  |
| decided_by | uuid | yes |  | → memberships |  |
| decided_at | timestamp with time zone | yes |  |  |  |
| completed_at | timestamp with time zone | yes |  |  |  |
| business_date | date | NO |  |  |  |
| created_at | timestamp with time zone | NO | now() |  |  |
| created_by | uuid | yes | app.current_membership_id() | → memberships |  |
| updated_at | timestamp with time zone | NO | now() |  |  |
| updated_by | uuid | yes |  | → memberships |  |
| row_version | integer | NO | 1 |  |  |

**Constraints:** `sale_returns_check` CHECK (((status <> ALL (ARRAY['approved'::app.return_status, 'rejected'::app.return_status, 'completed'::app.return_status])) OR ((decided_by IS NOT NULL) AND (decided_at IS NOT NULL)))); `sale_returns_check1` CHECK (((decided_by IS NULL) OR (decided_by <> requested_by))); `sale_returns_check2` CHECK (((status <> 'completed'::app.return_status) OR (completed_at IS NOT NULL))); `sale_returns_check3` CHECK (((resolution <> 'refund'::app.return_resolution) OR ((refund_amount)::numeric > (0)::numeric) OR (status = ANY (ARRAY['requested'::app.return_status, 'rejected'::app.return_status, 'cancelled'::app.return_status])))); `sale_returns_reason_check` CHECK ((length(btrim(reason)) >= 3)); `sale_returns_tenant_id_id_key` UNIQUE (id); `sale_returns_tenant_id_return_no_key` UNIQUE (return_no)

**Indexes:** `CREATE UNIQUE INDEX sale_returns_pkey ON app.sale_returns USING btree (id)`; `CREATE INDEX sale_returns_sale_idx ON app.sale_returns USING btree (sale_id)`; `CREATE UNIQUE INDEX sale_returns_tenant_id_id_key ON app.sale_returns USING btree (id)`; `CREATE UNIQUE INDEX sale_returns_tenant_id_return_no_key ON app.sale_returns USING btree (return_no)`


## app.sales

_(see spec section 5)_

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| business_unit_id | uuid | NO |  | → business_units |  |
| location_id | uuid | NO |  | → locations |  |
| receipt_no | text | yes |  |  |  |
| status | app.sale_status | NO | 'draft'::app.sale_status |  |  |
| customer_id | uuid | yes |  | → customers |  |
| sold_by | uuid | NO | app.current_membership_id() | → memberships |  |
| device_id | uuid | yes |  | → devices |  |
| client_ref | uuid | yes |  |  |  |
| business_date | date | NO |  |  | Trading day in the tenant timezone; drives end-of-day lock (app.day_closings). |
| subtotal | app.money_nn | NO | 0 |  |  |
| discount_type | app.discount_type | yes |  |  |  |
| discount_value | numeric(18,2) | NO | 0 |  |  |
| discount_amount | app.money_nn | NO | 0 |  |  |
| tax_amount | app.money_nn | NO | 0 |  |  |
| total | app.money_nn | NO | 0 |  |  |
| amount_paid | app.money_nn | NO | 0 |  |  |
| balance_due | numeric(18,2) | yes | ((total)::numeric - (amount_paid)::numeric) |  |  |
| due_date | date | yes |  |  |  |
| held_label | text | yes |  |  |  |
| held_at | timestamp with time zone | yes |  |  |  |
| completed_at | timestamp with time zone | yes |  |  |  |
| voided_at | timestamp with time zone | yes |  |  |  |
| voided_by | uuid | yes |  | → memberships |  |
| void_reason | text | yes |  |  |  |
| offline_created_at | timestamp with time zone | yes |  |  |  |
| synced_at | timestamp with time zone | yes |  |  |  |
| notes | text | yes |  |  |  |
| created_at | timestamp with time zone | NO | now() |  |  |
| created_by | uuid | yes | app.current_membership_id() | → memberships |  |
| updated_at | timestamp with time zone | NO | now() |  |  |
| updated_by | uuid | yes |  | → memberships |  |
| row_version | integer | NO | 1 |  |  |

**Constraints:** `sales_check` CHECK (((discount_type IS NOT NULL) OR (discount_value = (0)::numeric))); `sales_check1` CHECK (((discount_type <> 'percent'::app.discount_type) OR (discount_value <= (100)::numeric))); `sales_check2` CHECK (((discount_amount)::numeric <= (subtotal)::numeric)); `sales_check3` CHECK (((total)::numeric = ((subtotal)::numeric - (discount_amount)::numeric))); `sales_check4` CHECK (((amount_paid)::numeric <= (total)::numeric)); `sales_check5` CHECK (((status <> ALL (ARRAY['completed'::app.sale_status, 'voided'::app.sale_status])) OR ((receipt_no IS NOT NULL) AND (completed_at IS NOT NULL)))); `sales_check6` CHECK (((status <> 'voided'::app.sale_status) OR ((voided_at IS NOT NULL) AND (voided_by IS NOT NULL) AND (void_reason IS NOT NULL)))); `sales_check7` CHECK (((status <> 'held'::app.sale_status) OR (held_at IS NOT NULL))); `sales_discount_value_check` CHECK ((discount_value >= (0)::numeric)); `sales_held_label_check` CHECK ((length(held_label) <= 60)); `sales_notes_check` CHECK ((length(notes) <= 1000)); `sales_tenant_id_client_ref_key` UNIQUE (client_ref); `sales_tenant_id_id_key` UNIQUE (id); `sales_tenant_id_receipt_no_key` UNIQUE (receipt_no); `sales_void_reason_check` CHECK ((length(void_reason) <= 1000))

**Indexes:** `CREATE UNIQUE INDEX sales_pkey ON app.sales USING btree (id)`; `CREATE INDEX sales_customer_idx ON app.sales USING btree (customer_id) WHERE (customer_id IS NOT NULL)`; `CREATE INDEX sales_held_idx ON app.sales USING btree (location_id) WHERE (status = ANY (ARRAY['held'::app.sale_status, 'draft'::app.sale_status, 'pending_approval'::app.sale_status]))`; `CREATE INDEX sales_history_idx ON app.sales USING btree (business_date DESC, location_id) WHERE (status = ANY (ARRAY['completed'::app.sale_status, 'voided'::app.sale_status]))`; `CREATE INDEX sales_open_credit_idx ON app.sales USING btree (due_date) WHERE ((status = 'completed'::app.sale_status) AND (balance_due > (0)::numeric))`; `CREATE INDEX sales_receipt_no_trgm ON app.sales USING gin (receipt_no public.gin_trgm_ops)`; `CREATE INDEX sales_seller_idx ON app.sales USING btree (sold_by, business_date DESC)`; `CREATE UNIQUE INDEX sales_tenant_id_client_ref_key ON app.sales USING btree (client_ref)`; `CREATE UNIQUE INDEX sales_tenant_id_id_key ON app.sales USING btree (id)`; `CREATE UNIQUE INDEX sales_tenant_id_receipt_no_key ON app.sales USING btree (receipt_no)`


## app.scheduled_job_marks

One row per tenant, job kind and local date: makes the daily scheduler safe to run from several processes.

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| kind | text | NO |  | PK |  |
| run_date | date | NO |  | PK |  |
| created_at | timestamp with time zone | NO | now() |  |  |

**Constraints:**

**Indexes:** `CREATE UNIQUE INDEX scheduled_job_marks_pkey ON app.scheduled_job_marks USING btree (kind, run_date)`


## app.service_package_items

_(see spec section 5)_

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| package_service_id | uuid | NO |  | PK → services |  |
| component_service_id | uuid | NO |  | PK → services |  |
| seq | smallint | NO |  |  |  |

**Constraints:** `service_package_items_check` CHECK ((package_service_id <> component_service_id)); `service_package_items_seq_check` CHECK ((seq > 0)); `service_package_items_tenant_id_package_service_id_seq_key` UNIQUE (package_service_id, seq)

**Indexes:** `CREATE UNIQUE INDEX service_package_items_pkey ON app.service_package_items USING btree (package_service_id, component_service_id)`; `CREATE UNIQUE INDEX service_package_items_tenant_id_package_service_id_seq_key ON app.service_package_items USING btree (package_service_id, seq)`


## app.service_stage_checklist_items

_(see spec section 5)_

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| service_stage_id | uuid | NO |  | → service_stages |  |
| seq | smallint | NO |  |  |  |
| label | app.short_text | NO |  |  |  |

**Constraints:** `service_stage_checklist_items_seq_check` CHECK ((seq > 0)); `service_stage_checklist_items_tenant_id_id_key` UNIQUE (id); `service_stage_checklist_items_tenant_id_service_stage_id_se_key` UNIQUE (service_stage_id, seq)

**Indexes:** `CREATE UNIQUE INDEX service_stage_checklist_items_pkey ON app.service_stage_checklist_items USING btree (id)`; `CREATE UNIQUE INDEX service_stage_checklist_items_tenant_id_id_key ON app.service_stage_checklist_items USING btree (id)`; `CREATE UNIQUE INDEX service_stage_checklist_items_tenant_id_service_stage_id_se_key ON app.service_stage_checklist_items USING btree (service_stage_id, seq)`


## app.service_stage_documents

_(see spec section 5)_

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| service_stage_id | uuid | NO |  | PK → service_stages |  |
| document_type_id | uuid | NO |  | PK → document_types |  |
| is_mandatory | boolean | NO | true |  |  |

**Constraints:**

**Indexes:** `CREATE UNIQUE INDEX service_stage_documents_pkey ON app.service_stage_documents USING btree (service_stage_id, document_type_id)`


## app.service_stages

_(see spec section 5)_

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| service_id | uuid | NO |  | → services |  |
| seq | smallint | NO |  |  |  |
| name | app.short_text | NO |  |  |  |
| estimated_days | smallint | NO | 1 |  |  |
| is_branding_stage | boolean | NO | false |  |  |

**Constraints:** `service_stages_estimated_days_check` CHECK (((estimated_days >= 0) AND (estimated_days <= 365))); `service_stages_seq_check` CHECK ((seq > 0)); `service_stages_tenant_id_id_key` UNIQUE (id); `service_stages_tenant_id_service_id_seq_key` UNIQUE (service_id, seq)

**Indexes:** `CREATE UNIQUE INDEX service_stages_pkey ON app.service_stages USING btree (id)`; `CREATE UNIQUE INDEX service_stages_tenant_id_id_key ON app.service_stages USING btree (id)`; `CREATE UNIQUE INDEX service_stages_tenant_id_service_id_seq_key ON app.service_stages USING btree (service_id, seq)`


## app.services

_(see spec section 5)_

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| code | text | NO |  |  |  |
| name | app.short_text | NO |  |  |  |
| description | text | yes |  |  |  |
| category | app.service_category | NO |  |  |  |
| is_package | boolean | NO | false |  |  |
| standard_price | app.money_nn | NO |  |  |  |
| government_fee | app.money_nn | NO | 0 |  |  |
| estimated_days | smallint | yes |  |  |  |
| revisions_included | smallint | NO | 0 |  |  |
| creates_renewal_months | smallint | yes |  |  |  |
| status | app.record_status | NO | 'active'::app.record_status |  |  |
| created_at | timestamp with time zone | NO | now() |  |  |
| created_by | uuid | yes | app.current_membership_id() | → memberships |  |
| updated_at | timestamp with time zone | NO | now() |  |  |
| updated_by | uuid | yes |  | → memberships |  |
| row_version | integer | NO | 1 |  |  |

**Constraints:** `services_code_check` CHECK ((code ~ '^[A-Z0-9_-]{2,30}$'::text)); `services_creates_renewal_months_check` CHECK (((creates_renewal_months >= 1) AND (creates_renewal_months <= 120))); `services_description_check` CHECK ((length(description) <= 4000)); `services_estimated_days_check` CHECK (((estimated_days >= 0) AND (estimated_days <= 3650))); `services_revisions_included_check` CHECK (((revisions_included >= 0) AND (revisions_included <= 50))); `services_tenant_id_code_key` UNIQUE (code); `services_tenant_id_id_key` UNIQUE (id)

**Indexes:** `CREATE UNIQUE INDEX services_pkey ON app.services USING btree (id)`; `CREATE UNIQUE INDEX services_tenant_id_code_key ON app.services USING btree (code)`; `CREATE UNIQUE INDEX services_tenant_id_id_key ON app.services USING btree (id)`


## app.setting_values

Operational settings without a typed column in tenant_settings. Keys are validated by the API (org settings specification).

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| key | text | NO |  | PK |  |
| value | jsonb | NO |  |  |  |
| updated_at | timestamp with time zone | NO | now() |  |  |
| updated_by | uuid | yes | app.current_membership_id() |  |  |

**Constraints:** `tenant_setting_values_key_check` CHECK (((key ~ '^[a-z_]+\.[a-z0-9_]+$'::text) AND (length(key) <= 80)))

**Indexes:** `CREATE UNIQUE INDEX tenant_setting_values_pkey ON app.setting_values USING btree (key)`


## app.state_transitions

State machine definitions enforced by app.tg_enforce_transition. Global reference data.

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| entity | text | NO |  | PK |  |
| from_status | text | NO |  | PK |  |
| to_status | text | NO |  | PK |  |

**Constraints:**

**Indexes:** `CREATE UNIQUE INDEX state_transitions_pkey ON app.state_transitions USING btree (entity, from_status, to_status)`


## app.stock_adjustment_lines

_(see spec section 5)_

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| stock_adjustment_id | uuid | NO |  | → stock_adjustments |  |
| product_id | uuid | NO |  | → products |  |
| quantity_delta | app.qty | NO |  |  |  |
| unit_cost | numeric(18,4) | yes |  |  |  |

**Constraints:** `stock_adjustment_lines_quantity_delta_check` CHECK (((quantity_delta)::numeric <> (0)::numeric)); `stock_adjustment_lines_tenant_id_id_key` UNIQUE (id); `stock_adjustment_lines_tenant_id_stock_adjustment_id_produc_key` UNIQUE (stock_adjustment_id, product_id); `stock_adjustment_lines_unit_cost_check` CHECK ((unit_cost >= (0)::numeric))

**Indexes:** `CREATE UNIQUE INDEX stock_adjustment_lines_pkey ON app.stock_adjustment_lines USING btree (id)`; `CREATE UNIQUE INDEX stock_adjustment_lines_tenant_id_id_key ON app.stock_adjustment_lines USING btree (id)`; `CREATE UNIQUE INDEX stock_adjustment_lines_tenant_id_stock_adjustment_id_produc_key ON app.stock_adjustment_lines USING btree (stock_adjustment_id, product_id)`


## app.stock_adjustments

_(see spec section 5)_

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| adjustment_no | text | NO |  |  |  |
| location_id | uuid | NO |  | → locations |  |
| reason | app.adjustment_reason | NO |  |  |  |
| note | text | yes |  |  |  |
| status | app.doc_post_status | NO | 'draft'::app.doc_post_status |  |  |
| posted_at | timestamp with time zone | yes |  |  |  |
| business_date | date | NO |  |  |  |
| created_at | timestamp with time zone | NO | now() |  |  |
| created_by | uuid | yes | app.current_membership_id() | → memberships |  |
| updated_at | timestamp with time zone | NO | now() |  |  |
| updated_by | uuid | yes |  | → memberships |  |
| row_version | integer | NO | 1 |  |  |

**Constraints:** `stock_adjustments_check` CHECK (((status <> 'posted'::app.doc_post_status) OR (posted_at IS NOT NULL))); `stock_adjustments_check1` CHECK (((reason <> 'other'::app.adjustment_reason) OR (note IS NOT NULL))); `stock_adjustments_note_check` CHECK ((length(note) <= 1000)); `stock_adjustments_tenant_id_adjustment_no_key` UNIQUE (adjustment_no); `stock_adjustments_tenant_id_id_key` UNIQUE (id)

**Indexes:** `CREATE UNIQUE INDEX stock_adjustments_pkey ON app.stock_adjustments USING btree (id)`; `CREATE UNIQUE INDEX stock_adjustments_tenant_id_adjustment_no_key ON app.stock_adjustments USING btree (adjustment_no)`; `CREATE UNIQUE INDEX stock_adjustments_tenant_id_id_key ON app.stock_adjustments USING btree (id)`


## app.stock_balances

Derived. Written ONLY by app.tg_stock_movement_apply. Row lock serialises concurrent sales of the same product.

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| location_id | uuid | NO |  | PK → locations |  |
| product_id | uuid | NO |  | PK → products |  |
| quantity_on_hand | app.qty | NO | 0 |  |  |
| updated_at | timestamp with time zone | NO | now() |  |  |

**Constraints:**

**Indexes:** `CREATE UNIQUE INDEX stock_balances_pkey ON app.stock_balances USING btree (location_id, product_id)`; `CREATE INDEX stock_balances_product_idx ON app.stock_balances USING btree (product_id)`


## app.stock_count_lines

_(see spec section 5)_

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| stock_count_id | uuid | NO |  | PK → stock_counts |  |
| product_id | uuid | NO |  | PK → products |  |
| system_qty | app.qty | NO |  |  |  |
| counted_qty | app.qty | yes |  |  |  |
| counted_by | uuid | yes |  | → memberships |  |
| counted_at | timestamp with time zone | yes |  |  |  |

**Constraints:** `stock_count_lines_counted_qty_check` CHECK (((counted_qty)::numeric >= (0)::numeric))

**Indexes:** `CREATE UNIQUE INDEX stock_count_lines_pkey ON app.stock_count_lines USING btree (stock_count_id, product_id)`


## app.stock_counts

_(see spec section 5)_

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| location_id | uuid | NO |  | → locations |  |
| status | app.count_status | NO | 'in_progress'::app.count_status |  |  |
| stock_adjustment_id | uuid | yes |  | → stock_adjustments |  |
| completed_at | timestamp with time zone | yes |  |  |  |
| created_at | timestamp with time zone | NO | now() |  |  |
| created_by | uuid | yes | app.current_membership_id() | → memberships |  |
| updated_at | timestamp with time zone | NO | now() |  |  |
| updated_by | uuid | yes |  | → memberships |  |
| row_version | integer | NO | 1 |  |  |

**Constraints:** `stock_counts_tenant_id_id_key` UNIQUE (id)

**Indexes:** `CREATE UNIQUE INDEX stock_counts_pkey ON app.stock_counts USING btree (id)`; `CREATE UNIQUE INDEX stock_counts_one_open ON app.stock_counts USING btree (location_id) WHERE (status = 'in_progress'::app.count_status)`; `CREATE UNIQUE INDEX stock_counts_tenant_id_id_key ON app.stock_counts USING btree (id)`


## app.stock_movements

Append-only stock ledger. Every stock change is one row; stock_balances is a trigger-maintained projection. "Every movement shows who did it" (spec §6) = created_by.

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| location_id | uuid | NO |  | → locations |  |
| product_id | uuid | NO |  | → products |  |
| movement_type | app.stock_movement_type | NO |  |  |  |
| quantity_delta | app.qty | NO |  |  |  |
| unit_cost | numeric(18,4) | yes |  |  |  |
| business_date | date | NO |  |  |  |
| occurred_at | timestamp with time zone | NO | now() |  |  |
| sale_line_id | uuid | yes |  | → sale_lines |  |
| sale_return_line_id | uuid | yes |  | → sale_return_lines |  |
| goods_receipt_line_id | uuid | yes |  | → goods_receipt_lines |  |
| stock_adjustment_line_id | uuid | yes |  | → stock_adjustment_lines |  |
| stock_transfer_line_id | uuid | yes |  | → stock_transfer_lines |  |
| created_at | timestamp with time zone | NO | now() |  |  |
| created_by | uuid | yes | app.current_membership_id() | → memberships |  |

**Constraints:** `stock_movements_check` CHECK ((num_nonnulls(sale_line_id, sale_return_line_id, goods_receipt_line_id, stock_adjustment_line_id, stock_transfer_line_id) = 1)); `stock_movements_check1` CHECK (
CASE movement_type
    WHEN 'purchase_receipt'::app.stock_movement_type THEN ((goods_receipt_line_id IS NOT NULL) AND ((quantity_delta)::numeric > (0)::numeric))
    WHEN 'sale'::app.stock_movement_type THEN ((sale_line_id IS NOT NULL) AND ((quantity_delta)::numeric < (0)::numeric))
    WHEN 'sale_void'::app.stock_movement_type THEN ((sale_line_id IS NOT NULL) AND ((quantity_delta)::numeric > (0)::numeric))
    WHEN 'sale_return'::app.stock_movement_type THEN ((sale_return_line_id IS NOT NULL) AND ((quantity_delta)::numeric > (0)::numeric))
    WHEN 'adjustment'::app.stock_movement_type THEN (stock_adjustment_line_id IS NOT NULL)
    WHEN 'transfer_out'::app.stock_movement_type THEN ((stock_transfer_line_id IS NOT NULL) AND ((quantity_delta)::numeric < (0)::numeric))
    WHEN 'transfer_in'::app.stock_movement_type THEN ((stock_transfer_line_id IS NOT NULL) AND ((quantity_delta)::numeric > (0)::numeric))
    ELSE NULL::boolean
END); `stock_movements_quantity_delta_check` CHECK (((quantity_delta)::numeric <> (0)::numeric)); `stock_movements_tenant_id_id_key` UNIQUE (id); `stock_movements_unit_cost_check` CHECK ((unit_cost >= (0)::numeric))

**Indexes:** `CREATE UNIQUE INDEX stock_movements_pkey ON app.stock_movements USING btree (id)`; `CREATE INDEX stock_movements_location_date_idx ON app.stock_movements USING btree (location_id, business_date)`; `CREATE UNIQUE INDEX stock_movements_once ON app.stock_movements USING btree (movement_type, COALESCE(sale_line_id, sale_return_line_id, goods_receipt_line_id, stock_adjustment_line_id, stock_transfer_line_id))`; `CREATE INDEX stock_movements_product_idx ON app.stock_movements USING btree (product_id, occurred_at DESC)`; `CREATE UNIQUE INDEX stock_movements_tenant_id_id_key ON app.stock_movements USING btree (id)`


## app.stock_transfer_lines

_(see spec section 5)_

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| stock_transfer_id | uuid | NO |  | → stock_transfers |  |
| product_id | uuid | NO |  | → products |  |
| quantity | app.qty_pos | NO |  |  |  |

**Constraints:** `stock_transfer_lines_tenant_id_id_key` UNIQUE (id); `stock_transfer_lines_tenant_id_stock_transfer_id_product_id_key` UNIQUE (stock_transfer_id, product_id)

**Indexes:** `CREATE UNIQUE INDEX stock_transfer_lines_pkey ON app.stock_transfer_lines USING btree (id)`; `CREATE UNIQUE INDEX stock_transfer_lines_tenant_id_id_key ON app.stock_transfer_lines USING btree (id)`; `CREATE UNIQUE INDEX stock_transfer_lines_tenant_id_stock_transfer_id_product_id_key ON app.stock_transfer_lines USING btree (stock_transfer_id, product_id)`


## app.stock_transfers

_(see spec section 5)_

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| transfer_no | text | NO |  |  |  |
| from_location_id | uuid | NO |  | → locations |  |
| to_location_id | uuid | NO |  | → locations |  |
| status | app.transfer_status | NO | 'draft'::app.transfer_status |  |  |
| dispatched_at | timestamp with time zone | yes |  |  |  |
| dispatched_by | uuid | yes |  | → memberships |  |
| received_at | timestamp with time zone | yes |  |  |  |
| received_by | uuid | yes |  | → memberships |  |
| note | text | yes |  |  |  |
| created_at | timestamp with time zone | NO | now() |  |  |
| created_by | uuid | yes | app.current_membership_id() | → memberships |  |
| updated_at | timestamp with time zone | NO | now() |  |  |
| updated_by | uuid | yes |  | → memberships |  |
| row_version | integer | NO | 1 |  |  |

**Constraints:** `stock_transfers_check` CHECK ((from_location_id <> to_location_id)); `stock_transfers_check1` CHECK (((status <> ALL (ARRAY['dispatched'::app.transfer_status, 'received'::app.transfer_status])) OR (dispatched_at IS NOT NULL))); `stock_transfers_check2` CHECK (((status <> 'received'::app.transfer_status) OR (received_at IS NOT NULL))); `stock_transfers_note_check` CHECK ((length(note) <= 1000)); `stock_transfers_tenant_id_id_key` UNIQUE (id); `stock_transfers_tenant_id_transfer_no_key` UNIQUE (transfer_no)

**Indexes:** `CREATE UNIQUE INDEX stock_transfers_pkey ON app.stock_transfers USING btree (id)`; `CREATE UNIQUE INDEX stock_transfers_tenant_id_id_key ON app.stock_transfers USING btree (id)`; `CREATE UNIQUE INDEX stock_transfers_tenant_id_transfer_no_key ON app.stock_transfers USING btree (transfer_no)`


## app.suppliers

_(see spec section 5)_

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| name | app.short_text | NO |  |  |  |
| contact_person | text | yes |  |  |  |
| phone | app.phone_e164 | yes |  |  |  |
| email | app.email | yes |  |  |  |
| address | text | yes |  |  |  |
| tin | text | yes |  |  |  |
| goods_supplied | text | yes |  |  |  |
| payment_terms_days | smallint | yes |  |  |  |
| status | app.record_status | NO | 'active'::app.record_status |  |  |
| created_at | timestamp with time zone | NO | now() |  |  |
| created_by | uuid | yes | app.current_membership_id() | → memberships |  |
| updated_at | timestamp with time zone | NO | now() |  |  |
| updated_by | uuid | yes |  | → memberships |  |
| row_version | integer | NO | 1 |  |  |

**Constraints:** `suppliers_address_check` CHECK ((length(address) <= 500)); `suppliers_contact_person_check` CHECK ((length(contact_person) <= 200)); `suppliers_goods_supplied_check` CHECK ((length(goods_supplied) <= 1000)); `suppliers_payment_terms_days_check` CHECK (((payment_terms_days >= 0) AND (payment_terms_days <= 365))); `suppliers_tenant_id_id_key` UNIQUE (id); `suppliers_tin_check` CHECK ((tin ~ '^[0-9]{3}-?[0-9]{3}-?[0-9]{3}$'::text))

**Indexes:** `CREATE UNIQUE INDEX suppliers_pkey ON app.suppliers USING btree (id)`; `CREATE UNIQUE INDEX suppliers_name_uq ON app.suppliers USING btree (lower((name)::text))`; `CREATE UNIQUE INDEX suppliers_tenant_id_id_key ON app.suppliers USING btree (id)`


## app.tasks

_(see spec section 5)_

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| title | app.short_text | NO |  |  |  |
| description | text | yes |  |  |  |
| customer_id | uuid | yes |  | → customers |  |
| project_id | uuid | yes |  | → projects |  |
| project_stage_id | uuid | yes |  | → project_stages |  |
| design_job_id | uuid | yes |  | → design_jobs |  |
| assignee_id | uuid | NO |  | → memberships |  |
| due_at | timestamp with time zone | yes |  |  |  |
| priority | app.priority | NO | 'normal'::app.priority |  |  |
| status | app.task_status | NO | 'open'::app.task_status |  |  |
| completed_at | timestamp with time zone | yes |  |  |  |
| created_at | timestamp with time zone | NO | now() |  |  |
| created_by | uuid | yes | app.current_membership_id() | → memberships |  |
| updated_at | timestamp with time zone | NO | now() |  |  |
| updated_by | uuid | yes |  | → memberships |  |
| row_version | integer | NO | 1 |  |  |

**Constraints:** `tasks_check` CHECK (((status <> 'done'::app.task_status) OR (completed_at IS NOT NULL))); `tasks_check1` CHECK (((project_stage_id IS NULL) OR (project_id IS NOT NULL))); `tasks_description_check` CHECK ((length(description) <= 4000)); `tasks_tenant_id_id_key` UNIQUE (id)

**Indexes:** `CREATE UNIQUE INDEX tasks_pkey ON app.tasks USING btree (id)`; `CREATE INDEX tasks_assignee_idx ON app.tasks USING btree (assignee_id, due_at) WHERE (status = ANY (ARRAY['open'::app.task_status, 'in_progress'::app.task_status]))`; `CREATE INDEX tasks_overdue_idx ON app.tasks USING btree (due_at) WHERE (status = ANY (ARRAY['open'::app.task_status, 'in_progress'::app.task_status]))`; `CREATE INDEX tasks_project_idx ON app.tasks USING btree (project_id) WHERE (project_id IS NOT NULL)`; `CREATE UNIQUE INDEX tasks_tenant_id_id_key ON app.tasks USING btree (id)`


## app.tax_rates

_(see spec section 5)_

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| name | app.short_text | NO |  |  |  |
| rate_pct | app.pct | NO |  |  |  |
| is_default | boolean | NO | false |  |  |
| status | app.record_status | NO | 'active'::app.record_status |  |  |
| created_at | timestamp with time zone | NO | now() |  |  |
| created_by | uuid | yes | app.current_membership_id() | → memberships |  |
| updated_at | timestamp with time zone | NO | now() |  |  |
| updated_by | uuid | yes |  | → memberships |  |
| row_version | integer | NO | 1 |  |  |

**Constraints:** `tax_rates_tenant_id_id_key` UNIQUE (id); `tax_rates_tenant_id_name_key` UNIQUE (name)

**Indexes:** `CREATE UNIQUE INDEX tax_rates_pkey ON app.tax_rates USING btree (id)`; `CREATE UNIQUE INDEX tax_rates_one_default ON app.tax_rates USING btree ((true)) WHERE is_default`; `CREATE UNIQUE INDEX tax_rates_tenant_id_id_key ON app.tax_rates USING btree (id)`; `CREATE UNIQUE INDEX tax_rates_tenant_id_name_key ON app.tax_rates USING btree (name)`


## audit.audit_log

Immutable audit trail (spec §10 Audit Log; §12 History tab). Monthly partitions. row_hash = SHA-256 of the canonical row; audit_seals chains hashes per tenant so deletion or edits are detectable.

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| seq | bigint | NO | nextval('audit.audit_log_seq'::regclass) | PK |  |
| id | uuid | NO | gen_random_uuid() |  |  |
| occurred_at | timestamp with time zone | NO | clock_timestamp() | PK |  |
| legacy_scope | uuid | yes |  |  | Value covered by the row hash of entries written before the single-business migration. NULL for new rows. |
| actor_type | text | NO |  |  |  |
| actor_user_id | uuid | yes |  |  |  |
| actor_membership_id | uuid | yes |  |  |  |
| action | text | NO |  |  |  |
| entity_table | text | yes |  |  |  |
| entity_id | uuid | yes |  |  |  |
| old_values | jsonb | yes |  |  |  |
| new_values | jsonb | yes |  |  |  |
| changed_columns | text[] | yes |  |  |  |
| outcome | text | NO | 'success'::text |  |  |
| reason | text | yes |  |  |  |
| request_id | text | yes |  |  |  |
| client_ip | inet | yes |  |  |  |
| user_agent | text | yes |  |  |  |
| row_hash | bytea | NO |  |  |  |

**Constraints:** `audit_log_action_check` CHECK ((action ~ '^[a-z_]+(\.[a-z_]+)*$'::text)); `audit_log_actor_type_check` CHECK ((actor_type = ANY (ARRAY['user'::text, 'system'::text, 'client_link'::text, 'device'::text, 'service_account'::text]))); `audit_log_outcome_check` CHECK ((outcome = ANY (ARRAY['success'::text, 'denied'::text, 'failure'::text]))); `audit_log_reason_check` CHECK ((length(reason) <= 2000)); `audit_log_request_id_check` CHECK ((length(request_id) <= 100)); `audit_log_user_agent_check` CHECK ((length(user_agent) <= 512))

**Indexes:** `CREATE UNIQUE INDEX audit_log_pkey ON ONLY audit.audit_log USING btree (occurred_at, seq)`; `CREATE INDEX audit_actor_idx ON ONLY audit.audit_log USING btree (actor_membership_id, occurred_at DESC)`; `CREATE INDEX audit_entity_idx ON ONLY audit.audit_log USING btree (entity_table, entity_id, occurred_at)`; `CREATE INDEX audit_seq_idx ON ONLY audit.audit_log USING btree (seq)`; `CREATE INDEX audit_tenant_time_idx ON ONLY audit.audit_log USING btree (occurred_at DESC)`


## audit.audit_seals

Run audit.seal_all() every 15 min (worker). Export seal hashes daily to WORM object storage so even a DB superuser cannot rewrite history unnoticed.

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| tenant_key | uuid | NO |  | PK |  |
| seal_no | bigint | NO |  | PK |  |
| from_seq | bigint | NO |  |  |  |
| to_seq | bigint | NO |  |  |  |
| row_count | integer | NO |  |  |  |
| prev_chain_hash | bytea | yes |  |  |  |
| chain_hash | bytea | NO |  |  |  |
| sealed_at | timestamp with time zone | NO | now() |  |  |

**Constraints:** `audit_seals_check` CHECK ((to_seq >= from_seq)); `audit_seals_row_count_check` CHECK ((row_count > 0))

**Indexes:** `CREATE UNIQUE INDEX audit_seals_pkey ON audit.audit_seals USING btree (tenant_key, seal_no)`


## iam.auth_events

Login History screen (spec §10). Append-only, monthly partitions. No FKs so history survives user lifecycle.

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| occurred_at | timestamp with time zone | NO | now() | PK |  |
| event_type | iam.auth_event_type | NO |  |  |  |
| user_id | uuid | yes |  |  |  |
| identifier_hmac | bytea | yes |  |  |  |
| session_id | uuid | yes |  |  |  |
| ip | inet | yes |  |  |  |
| user_agent | text | yes |  |  |  |
| geo_country | text | yes |  |  |  |
| geo_city | text | yes |  |  |  |
| failure_reason | text | yes |  |  |  |

**Constraints:** `auth_events_failure_reason_check` CHECK ((failure_reason = ANY (ARRAY['bad_password'::text, 'unknown_identifier'::text, 'locked'::text, 'disabled'::text, 'mfa_failed'::text, 'membership_inactive'::text]))); `auth_events_user_agent_check` CHECK ((length(user_agent) <= 512))

**Indexes:** `CREATE UNIQUE INDEX auth_events_pkey ON ONLY iam.auth_events USING btree (occurred_at, id)`; `CREATE INDEX auth_events_tenant_idx ON ONLY iam.auth_events USING btree (occurred_at DESC)`; `CREATE INDEX auth_events_user_idx ON ONLY iam.auth_events USING btree (user_id, occurred_at DESC)`


## iam.password_history

_(see spec section 5)_

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| user_id | uuid | NO |  | → users |  |
| password_hash | text | NO |  |  |  |
| created_at | timestamp with time zone | NO | now() |  |  |

**Constraints:** `password_history_password_hash_check` CHECK ((password_hash ~~ '$argon2id$%'::text))

**Indexes:** `CREATE UNIQUE INDEX password_history_pkey ON iam.password_history USING btree (id)`; `CREATE INDEX password_history_user_idx ON iam.password_history USING btree (user_id, created_at DESC)`


## iam.password_reset_requests

_(see spec section 5)_

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| user_id | uuid | NO |  | → users |  |
| channel | iam.otp_channel | NO |  |  |  |
| code_hash | bytea | NO |  |  |  |
| expires_at | timestamp with time zone | NO |  |  |  |
| attempts | smallint | NO | 0 |  |  |
| consumed_at | timestamp with time zone | yes |  |  |  |
| superseded_at | timestamp with time zone | yes |  |  |  |
| requested_ip | inet | yes |  |  |  |
| created_at | timestamp with time zone | NO | now() |  |  |

**Constraints:** `password_reset_requests_attempts_check` CHECK (((attempts >= 0) AND (attempts <= 5))); `password_reset_requests_check` CHECK (((expires_at > created_at) AND (expires_at <= (created_at + '00:30:00'::interval)))); `password_reset_requests_code_hash_check` CHECK ((octet_length(code_hash) = 32))

**Indexes:** `CREATE UNIQUE INDEX password_reset_requests_pkey ON iam.password_reset_requests USING btree (id)`; `CREATE UNIQUE INDEX password_reset_one_live ON iam.password_reset_requests USING btree (user_id) WHERE ((consumed_at IS NULL) AND (superseded_at IS NULL))`


## iam.public_tokens

Resolves a public link token to its tenant before tenant context exists; the API then sets app.tenant_id and loads the target under RLS.

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| token_hash | bytea | NO |  | PK |  |
| purpose | iam.public_token_purpose | NO |  |  |  |
| target_id | uuid | NO |  |  |  |
| expires_at | timestamp with time zone | NO |  |  |  |
| revoked_at | timestamp with time zone | yes |  |  |  |
| created_at | timestamp with time zone | NO | now() |  |  |

**Constraints:** `public_tokens_token_hash_check` CHECK ((octet_length(token_hash) = 32))

**Indexes:** `CREATE UNIQUE INDEX public_tokens_pkey ON iam.public_tokens USING btree (token_hash)`


## iam.rate_limits

_(see spec section 5)_

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| bucket | bytea | NO |  | PK |  |
| window_start | timestamp with time zone | NO |  |  |  |
| hits | integer | NO |  |  |  |

**Constraints:** `rate_limits_bucket_check` CHECK ((octet_length(bucket) = 32)); `rate_limits_hits_check` CHECK ((hits > 0))

**Indexes:** `CREATE UNIQUE INDEX rate_limits_pkey ON iam.rate_limits USING btree (bucket)`; `CREATE INDEX rate_limits_window_idx ON iam.rate_limits USING btree (window_start)`


## iam.session_refresh_tokens

_(see spec section 5)_

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| token_hash | bytea | NO |  | PK |  |
| session_id | uuid | NO |  | → sessions |  |
| user_id | uuid | NO |  | → users |  |
| replaced_at | timestamp with time zone | NO | now() |  |  |

**Constraints:** `session_refresh_tokens_token_hash_check` CHECK ((octet_length(token_hash) = 32))

**Indexes:** `CREATE UNIQUE INDEX session_refresh_tokens_pkey ON iam.session_refresh_tokens USING btree (token_hash)`; `CREATE INDEX session_refresh_tokens_session_idx ON iam.session_refresh_tokens USING btree (session_id)`


## iam.sessions

_(see spec section 5)_

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| user_id | uuid | NO |  | → users |  |
| membership_id | uuid | yes |  | → memberships |  |
| token_hash | bytea | NO |  |  |  |
| created_at | timestamp with time zone | NO | now() |  |  |
| last_seen_at | timestamp with time zone | NO | now() |  |  |
| idle_timeout | interval | NO | '00:30:00'::interval |  |  |
| absolute_expires_at | timestamp with time zone | NO |  |  |  |
| remember_me | boolean | NO | false |  |  |
| ip | inet | yes |  |  |  |
| user_agent | text | yes |  |  |  |
| revoked_at | timestamp with time zone | yes |  |  |  |
| revoked_reason | text | yes |  |  |  |
| reauthenticated_at | timestamp with time zone | NO | now() |  |  |

**Constraints:** `sessions_check` CHECK ((absolute_expires_at > created_at)); `sessions_check1` CHECK (((revoked_at IS NULL) = (revoked_reason IS NULL))); `sessions_revoked_reason_check` CHECK ((revoked_reason = ANY (ARRAY['logout'::text, 'idle_timeout'::text, 'admin_force_signout'::text, 'password_changed'::text, 'user_disabled'::text, 'membership_revoked'::text, 'expired'::text, 'token_reuse'::text]))); `sessions_token_hash_check` CHECK ((octet_length(token_hash) = 32)); `sessions_token_hash_key` UNIQUE (token_hash); `sessions_user_agent_check` CHECK ((length(user_agent) <= 512))

**Indexes:** `CREATE UNIQUE INDEX sessions_pkey ON iam.sessions USING btree (id)`; `CREATE INDEX sessions_tenant_live_idx ON iam.sessions USING btree (membership_id) WHERE (revoked_at IS NULL)`; `CREATE UNIQUE INDEX sessions_token_hash_key ON iam.sessions USING btree (token_hash)`; `CREATE INDEX sessions_user_live_idx ON iam.sessions USING btree (user_id) WHERE (revoked_at IS NULL)`


## iam.user_credentials

Argon2id hashes only. Kept apart from iam.users so profile queries never touch secrets.

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| user_id | uuid | NO |  | PK → users |  |
| password_hash | text | NO |  |  |  |
| password_changed_at | timestamp with time zone | NO | now() |  |  |
| must_change_password | boolean | NO | true |  | True for temporary passwords set by Admin (spec §4 First login). |
| failed_attempts | integer | NO | 0 |  |  |
| last_failed_at | timestamp with time zone | yes |  |  |  |
| locked_until | timestamp with time zone | yes |  |  |  |
| updated_at | timestamp with time zone | NO | now() |  |  |

**Constraints:** `user_credentials_failed_attempts_check` CHECK ((failed_attempts >= 0)); `user_credentials_password_hash_check` CHECK ((password_hash ~~ '$argon2id$%'::text))

**Indexes:** `CREATE UNIQUE INDEX user_credentials_pkey ON iam.user_credentials USING btree (user_id)`


## iam.users

Global person identity. Login is by phone or email + password (spec §4).

| Column | Type | Null | Default | Key / FK | Notes |
|---|---|---|---|---|---|
| id | uuid | NO | gen_random_uuid() | PK |  |
| full_name | app.short_text | NO |  |  |  |
| email | app.email | yes |  |  |  |
| phone | app.phone_e164 | yes |  |  |  |
| preferred_locale | text | NO | 'sw'::text |  |  |
| avatar_storage_key | text | yes |  |  |  |
| status | iam.user_status | NO | 'active'::iam.user_status |  |  |
| created_at | timestamp with time zone | NO | now() |  |  |
| updated_at | timestamp with time zone | NO | now() |  |  |
| updated_by | uuid | yes |  |  |  |
| row_version | integer | NO | 1 |  |  |

**Constraints:** `users_email_key` UNIQUE (email); `users_has_login_identifier` CHECK (((email IS NOT NULL) OR (phone IS NOT NULL))); `users_phone_key` UNIQUE (phone); `users_preferred_locale_check` CHECK ((preferred_locale = ANY (ARRAY['sw'::text, 'en'::text])))

**Indexes:** `CREATE UNIQUE INDEX users_pkey ON iam.users USING btree (id)`; `CREATE UNIQUE INDEX users_email_key ON iam.users USING btree (email)`; `CREATE UNIQUE INDEX users_phone_key ON iam.users USING btree (phone)`
