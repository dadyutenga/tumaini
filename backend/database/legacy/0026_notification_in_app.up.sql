-- FR-SET-08: a notification type can be switched off in the app and still go out by email or SMS.
-- Such a row exists so the delivery job can read it, but it never shows in the user's list or count.
ALTER TABLE notifications ADD COLUMN IF NOT EXISTS in_app boolean NOT NULL DEFAULT true;
