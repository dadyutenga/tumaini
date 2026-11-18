-- Wrong codes are counted so a 6-digit reset code cannot be guessed by brute force.
ALTER TABLE password_resets ADD COLUMN IF NOT EXISTS attempts integer NOT NULL DEFAULT 0;
