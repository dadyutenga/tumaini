-- Indexes for the HR, attendance and payroll query patterns.
-- Commission: completed sales per employee and business day.
CREATE INDEX IF NOT EXISTS sales_employee_day_idx ON sales (employee_id, business_date) WHERE employee_id IS NOT NULL;
-- Payroll gathers one-off adjustments per employee and period.
CREATE INDEX IF NOT EXISTS pay_adjustments_emp_period_idx ON pay_adjustments (employee_id, period);
CREATE INDEX IF NOT EXISTS pay_adjustments_payslip_idx ON pay_adjustments (payslip_id) WHERE payslip_id IS NOT NULL;
-- Leave lists by status and overlap checks.
CREATE INDEX IF NOT EXISTS leave_requests_status_idx ON leave_requests (business_id, status, start_date);
-- Loan detail and payslip loan lines.
CREATE INDEX IF NOT EXISTS loan_repayments_loan_idx ON loan_repayments (loan_id, created_at);
CREATE INDEX IF NOT EXISTS payslip_lines_payslip_idx ON payslip_lines (payslip_id);
CREATE INDEX IF NOT EXISTS payslip_lines_loan_idx ON payslip_lines (loan_id) WHERE loan_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS payslips_employee_idx ON payslips (employee_id);
-- Late, overtime and review lists.
CREATE INDEX IF NOT EXISTS attendance_days_review_idx ON attendance_days (branch_id, day) WHERE needs_review;
CREATE INDEX IF NOT EXISTS attendance_days_overtime_idx ON attendance_days (branch_id, day) WHERE overtime_minutes > 0;
-- Commission rules by employee or position.
CREATE INDEX IF NOT EXISTS commission_rules_emp_idx ON commission_rules (employee_id) WHERE employee_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS commission_rules_pos_idx ON commission_rules (position_id) WHERE position_id IS NOT NULL;
-- One schedule row per branch or employee weekday is already unique; holidays are unique per day.
