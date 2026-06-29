CREATE TABLE IF NOT EXISTS DOCS_Space (
    Id          VARCHAR(26) PRIMARY KEY,
    ChannelId   VARCHAR(26) NOT NULL,
    TeamId      VARCHAR(26) NOT NULL DEFAULT '',
    Title       VARCHAR(128) NOT NULL,
    Description TEXT NOT NULL DEFAULT '',
    Icon        VARCHAR(256) NOT NULL DEFAULT '',
    CreatorId   VARCHAR(26) NOT NULL DEFAULT '',
    Props       JSONB NOT NULL DEFAULT '{}'::jsonb,
    CreateAt    BIGINT NOT NULL,
    UpdateAt    BIGINT NOT NULL,
    DeleteAt    BIGINT NOT NULL DEFAULT 0,
    SortOrder   BIGINT NOT NULL DEFAULT 0
);

-- Enforce one active space per channel (also serves the channel-to-space lookups).
CREATE UNIQUE INDEX IF NOT EXISTS uq_docs_space_channel_id ON DOCS_Space (ChannelId) WHERE DeleteAt = 0;

-- List-by-team: (TeamId, DeleteAt=0) filtered, ordered by
-- (SortOrder, CreateAt DESC, Id). Sort columns are in the index so paginated
-- listings avoid a filesort and can stop early under LIMIT.
CREATE INDEX IF NOT EXISTS idx_docs_space_teamid ON DOCS_Space (TeamId, SortOrder, CreateAt DESC, Id) WHERE DeleteAt = 0;
