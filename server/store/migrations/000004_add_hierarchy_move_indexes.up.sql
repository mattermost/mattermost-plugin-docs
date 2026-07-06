-- Snapshot re-home by original page: rewriteSubtreeSpace filters DOCS_Page
-- WHERE OriginalId IN (...) AND DeleteAt>0 while holding FOR UPDATE locks on the
-- source/target DOCS_Space rows during MovePageToSpace. idx_docs_page_spaceid_deleted
-- only covers the opposite case (OriginalId=''), so this predicate had no index.
CREATE INDEX IF NOT EXISTS idx_docs_page_originalid ON DOCS_Page (OriginalId) WHERE OriginalId <> '';
