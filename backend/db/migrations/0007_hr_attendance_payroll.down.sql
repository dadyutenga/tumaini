ALTER TABLE pay_adjustments DROP CONSTRAINT IF EXISTS pay_adjustments_payslip_fk;
ALTER TABLE loan_repayments DROP CONSTRAINT IF EXISTS loan_repayments_payslip_fk;
ALTER TABLE sales DROP CONSTRAINT IF EXISTS sales_employee_fk;
ALTER TABLE departments DROP CONSTRAINT IF EXISTS departments_head_fk;
ALTER TABLE branches DROP CONSTRAINT IF EXISTS branches_manager_fk;
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_employee_fk;
DROP TABLE IF EXISTS payslip_lines, payslips, payroll_runs, employee_components, salary_components,
  attendance_days, attendance_events, holidays, work_schedules, pay_adjustments, commission_rules,
  loan_repayments, employee_loans, leave_requests, leave_types, employees, positions, departments CASCADE;
