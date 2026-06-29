CREATE TABLE IF NOT EXISTS DOCS_Draft (
    UserId   VARCHAR(26) NOT NULL,
    SpaceId  VARCHAR(26) NOT NULL,
    PageId   VARCHAR(26) NOT NULL,
    ParentId VARCHAR(26) NOT NULL DEFAULT '',
    Title    VARCHAR(255) NOT NULL DEFAULT '',
    Body     TEXT NOT NULL DEFAULT '',
    FileIds  TEXT NOT NULL DEFAULT '[]',
    Props    JSONB NOT NULL DEFAULT '{}'::jsonb,
    CreateAt BIGINT NOT NULL,
    UpdateAt BIGINT NOT NULL,
    PRIMARY KEY (UserId, PageId)
);

-- List a user's drafts within a space (sidebar/tree): (UserId, SpaceId) filtered, ordered by
-- UpdateAt DESC; the trailing column lets the index satisfy the sort, no filesort.
CREATE INDEX IF NOT EXISTS idx_docs_draft_user_space ON DOCS_Draft (UserId, SpaceId, UpdateAt DESC);

-- Purge-by-page: DELETE FROM DOCS_Draft WHERE PageId = ?, which the (UserId, PageId)
-- primary key cannot serve because PageId is not its leading column.
CREATE INDEX IF NOT EXISTS idx_docs_draft_pageid ON DOCS_Draft (PageId);
