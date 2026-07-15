-- LastActiveAt records the user's own last autosave of the draft, which is what editor presence is
-- derived from. It is distinct from UpdateAt, which can also be bumped by internal maintenance
-- writes that do not reflect user activity and would otherwise report the user as an active editor.
ALTER TABLE DOCS_Draft ADD COLUMN IF NOT EXISTS LastActiveAt BIGINT NOT NULL DEFAULT 0;
