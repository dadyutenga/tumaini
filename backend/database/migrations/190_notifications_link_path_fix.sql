-- =====================================================================
-- 190_notifications_link_path_fix.sql — fix POSIX regex bound > 255 on
-- app.notifications.link_path
--
-- PostgreSQL's POSIX regex engine caps {m,n} repetition counts at
-- RE_DUP_MAX = 255, so the original {0,300} in 090 failed at runtime on
-- every non-NULL link_path insert. Express the length bound with length()
-- and keep the character allow-list in the regex.
-- =====================================================================
SET search_path = app, public;

ALTER TABLE app.notifications DROP CONSTRAINT IF EXISTS notifications_link_path_check;
ALTER TABLE app.notifications ADD CONSTRAINT notifications_link_path_check
  CHECK (length(link_path) <= 301 AND link_path ~ '^/[A-Za-z0-9/_?=&.-]*$');
