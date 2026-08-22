-- Auto-join provenance: one row per (space, member) that the auto-join pre-step added, telling
-- auto-joined members apart from deliberately added ones. Rows live on the plugin's master handle
-- and are read/written under the space membership lock: the undo path deletes a membership only
-- while its marker row exists, so a concurrent legitimization's clear must be observable the
-- moment it commits. The primary key's job is one idempotent row per membership, so no marker
-- write can overwrite another's outcome.
CREATE TABLE IF NOT EXISTS DOCS_SpaceAutoJoin (
    SpaceId VARCHAR(26) NOT NULL,
    UserId  VARCHAR(26) NOT NULL,
    PRIMARY KEY (SpaceId, UserId)
);
