-- Issuing a payslip during approval checks the restrictive read policy.
-- An approver who does not also hold hr.payroll.view must still be able to insert it.
DROP POLICY payslip_read ON app.payslips;
CREATE POLICY payslip_read ON app.payslips AS RESTRICTIVE FOR SELECT
  USING ((SELECT app.has_permission('hr.payroll.view'))
      OR (SELECT app.has_permission('hr.payroll.approve'))
      OR EXISTS (SELECT 1 FROM app.payroll_lines l
                  WHERE l.tenant_id = payslips.tenant_id AND l.id = payslips.payroll_line_id
                    AND app.is_self_employee(l.employee_id)));
