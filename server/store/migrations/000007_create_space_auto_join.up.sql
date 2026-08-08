-- Auto-join provenance: one row per (space, member) that the auto-join pre-step added, letting a
-- membership review and UndoAutoJoin tell auto-joined members from deliberately added ones.
-- Per-membership rows on the plugin's master handle: UndoAutoJoin deletes a membership on the
-- strength of this marker, so it must observe a concurrent legitimization (which clears the
-- marker) the moment it commits, and a clear must never be lost to a concurrent marker write.
CREATE TABLE IF NOT EXISTS DOCS_SpaceAutoJoin (
    SpaceId VARCHAR(26) NOT NULL,
    UserId  VARCHAR(26) NOT NULL,
    PRIMARY KEY (SpaceId, UserId)
);
