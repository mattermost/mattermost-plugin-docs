-- DOCS_ImportSource: one row identifies one Confluence Space as explicitly chosen by a user
-- within one target Docs Space. It — not organization id or space key — scopes page mappings.
CREATE TABLE IF NOT EXISTS DOCS_ImportSource (
    Id                  VARCHAR(26) PRIMARY KEY,
    SpaceId             VARCHAR(26) NOT NULL,
    SourceType          VARCHAR(32) NOT NULL DEFAULT 'confluence',
    DisplayName         VARCHAR(255) NOT NULL DEFAULT '',
    OrganizationId      TEXT NULL,
    ExternalSpaceKey    TEXT NOT NULL,
    ExternalSpaceName   TEXT NOT NULL DEFAULT '',
    CreatedBy           VARCHAR(26) NOT NULL,
    CreateAt            BIGINT NOT NULL,
    UpdateAt            BIGINT NOT NULL,
    LastImportAt        BIGINT NOT NULL DEFAULT 0,
    LastSuccessfulJobId VARCHAR(26) NOT NULL DEFAULT '',
    ActiveJobId         VARCHAR(26) NOT NULL DEFAULT '',
    Props               jsonb NOT NULL DEFAULT '{}'::jsonb,
    CONSTRAINT chk_docs_importsource_type CHECK (SourceType = 'confluence')
);

CREATE INDEX IF NOT EXISTS idx_docs_importsource_space
    ON DOCS_ImportSource (SpaceId, CreateAt, Id);

-- Do not add uniqueness on organization id, space key, or display name: two Confluence
-- instances may use identical values and must remain selectable as distinct sources.
CREATE INDEX IF NOT EXISTS idx_docs_importsource_candidate
    ON DOCS_ImportSource (SpaceId, SourceType, ExternalSpaceKey, OrganizationId, CreateAt);

CREATE INDEX IF NOT EXISTS idx_docs_importsource_active_job
    ON DOCS_ImportSource (ActiveJobId)
    WHERE ActiveJobId <> '';

-- DOCS_ImportJob: one restartable import lifecycle. TargetSpaceId is generated before insert
-- (even for a new target), making target-level serialization possible without a nullable key.
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

    State                     VARCHAR(32) NOT NULL,
    Phase                     VARCHAR(64) NOT NULL DEFAULT '',
    ProgressCurrent           BIGINT NOT NULL DEFAULT 0,
    ProgressTotal             BIGINT NOT NULL DEFAULT 0,

    BundleSha256              VARCHAR(64) NOT NULL,
    BundleSummary             jsonb NOT NULL DEFAULT '{}'::jsonb,
    PreflightSummary          jsonb NOT NULL DEFAULT '{}'::jsonb,
    PreflightRevision         VARCHAR(64) NOT NULL DEFAULT '',
    Confirmation              jsonb NOT NULL DEFAULT '{}'::jsonb,
    FinalSummary              jsonb NOT NULL DEFAULT '{}'::jsonb,

    ErrorCode                 VARCHAR(64) NOT NULL DEFAULT '',
    ErrorMessage              TEXT NOT NULL DEFAULT '',
    CancelRequestedAt         BIGINT NOT NULL DEFAULT 0,

    ClaimToken                VARCHAR(26) NOT NULL DEFAULT '',
    ClaimedBy                 VARCHAR(128) NOT NULL DEFAULT '',
    LeaseExpiresAt            BIGINT NOT NULL DEFAULT 0,
    HeartbeatAt               BIGINT NOT NULL DEFAULT 0,

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
    CONSTRAINT chk_docs_importjob_state
        CHECK (State IN (
            'awaiting_source',
            'waiting_source_turn',
            'queued_preflight', 'preflighting',
            'awaiting_confirmation',
            'queued_import', 'importing', 'canceling',
            'completed', 'completed_with_issues',
            'failed', 'canceled'
        ))
);

CREATE INDEX IF NOT EXISTS idx_docs_importjob_claim
    ON DOCS_ImportJob (State, LeaseExpiresAt, CreateAt, Id)
    WHERE State IN ('queued_preflight', 'preflighting', 'queued_import', 'importing', 'canceling');

CREATE INDEX IF NOT EXISTS idx_docs_importjob_actor
    ON DOCS_ImportJob (ActorId, CreateAt DESC, Id DESC);

CREATE INDEX IF NOT EXISTS idx_docs_importjob_target
    ON DOCS_ImportJob (TargetSpaceId, CreateAt DESC, Id DESC);

CREATE INDEX IF NOT EXISTS idx_docs_importjob_cleanup
    ON DOCS_ImportJob (RetainUntil)
    WHERE State IN ('completed', 'completed_with_issues', 'failed', 'canceled');

-- Only one queued/running execution per target Space at a time.
CREATE UNIQUE INDEX IF NOT EXISTS uq_docs_importjob_active_target
    ON DOCS_ImportJob (TargetSpaceId)
    WHERE State IN ('queued_import', 'importing', 'canceling');

-- DOCS_ImportStagedPage: temporary normalized input retained until job cleanup.
CREATE TABLE IF NOT EXISTS DOCS_ImportStagedPage (
    JobId                    VARCHAR(26) NOT NULL
        REFERENCES DOCS_ImportJob(Id) ON DELETE CASCADE,
    Ordinal                  INTEGER NOT NULL,
    ExternalId               TEXT NOT NULL,
    ParentExternalId         TEXT NOT NULL DEFAULT '',
    SourceOrdinal            INTEGER NOT NULL,

    Title                    TEXT NOT NULL,
    CanonicalBody            TEXT NOT NULL,
    SearchText               TEXT NOT NULL,
    SourceUserProposal       TEXT NOT NULL DEFAULT '',
    SourceAuthorAccountId    TEXT NOT NULL DEFAULT '',
    SourceCreateAt           BIGINT NOT NULL DEFAULT 0,
    SourceUpdateAt           BIGINT NOT NULL DEFAULT 0,
    SourceProps              jsonb NOT NULL DEFAULT '{}'::jsonb,

    IncomingSourceHash       VARCHAR(64) NOT NULL,
    PreflightCurrentHash     VARCHAR(64) NOT NULL DEFAULT '',
    PreflightMappingHash     VARCHAR(64) NOT NULL DEFAULT '',
    PreflightMappingUpdateAt BIGINT NOT NULL DEFAULT 0,
    PlannedAction            VARCHAR(32) NOT NULL DEFAULT '',
    PlannedPageId            VARCHAR(26) NOT NULL DEFAULT '',
    ResolvedUserId           VARCHAR(26) NOT NULL DEFAULT '',
    AuthorFallbackReason     VARCHAR(64) NOT NULL DEFAULT '',

    PRIMARY KEY (JobId, ExternalId),
    UNIQUE (JobId, Ordinal)
);

CREATE INDEX IF NOT EXISTS idx_docs_importstagedpage_order
    ON DOCS_ImportStagedPage (JobId, Ordinal);

-- DOCS_ImportEntity: the durable idempotency boundary. The same external id may exist in two
-- ImportSources even when they target the same Docs Space.
CREATE TABLE IF NOT EXISTS DOCS_ImportEntity (
    ImportSourceId             VARCHAR(26) NOT NULL
        REFERENCES DOCS_ImportSource(Id) ON DELETE CASCADE,
    EntityType                 VARCHAR(32) NOT NULL,
    ExternalId                 TEXT NOT NULL,
    LocalId                    VARCHAR(26) NOT NULL,

    LastSourceHash             VARCHAR(64) NOT NULL,
    LastAppliedHash            VARCHAR(64) NOT NULL,
    LastSourceParentExternalId TEXT NOT NULL DEFAULT '',
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
    ExternalId  TEXT NOT NULL DEFAULT '',
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

-- DOCS_ImportResult: durable entity-level outcomes, kept separate from staging so the report can
-- enumerate creates/updates/no-ops after staged bodies are purged. Never store bodies here.
CREATE TABLE IF NOT EXISTS DOCS_ImportResult (
    JobId         VARCHAR(26) NOT NULL
        REFERENCES DOCS_ImportJob(Id) ON DELETE CASCADE,
    Stage         VARCHAR(16) NOT NULL,
    Ordinal       INTEGER NOT NULL,
    EntityType    VARCHAR(32) NOT NULL DEFAULT 'page',
    ExternalId    TEXT NOT NULL,
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
