CREATE TABLE IF NOT EXISTS DOCS_Page (
    Id                          VARCHAR(26) PRIMARY KEY,
    SpaceId                      VARCHAR(26) NOT NULL,
    ChannelId                   VARCHAR(26) NOT NULL,
    ParentId                    VARCHAR(26) NOT NULL DEFAULT '',
    Type                        VARCHAR(26) NOT NULL DEFAULT 'page',
    Title                       VARCHAR(255) NOT NULL DEFAULT '',
    Body                        TEXT NOT NULL DEFAULT '',
    SearchText                  TEXT NOT NULL DEFAULT '',
    UserId                      VARCHAR(26) NOT NULL,
    LastModifiedBy              VARCHAR(26) NOT NULL DEFAULT '',
    SortOrder                   BIGINT NOT NULL DEFAULT 0,
    CreateAt                    BIGINT NOT NULL DEFAULT 0,
    UpdateAt                    BIGINT NOT NULL DEFAULT 0,
    DeleteAt                    BIGINT NOT NULL DEFAULT 0,
    EditAt                      BIGINT NOT NULL DEFAULT 0,
    OriginalId                  VARCHAR(26) NOT NULL DEFAULT '',
    Props                       jsonb NOT NULL DEFAULT '{}'::jsonb,
    ReparentedParentOnDelete    VARCHAR(26),
    ReparentedChildrenOnDelete  TEXT,

    -- A version snapshot (OriginalId<>'') is always soft-deleted. DeleteSpace's
    -- cascade and every live-page query rely on this, so enforce it at the DB level
    -- rather than trusting all current and future write paths to maintain it.
    CONSTRAINT chk_docs_page_snapshot_deleted CHECK (OriginalId = '' OR DeleteAt > 0)
);

-- Child listing: GetPageChildren filters (ParentId, DeleteAt=0) and orders by
-- (SortOrder, CreateAt, Id); the trailing sort columns let the listing avoid a filesort.
CREATE INDEX IF NOT EXISTS idx_docs_page_parentid ON DOCS_Page (ParentId, SortOrder, CreateAt, Id) WHERE DeleteAt = 0;

-- Sibling queries filter (ChannelId, ParentId, DeleteAt=0) and order by (SortOrder, CreateAt, Id);
-- the trailing sort columns make CreatePage's MAX(SortOrder) an index-only lookup, and let
-- the sibling-reorder listing avoid a filesort.
CREATE INDEX IF NOT EXISTS idx_docs_page_channelid_parentid ON DOCS_Page (ChannelId, ParentId, SortOrder, CreateAt, Id) WHERE DeleteAt = 0;

-- List-by-channel: the channel page listings key on (ChannelId, DeleteAt=0, OriginalId='').
-- Partial on OriginalId='' so version snapshots stay out of the index.
CREATE INDEX IF NOT EXISTS idx_docs_page_channelid ON DOCS_Page (ChannelId) WHERE DeleteAt = 0 AND OriginalId = '';

-- Full-text search over Title + SearchText. Partial on live, non-snapshot rows so the
-- index does not bloat with version-history rows. The search query must include
-- DeleteAt=0 AND OriginalId='' for the planner to use this index.
CREATE INDEX IF NOT EXISTS idx_docs_page_search_txt ON DOCS_Page
    USING GIN (to_tsvector('english', COALESCE(Title, '') || ' ' || COALESCE(SearchText, '')))
    WHERE DeleteAt = 0 AND OriginalId = '';

-- Version-history lookup: WHERE OriginalId=pageId AND DeleteAt>0.
CREATE INDEX IF NOT EXISTS idx_docs_page_originalid ON DOCS_Page (OriginalId) WHERE DeleteAt > 0;

-- Space page listing (GetSpacePages) and space page-ID collect: WHERE SpaceId=? AND
-- OriginalId='' AND DeleteAt=0, ordered by (CreateAt DESC, Id DESC). The trailing sort
-- columns make the paginated listing an index scan with limit (no filesort).
CREATE INDEX IF NOT EXISTS idx_docs_page_spaceid ON DOCS_Page (SpaceId, CreateAt DESC, Id DESC) WHERE OriginalId = '' AND DeleteAt = 0;
