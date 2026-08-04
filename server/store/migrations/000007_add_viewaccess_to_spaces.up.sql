ALTER TABLE DOCS_Space ADD COLUMN IF NOT EXISTS ViewAccess VARCHAR(16) NOT NULL DEFAULT 'private';

-- The DDL default is 'private' (fail-closed), so a row written without ViewAccess lands on the more
-- restrictive access level.
ALTER TABLE DOCS_Space ADD CONSTRAINT chk_docs_space_view_access CHECK (ViewAccess IN ('open', 'private'));
