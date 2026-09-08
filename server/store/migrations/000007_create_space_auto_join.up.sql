-- Records that a membership originated from the open-space self-join path. Access gates do not
-- consult this provenance. The primary key makes marking a membership idempotent.
CREATE TABLE IF NOT EXISTS DOCS_SpaceAutoJoin (
    SpaceId VARCHAR(26) NOT NULL,
    UserId  VARCHAR(26) NOT NULL,
    PRIMARY KEY (SpaceId, UserId)
);
