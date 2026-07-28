-- LastActiveAt records the user's own last autosave of the draft, which is what editor presence is
-- derived from. It is distinct from UpdateAt, which can also be bumped by internal maintenance
-- writes that do not reflect user activity and would otherwise report the user as an active editor.
ALTER TABLE DOCS_Draft ADD COLUMN IF NOT EXISTS LastActiveAt BIGINT NOT NULL DEFAULT 0;

-- BaseEditAt is the optimistic-lock baseline: the page EditAt the client saw at edit-open, compared
-- against the page's current EditAt on publish to reject concurrent-edit conflicts. It is write-once:
-- set when the draft row is first inserted and never overwritten by later autosaves. Existing rows
-- default to 0 (no baseline), which fails closed to requiring a forced publish.
ALTER TABLE DOCS_Draft ADD COLUMN IF NOT EXISTS BaseEditAt BIGINT NOT NULL DEFAULT 0;
