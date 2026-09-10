-- Confluence bundle import (single-node restartable V1).
--
-- V1 deliberately has no distributed claiming: there are no claim tokens, leases, heartbeats, or
-- worker instance IDs. One worker goroutine on one application node owns all import work, and
-- restart safety comes from PostgreSQL state plus immutable per-page execution checkpoints.
--
-- Every externally supplied identifier that participates in a B-tree index is length-bounded, and
-- inspection additionally validates it against the ASCII contract pattern ^[A-Za-z0-9._:@-]+$ so
-- index sizing stays deterministic.

-- DOCS_ImportSource: one row identifies one Confluence Space as explicitly chosen by a user
-- within one target Docs Space. It -- not organization id or space key -- scopes page mappings.
CREATE TABLE IF NOT EXISTS DOCS_ImportSource (
    Id                  VARCHAR(26) PRIMARY KEY,
    SpaceId             VARCHAR(26) NOT NULL,
    SourceType          VARCHAR(32) NOT NULL DEFAULT 'confluence',
    DisplayName         VARCHAR(255) NOT NULL DEFAULT '',
    -- Optional organization id is stored as '' rather than SQL NULL so ordinary sqlx scans into the
    -- Go string model stay safe.
    OrganizationId      VARCHAR(255) NOT NULL DEFAULT '',
    ExternalSpaceKey    VARCHAR(255) NOT NULL,
    ExternalSpaceName   TEXT NOT NULL DEFAULT '',
    CreatedBy           VARCHAR(26) NOT NULL,
    CreateAt            BIGINT NOT NULL,
    UpdateAt            BIGINT NOT NULL,
    LastImportAt        BIGINT NOT NULL DEFAULT 0,
    LastSuccessfulJobId VARCHAR(26) NOT NULL DEFAULT '',
    -- MappingRevision replaces full-lifecycle source ownership. Preflight captures it; confirmation
    -- and queued_import -> importing require an exact match; terminalization increments it once when
    -- the job committed any mapping-affecting decision.
    MappingRevision     BIGINT NOT NULL DEFAULT 0,
    Props               jsonb NOT NULL DEFAULT '{}'::jsonb,
    CONSTRAINT chk_docs_importsource_type CHECK (SourceType = 'confluence')
);

CREATE INDEX IF NOT EXISTS idx_docs_importsource_space
    ON DOCS_ImportSource (SpaceId, CreateAt, Id);

-- No uniqueness on organization id, space key, or display name: two Confluence instances may use
-- identical values and must remain selectable as distinct sources.
CREATE INDEX IF NOT EXISTS idx_docs_importsource_candidate
    ON DOCS_ImportSource (SpaceId, SourceType, ExternalSpaceKey, OrganizationId, CreateAt);

-- DOCS_ImportJob: one restartable import lifecycle. TargetSpaceId is generated before insert (even
-- for a new target) so it is a stable planned id until execution creates DOCS_Space.
CREATE TABLE IF NOT EXISTS DOCS_ImportJob (
    Id                        VARCHAR(26) PRIMARY KEY,
    ActorId                   VARCHAR(26) NOT NULL,
    TeamId                    VARCHAR(26) NOT NULL,

    TargetKind                VARCHAR(16) NOT NULL,
    TargetSpaceId             VARCHAR(26) NOT NULL,
    TargetSpaceExisted        BOOLEAN NOT NULL,
    ConfirmedSpaceTitle       VARCHAR(128) NOT NULL DEFAULT '',
    ConfirmedSpaceDescription TEXT NOT NULL DEFAULT '',
    ProvisionedChannelId      VARCHAR(26) NOT NULL DEFAULT '',

    SourceSelectionMode       VARCHAR(16) NOT NULL DEFAULT '',
    SelectedImportSourceId    VARCHAR(26) NOT NULL DEFAULT '',
    SelectedSourceDisplayName VARCHAR(255) NOT NULL DEFAULT '',
    -- Mapping revision captured while computing preflight; rechecked at confirmation and again
    -- immediately before execution starts.
    PreflightMappingRevision  BIGINT NOT NULL DEFAULT 0,

    State                     VARCHAR(32) NOT NULL,
    Phase                     VARCHAR(64) NOT NULL DEFAULT '',
    -- TerminalIntent makes terminalization restartable: it records which terminal outcome the
    -- terminalizer must durably produce before leaving the terminalizing state.
    TerminalIntent            VARCHAR(16) NOT NULL DEFAULT '',
    -- Set by any page transaction whose mapping decision changed a field later preflights consume;
    -- terminalization reads it once to decide whether to bump the source MappingRevision.
    MappingInputsChanged      BOOLEAN NOT NULL DEFAULT FALSE,
    -- Set when a terminal job committed tree changes and the channel-scoped invalidation event has
    -- not been published yet.
    InvalidationPending       BOOLEAN NOT NULL DEFAULT FALSE,
    ProgressCurrent           BIGINT NOT NULL DEFAULT 0,
    ProgressTotal             BIGINT NOT NULL DEFAULT 0,
    -- Purgeable staged-page bytes (bodies, SearchText, titles, source props, staged metadata).
    StagedBytes               BIGINT NOT NULL DEFAULT 0,
    -- Actual durable manifest-user, result, issue, and summary bytes.
    RetainedBytes             BIGINT NOT NULL DEFAULT 0,
    -- The discretionary (issue-row) share of RetainedBytes, tracked separately so issue writers are
    -- bounded by their own flat allowance and cannot spend the capacity held for mandatory outcomes.
    RetainedIssueBytes        BIGINT NOT NULL DEFAULT 0,
    -- What the current preflight-stage rows contribute to the two figures above. Preflight is republished
    -- wholesale on every recomputation, so the charge has to be replaceable: without knowing what the
    -- previous plan cost, a recompute would either double-count it or lose it entirely.
    PreflightRetainedBytes      BIGINT NOT NULL DEFAULT 0,
    PreflightRetainedIssueBytes BIGINT NOT NULL DEFAULT 0,
    -- Conservative budget reserved at upload for preflight and mandatory terminal report rows, so an
    -- admitted job can always afford its terminal outcome.
    RetainedReservedBytes     BIGINT NOT NULL DEFAULT 0,

    BundleSha256              VARCHAR(64) NOT NULL,
    BundleSummary             jsonb NOT NULL DEFAULT '{}'::jsonb,
    PreflightSummary          jsonb NOT NULL DEFAULT '{}'::jsonb,
    PreflightRevision         VARCHAR(64) NOT NULL DEFAULT '',
    Confirmation              jsonb NOT NULL DEFAULT '{}'::jsonb,
    FinalSummary              jsonb NOT NULL DEFAULT '{}'::jsonb,

    ErrorCode                 VARCHAR(64) NOT NULL DEFAULT '',
    ErrorMessage              TEXT NOT NULL DEFAULT '',
    CancelRequestedAt         BIGINT NOT NULL DEFAULT 0,

    CreateAt                  BIGINT NOT NULL,
    UpdateAt                  BIGINT NOT NULL,
    ConfirmedAt               BIGINT NOT NULL DEFAULT 0,
    StartedAt                 BIGINT NOT NULL DEFAULT 0,
    FinishedAt                BIGINT NOT NULL DEFAULT 0,
    RetainUntil               BIGINT NOT NULL,

    CONSTRAINT chk_docs_importjob_target
        CHECK (TargetKind IN ('new', 'existing')),
    CONSTRAINT chk_docs_importjob_source_mode
        CHECK (SourceSelectionMode IN ('', 'new', 'existing')),
    CONSTRAINT chk_docs_importjob_terminal_intent
        CHECK (TerminalIntent IN ('', 'completed', 'failed', 'canceled')),
    CONSTRAINT chk_docs_importjob_state
        CHECK (State IN (
            'awaiting_source',
            'queued_preflight', 'preflighting',
            'awaiting_confirmation',
            'queued_import', 'importing',
            'terminalizing',
            'completed', 'completed_with_issues',
            'failed', 'canceled'
        ))
);

-- Work selection for the sole worker: an ordered scan over the states it may act on. No lease
-- column is involved because V1 has no distributed claiming.
CREATE INDEX IF NOT EXISTS idx_docs_importjob_work
    ON DOCS_ImportJob (State, CreateAt, Id)
    WHERE State IN (
        'queued_preflight', 'preflighting',
        'queued_import', 'importing', 'terminalizing'
    );

CREATE INDEX IF NOT EXISTS idx_docs_importjob_actor
    ON DOCS_ImportJob (ActorId, CreateAt DESC, Id DESC);

CREATE INDEX IF NOT EXISTS idx_docs_importjob_target
    ON DOCS_ImportJob (TargetSpaceId, CreateAt DESC, Id DESC);

CREATE INDEX IF NOT EXISTS idx_docs_importjob_invalidation
    ON DOCS_ImportJob (UpdateAt, Id)
    WHERE InvalidationPending = TRUE;

CREATE INDEX IF NOT EXISTS idx_docs_importjob_cleanup
    ON DOCS_ImportJob (RetainUntil)
    WHERE State IN (
        'awaiting_source', 'awaiting_confirmation',
        'completed', 'completed_with_issues', 'failed', 'canceled'
    );

-- DOCS_ImportChannelAttempt: every external channel-create attempt gets a durable identity before
-- the call, so an extra or unattached channel can be compensated independently of the one selected
-- ProvisionedChannelId.
CREATE TABLE IF NOT EXISTS DOCS_ImportChannelAttempt (
    JobId       VARCHAR(26) NOT NULL
        REFERENCES DOCS_ImportJob(Id) ON DELETE CASCADE,
    AttemptId   VARCHAR(26) NOT NULL,
    ChannelName VARCHAR(64) NOT NULL,
    ChannelId   VARCHAR(26) NOT NULL DEFAULT '',
    State       VARCHAR(32) NOT NULL,
    ErrorCode   VARCHAR(64) NOT NULL DEFAULT '',
    CreateAt    BIGINT NOT NULL,
    UpdateAt    BIGINT NOT NULL,

    PRIMARY KEY (JobId, AttemptId),
    CONSTRAINT chk_docs_importchannelattempt_state
        CHECK (State IN (
            'creating', 'provisioned', 'attached',
            'pending_compensation', 'compensated', 'failed'
        ))
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_docs_importchannelattempt_channel
    ON DOCS_ImportChannelAttempt (ChannelId)
    WHERE ChannelId <> '';

CREATE INDEX IF NOT EXISTS idx_docs_importchannelattempt_compensation
    ON DOCS_ImportChannelAttempt (State, UpdateAt, JobId)
    WHERE State = 'pending_compensation';

-- DOCS_ImportStagedPage: temporary normalized input retained until terminal staged-body cleanup.
CREATE TABLE IF NOT EXISTS DOCS_ImportStagedPage (
    JobId                       VARCHAR(26) NOT NULL
        REFERENCES DOCS_ImportJob(Id) ON DELETE CASCADE,
    -- Zero-based page ordinal (0..4999), used by execution checkpoints and deterministic issue ranges.
    Ordinal                     INTEGER NOT NULL,
    -- One-based JSONL line number, kept for diagnostics only.
    SourceLine                  INTEGER NOT NULL,
    ExternalId                  VARCHAR(512) NOT NULL,
    ParentExternalId            VARCHAR(512) NOT NULL DEFAULT '',
    SourceOrdinal               INTEGER NOT NULL,
    -- True when the manifest restriction list intersects this emitted page.
    Restricted                  BOOLEAN NOT NULL DEFAULT FALSE,

    Title                       TEXT NOT NULL,
    CanonicalBody               TEXT NOT NULL,
    SearchText                  TEXT NOT NULL,
    SourceUserProposal          TEXT NOT NULL DEFAULT '',
    SourceAuthorAccountId       VARCHAR(512) NOT NULL DEFAULT '',
    SourceCreateAt              BIGINT NOT NULL DEFAULT 0,
    SourceUpdateAt              BIGINT NOT NULL DEFAULT 0,
    SourceProps                 jsonb NOT NULL DEFAULT '{}'::jsonb,

    -- Content hashes deliberately exclude parent and ordinal; structural baselines are separate
    -- columns so a preserved local move never reads as a content conflict.
    IncomingSourceContentHash   VARCHAR(64) NOT NULL,
    PreflightCurrentContentHash VARCHAR(64) NOT NULL DEFAULT '',
    PreflightMappingContentHash VARCHAR(64) NOT NULL DEFAULT '',
    PreflightCurrentParentId    VARCHAR(26) NOT NULL DEFAULT '',
    PreflightMappingParentId    VARCHAR(26) NOT NULL DEFAULT '',
    PreflightMappingUpdateAt    BIGINT NOT NULL DEFAULT 0,
    PlannedAction               VARCHAR(32) NOT NULL DEFAULT '',
    PlannedPageId               VARCHAR(26) NOT NULL DEFAULT '',
    ResolvedUserId              VARCHAR(26) NOT NULL DEFAULT '',
    AuthorFallbackReason        VARCHAR(64) NOT NULL DEFAULT '',

    PRIMARY KEY (JobId, ExternalId),
    UNIQUE (JobId, Ordinal)
);

CREATE INDEX IF NOT EXISTS idx_docs_importstagedpage_order
    ON DOCS_ImportStagedPage (JobId, Ordinal);

-- DOCS_ImportManifestUser: manifest user mappings are worker input and must survive the upload
-- request and a process restart. These rows carry no bodies and are kept until job deletion so
-- reports and author-fallback explanations stay reconstructable.
CREATE TABLE IF NOT EXISTS DOCS_ImportManifestUser (
    JobId              VARCHAR(26) NOT NULL
        REFERENCES DOCS_ImportJob(Id) ON DELETE CASCADE,
    Ordinal            INTEGER NOT NULL,
    AccountId          VARCHAR(512) NOT NULL,
    ConfluenceUsername TEXT NOT NULL DEFAULT '',
    MattermostUsername TEXT NOT NULL DEFAULT '',

    PRIMARY KEY (JobId, Ordinal),
    UNIQUE (JobId, AccountId)
);

CREATE INDEX IF NOT EXISTS idx_docs_importmanifestuser_account
    ON DOCS_ImportManifestUser (JobId, AccountId);

-- DOCS_ImportEntity: the durable page mapping and idempotency boundary. The same external id may
-- exist in two ImportSources even when they target the same Docs Space.
CREATE TABLE IF NOT EXISTS DOCS_ImportEntity (
    ImportSourceId             VARCHAR(26) NOT NULL
        REFERENCES DOCS_ImportSource(Id) ON DELETE CASCADE,
    EntityType                 VARCHAR(32) NOT NULL,
    ExternalId                 VARCHAR(512) NOT NULL,
    LocalId                    VARCHAR(26) NOT NULL,

    LastSourceContentHash      VARCHAR(64) NOT NULL,
    LastAppliedContentHash     VARCHAR(64) NOT NULL,
    -- Structural baseline: the local parent the importer established. V1 never changes it for an
    -- existing page.
    LastAppliedParentId        VARCHAR(26) NOT NULL DEFAULT '',
    LastSourceParentExternalId VARCHAR(512) NOT NULL DEFAULT '',
    -- Supports durable same/cross-source title-placeholder analysis after local renames and staged
    -- body cleanup.
    LastSourceTitle            TEXT NOT NULL DEFAULT '',
    LastSourceOrdinal          INTEGER NOT NULL DEFAULT 0,
    FirstJobId                 VARCHAR(26) NOT NULL,
    LastSeenJobId              VARCHAR(26) NOT NULL,
    CreateAt                   BIGINT NOT NULL,
    UpdateAt                   BIGINT NOT NULL,

    PRIMARY KEY (ImportSourceId, EntityType, ExternalId),
    CONSTRAINT chk_docs_importentity_type CHECK (EntityType = 'page')
);

-- One local Page can map to at most one ImportSource.
CREATE UNIQUE INDEX IF NOT EXISTS uq_docs_importentity_local_page
    ON DOCS_ImportEntity (LocalId)
    WHERE EntityType = 'page';

-- DOCS_ImportIssue: individual structured issue rows (never thousands packed into one JSONB field).
CREATE TABLE IF NOT EXISTS DOCS_ImportIssue (
    JobId       VARCHAR(26) NOT NULL
        REFERENCES DOCS_ImportJob(Id) ON DELETE CASCADE,
    Stage       VARCHAR(16) NOT NULL,
    Ordinal     INTEGER NOT NULL,
    Severity    VARCHAR(16) NOT NULL,
    Code        VARCHAR(64) NOT NULL,
    EntityType  VARCHAR(32) NOT NULL DEFAULT '',
    ExternalId  VARCHAR(512) NOT NULL DEFAULT '',
    LocalId     VARCHAR(26) NOT NULL DEFAULT '',
    Title       TEXT NOT NULL DEFAULT '',
    Message     TEXT NOT NULL,
    Remediation TEXT NOT NULL DEFAULT '',
    Details     jsonb NOT NULL DEFAULT '{}'::jsonb,

    PRIMARY KEY (JobId, Stage, Ordinal),
    CONSTRAINT chk_docs_importissue_stage CHECK (Stage IN ('inspection', 'preflight', 'execution')),
    CONSTRAINT chk_docs_importissue_severity CHECK (Severity IN ('info', 'warning', 'error'))
);

CREATE INDEX IF NOT EXISTS idx_docs_importissue_page
    ON DOCS_ImportIssue (JobId, Stage, Severity, Ordinal);

-- DOCS_ImportResult: durable entity-level outcomes, kept separate from staging so reports stay
-- complete after staged bodies are purged. Never store bodies, SearchText, approval baselines, or
-- raw source props here.
CREATE TABLE IF NOT EXISTS DOCS_ImportResult (
    JobId         VARCHAR(26) NOT NULL
        REFERENCES DOCS_ImportJob(Id) ON DELETE CASCADE,
    Stage         VARCHAR(16) NOT NULL,
    Ordinal       INTEGER NOT NULL,
    EntityType    VARCHAR(32) NOT NULL DEFAULT 'page',
    ExternalId    VARCHAR(512) NOT NULL,
    LocalId       VARCHAR(26) NOT NULL DEFAULT '',
    Title         TEXT NOT NULL DEFAULT '',
    PlannedAction VARCHAR(32) NOT NULL DEFAULT '',
    ActualAction  VARCHAR(32) NOT NULL DEFAULT '',
    Outcome       VARCHAR(32) NOT NULL DEFAULT '',
    Details       jsonb NOT NULL DEFAULT '{}'::jsonb,
    CreateAt      BIGINT NOT NULL,
    UpdateAt      BIGINT NOT NULL,

    PRIMARY KEY (JobId, Stage, Ordinal),
    CONSTRAINT chk_docs_importresult_stage CHECK (Stage IN ('preflight', 'execution'))
);

CREATE INDEX IF NOT EXISTS idx_docs_importresult_page
    ON DOCS_ImportResult (JobId, Stage, Ordinal);

-- DOCS_ImportCapacity: one singleton accounting row so upload admission, preflight publication,
-- terminalization, and cleanup share one atomic boundary. Every reservation/release transaction
-- locks Id=1 FOR UPDATE. This is resource accounting, not worker ownership or HA claiming.
CREATE TABLE IF NOT EXISTS DOCS_ImportCapacity (
    Id                    SMALLINT PRIMARY KEY,
    ReservedStagedBytes   BIGINT NOT NULL DEFAULT 0,
    ReservedRetainedBytes BIGINT NOT NULL DEFAULT 0,
    UpdateAt              BIGINT NOT NULL,
    CONSTRAINT chk_docs_importcapacity_singleton CHECK (Id = 1)
);

INSERT INTO DOCS_ImportCapacity (Id, ReservedStagedBytes, ReservedRetainedBytes, UpdateAt)
VALUES (1, 0, 0, 0)
ON CONFLICT (Id) DO NOTHING;
