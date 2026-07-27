ALTER TABLE DOCS_Space ADD COLUMN IF NOT EXISTS ViewAccess VARCHAR(16) NOT NULL DEFAULT 'private';

-- The DDL default is 'private' (fail-closed): every plugin insert writes ViewAccess explicitly,
-- so this default only protects a row the application somehow left unset.
ALTER TABLE DOCS_Space ADD CONSTRAINT chk_docs_space_view_access CHECK (ViewAccess IN ('open', 'private'));
