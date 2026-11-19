DROP TABLE IF EXISTS attachments;
DROP TABLE IF EXISTS user_permissions;

ALTER TABLE commission_rules DROP CONSTRAINT IF EXISTS commission_rules_calc_check;
ALTER TABLE commission_rules
  ADD CONSTRAINT commission_rules_calc_check
  CHECK (calc IN ('percent_of_sales', 'fixed_per_sale'));
