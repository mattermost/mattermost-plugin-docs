-- morph:nontransactional
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_docs_page_originalid ON DOCS_Page (OriginalId) WHERE OriginalId <> '';
