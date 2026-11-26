-- Allow Admin holders of finance.day.reopen to reopen a closed business day
-- directly without a separate approval_requests row, matching tg_day_closing_guard().
-- Non-admin callers still require both reopen_approval_id and an approved day_reopen
-- request, enforced by app.tg_day_closing_guard().
ALTER TABLE app.day_closings DROP CONSTRAINT IF EXISTS day_closings_reopen_needs_approval;
