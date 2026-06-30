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

    -- A version snapshot (OriginalId<>'') is always soft-deleted. Space deletion
    -- cascades a soft-delete to all its pages, and every live-page query filters on
    -- DeleteAt=0.
    CONSTRAINT chk_docs_page_snapshot_deleted CHECK (OriginalId = '' OR DeleteAt > 0)
);

-- Children of a page (ParentId, DeleteAt=0) ordered by (SortOrder, CreateAt, Id);
-- the trailing sort columns avoid a filesort for the paginated listing.
CREATE INDEX IF NOT EXISTS idx_docs_page_parentid ON DOCS_Page (ParentId, SortOrder, CreateAt, Id) WHERE DeleteAt = 0;

-- Sibling queries filter (ChannelId, ParentId, DeleteAt=0) and order by (SortOrder, CreateAt, Id);
-- the trailing sort columns make the MAX(SortOrder)-per-sibling-group lookup an index-only
-- scan, and let the sibling-reorder listing avoid a filesort.
CREATE INDEX IF NOT EXISTS idx_docs_page_channelid_parentid ON DOCS_Page (ChannelId, ParentId, SortOrder, CreateAt, Id) WHERE DeleteAt = 0;

-- Space page listing: WHERE SpaceId=? AND OriginalId='' AND DeleteAt=0,
-- ordered by (CreateAt DESC, Id DESC). The trailing sort columns make paginated
-- reads an index scan with limit (no filesort).
CREATE INDEX IF NOT EXISTS idx_docs_page_spaceid ON DOCS_Page (SpaceId, CreateAt DESC, Id DESC) WHERE OriginalId = '' AND DeleteAt = 0;

-- Space soft-delete/un-delete bookkeeping over soft-deleted, non-snapshot pages:
-- WHERE SpaceId=? AND OriginalId='' AND DeleteAt>0. Separate from the live-only
-- SpaceId index above.
CREATE INDEX IF NOT EXISTS idx_docs_page_spaceid_deleted ON DOCS_Page (SpaceId, DeleteAt) WHERE OriginalId = '' AND DeleteAt > 0;
