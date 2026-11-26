-- Cancelling approved leave deletes the system-generated on_leave attendance rows,
-- and an unapplied pay adjustment is removed by DELETE. Approvers can read the
-- payroll run and its lines; otherwise approval returns not-found.
GRANT DELETE ON app.attendance_records, app.employee_pay_adjustments TO bms_app;

DROP POLICY payroll_run_read ON app.payroll_runs;
CREATE POLICY payroll_run_read ON app.payroll_runs AS RESTRICTIVE FOR SELECT
  USING ((SELECT app.has_permission('hr.payroll.view'))
      OR (SELECT app.has_permission('hr.payroll.approve')));

DROP POLICY own_or_payroll_read ON app.payroll_lines;
CREATE POLICY own_or_payroll_read ON app.payroll_lines AS RESTRICTIVE FOR SELECT
  USING ((SELECT app.has_permission('hr.payroll.view'))
      OR (SELECT app.has_permission('hr.payroll.approve'))
      OR app.is_self_employee(employee_id)
      OR current_user = 'bms_owner');
