// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package model

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	mmmodel "github.com/mattermost/mattermost/server/public/model"
)

// ImportJobState is a persisted import-job lifecycle state. The values here must exactly match the
// chk_docs_importjob_state CHECK constraint in migration 000006.
//
// V1 runs a single worker goroutine on a single application node, so there are deliberately no
// claim/lease states: restart safety comes from PostgreSQL state plus immutable per-page execution
// checkpoints. Terminalization is its own state because success, failure, and cancellation all have
// to durably record an outcome for every staged page before the job may become terminal.
type ImportJobState string

const (
	ImportStateAwaitingSource       ImportJobState = "awaiting_source"
	ImportStateQueuedPreflight      ImportJobState = "queued_preflight"
	ImportStatePreflighting         ImportJobState = "preflighting"
	ImportStateAwaitingConfirmation ImportJobState = "awaiting_confirmation"
	ImportStateQueuedImport         ImportJobState = "queued_import"
	ImportStateImporting            ImportJobState = "importing"
	ImportStateTerminalizing        ImportJobState = "terminalizing"
	ImportStateCompleted            ImportJobState = "completed"
	ImportStateCompletedWithIssues  ImportJobState = "completed_with_issues"
	ImportStateFailed               ImportJobState = "failed"
	ImportStateCanceled             ImportJobState = "canceled"
)

// validImportStates is the set matching the DB CHECK; used for model-level validation.
var validImportStates = map[ImportJobState]struct{}{
	ImportStateAwaitingSource: {}, ImportStateQueuedPreflight: {}, ImportStatePreflighting: {},
	ImportStateAwaitingConfirmation: {}, ImportStateQueuedImport: {}, ImportStateImporting: {},
	ImportStateTerminalizing: {},
	ImportStateCompleted:     {}, ImportStateCompletedWithIssues: {},
	ImportStateFailed: {}, ImportStateCanceled: {},
}

// IsValid reports whether s is a known import state.
func (s ImportJobState) IsValid() bool {
	_, ok := validImportStates[s]
	return ok
}

// IsTerminal reports whether s is a terminal (finished) state. Cleanup retention and mapping-revision
// bookkeeping key off this predicate. Note that terminalizing is *not* terminal: it is the worker
// phase that produces the durable outcome the terminal state then publishes.
func (s ImportJobState) IsTerminal() bool {
	switch s {
	case ImportStateCompleted, ImportStateCompletedWithIssues, ImportStateFailed, ImportStateCanceled:
		return true
	default:
		return false
	}
}

// IsWorkerOwned reports whether the sole worker acts on a job in state s. Jobs awaiting source
// selection or human confirmation are deliberately not worker-owned and never block other jobs.
func (s ImportJobState) IsWorkerOwned() bool {
	switch s {
	case ImportStateQueuedPreflight, ImportStatePreflighting,
		ImportStateQueuedImport, ImportStateImporting, ImportStateTerminalizing:
		return true
	default:
		return false
	}
}

// AwaitsUser reports whether s is waiting on a human, which the seven-day review retention applies to.
func (s ImportJobState) AwaitsUser() bool {
	return s == ImportStateAwaitingSource || s == ImportStateAwaitingConfirmation
}

// ImportTerminalIntent records which terminal outcome the terminalizer must produce. It exists so
// terminalization is restartable: a crash mid-terminalization resumes with the same intent rather
// than re-deciding. Matches chk_docs_importjob_terminal_intent.
type ImportTerminalIntent string

const (
	ImportIntentNone      ImportTerminalIntent = ""
	ImportIntentCompleted ImportTerminalIntent = "completed"
	ImportIntentFailed    ImportTerminalIntent = "failed"
	ImportIntentCanceled  ImportTerminalIntent = "canceled"
)

// IsValid reports whether i is a known terminal intent (including unset).
func (i ImportTerminalIntent) IsValid() bool {
	switch i {
	case ImportIntentNone, ImportIntentCompleted, ImportIntentFailed, ImportIntentCanceled:
		return true
	default:
		return false
	}
}

// ImportJobPhase is a free-form, human-facing progress phase label (no DB CHECK).
type ImportJobPhase string

const (
	ImportPhaseInspecting           ImportJobPhase = "inspecting"
	ImportPhaseResolvingUsers       ImportJobPhase = "resolving_users"
	ImportPhaseComputingActions     ImportJobPhase = "computing_actions"
	ImportPhaseAwaitingConfirmation ImportJobPhase = "awaiting_confirmation"
	ImportPhaseQueuedImport         ImportJobPhase = "queued_import"
	ImportPhaseProvisioning         ImportJobPhase = "provisioning_space"
	ImportPhaseWritingPages         ImportJobPhase = "writing_pages"
	ImportPhaseFinalizing           ImportJobPhase = "finalizing"
)

// ImportTargetKind selects a new or existing Docs Space. Matches chk_docs_importjob_target.
type ImportTargetKind string

const (
	ImportTargetNew      ImportTargetKind = "new"
	ImportTargetExisting ImportTargetKind = "existing"
)

// IsValid reports whether k is a known target kind.
func (k ImportTargetKind) IsValid() bool {
	return k == ImportTargetNew || k == ImportTargetExisting
}

// ImportSourceSelectionMode selects a new or existing ImportSource. Matches
// chk_docs_importjob_source_mode (which also permits the empty string before selection).
type ImportSourceSelectionMode string

const (
	ImportSourceModeUnset    ImportSourceSelectionMode = ""
	ImportSourceModeNew      ImportSourceSelectionMode = "new"
	ImportSourceModeExisting ImportSourceSelectionMode = "existing"
)

// IsValid reports whether m is a known source-selection mode (including unset).
func (m ImportSourceSelectionMode) IsValid() bool {
	switch m {
	case ImportSourceModeUnset, ImportSourceModeNew, ImportSourceModeExisting:
		return true
	default:
		return false
	}
}

// ImportAction is the planned/actual per-page decision recorded in staging and results.
type ImportAction string

const (
	ImportActionCreate        ImportAction = "create"
	ImportActionUpdate        ImportAction = "update"
	ImportActionNoop          ImportAction = "noop"
	ImportActionPreserveLocal ImportAction = "preserve_local"
	ImportActionConflict      ImportAction = "conflict"
	ImportActionBlocked       ImportAction = "blocked"
	ImportActionStale         ImportAction = "stale"
	// ImportActionNotAttempted is recorded by terminalization for every staged page that never got an
	// execution checkpoint because the job failed or was canceled first.
	ImportActionNotAttempted ImportAction = "not_attempted"
)

// IsValid reports whether a is a known action (an empty action means "not yet classified").
func (a ImportAction) IsValid() bool {
	switch a {
	case "", ImportActionCreate, ImportActionUpdate, ImportActionNoop, ImportActionPreserveLocal,
		ImportActionConflict, ImportActionBlocked, ImportActionStale, ImportActionNotAttempted:
		return true
	default:
		return false
	}
}

// ImportOutcome is the durable per-entity outcome recorded in DOCS_ImportResult.
type ImportOutcome string

const (
	ImportOutcomeCreated             ImportOutcome = "created"
	ImportOutcomeUpdated             ImportOutcome = "updated"
	ImportOutcomeUnchanged           ImportOutcome = "unchanged"
	ImportOutcomeLocalPreserved      ImportOutcome = "local_preserved"
	ImportOutcomeConflictSkipped     ImportOutcome = "conflict_skipped"
	ImportOutcomeBlocked             ImportOutcome = "blocked"
	ImportOutcomeStale               ImportOutcome = "stale"
	ImportOutcomeNotAttemptedCancel  ImportOutcome = "not_attempted_canceled"
	ImportOutcomeNotAttemptedFailure ImportOutcome = "not_attempted_failed"
)

// IsValid reports whether o is a known outcome (empty means "not yet decided").
func (o ImportOutcome) IsValid() bool {
	switch o {
	case "", ImportOutcomeCreated, ImportOutcomeUpdated, ImportOutcomeUnchanged,
		ImportOutcomeLocalPreserved, ImportOutcomeConflictSkipped, ImportOutcomeBlocked,
		ImportOutcomeStale, ImportOutcomeNotAttemptedCancel, ImportOutcomeNotAttemptedFailure:
		return true
	default:
		return false
	}
}

// ImportIssueStage and ImportIssueSeverity mirror the DOCS_ImportIssue CHECK constraints.
type (
	ImportIssueStage    string
	ImportIssueSeverity string
)

const (
	ImportStageInspection ImportIssueStage = "inspection"
	ImportStagePreflight  ImportIssueStage = "preflight"
	ImportStageExecution  ImportIssueStage = "execution"

	ImportSeverityInfo    ImportIssueSeverity = "info"
	ImportSeverityWarning ImportIssueSeverity = "warning"
	ImportSeverityError   ImportIssueSeverity = "error"
)

// IsValid reports whether the stage is one of the three persisted stages.
func (s ImportIssueStage) IsValid() bool {
	switch s {
	case ImportStageInspection, ImportStagePreflight, ImportStageExecution:
		return true
	default:
		return false
	}
}

// IsResultStage reports whether the stage may appear in DOCS_ImportResult, which — unlike issues —
// has no inspection stage.
func (s ImportIssueStage) IsResultStage() bool {
	return s == ImportStagePreflight || s == ImportStageExecution
}

// IsValid reports whether the severity is one of the three persisted severities.
func (s ImportIssueSeverity) IsValid() bool {
	switch s {
	case ImportSeverityInfo, ImportSeverityWarning, ImportSeverityError:
		return true
	default:
		return false
	}
}

// ImportChannelAttemptState mirrors chk_docs_importchannelattempt_state. Each external
// channel-create call gets a durable attempt row before the call, so a returned-but-unattached
// channel can be compensated independently of the one channel that ends up backing the Space.
type ImportChannelAttemptState string

const (
	ImportChannelCreating            ImportChannelAttemptState = "creating"
	ImportChannelProvisioned         ImportChannelAttemptState = "provisioned"
	ImportChannelAttached            ImportChannelAttemptState = "attached"
	ImportChannelPendingCompensation ImportChannelAttemptState = "pending_compensation"
	ImportChannelCompensated         ImportChannelAttemptState = "compensated"
	ImportChannelFailed              ImportChannelAttemptState = "failed"
)

// IsValid reports whether s is a known channel-attempt state.
func (s ImportChannelAttemptState) IsValid() bool {
	switch s {
	case ImportChannelCreating, ImportChannelProvisioned, ImportChannelAttached,
		ImportChannelPendingCompensation, ImportChannelCompensated, ImportChannelFailed:
		return true
	default:
		return false
	}
}

// Entity type and source type constants matching their DB CHECK constraints.
const (
	ImportEntityTypePage       = "page"
	ImportSourceTypeConfluence = "confluence"
)

// Field size limits enforced at the model boundary (mirroring the migration's column types).
const (
	ImportDisplayNameMaxRunes = 255
	ImportSpaceTitleMaxRunes  = 128
	ImportErrorCodeMaxRunes   = 64
	ImportIssueCodeMaxRunes   = 64

	// ImportExternalIDMaxBytes bounds every externally supplied identifier that participates in a
	// B-tree index (Confluence page IDs, Atlassian account IDs).
	ImportExternalIDMaxBytes = 512
	// ImportSpaceKeyMaxBytes bounds source organization IDs and Space keys.
	ImportSpaceKeyMaxBytes = 255

	// ImportMaxPages is the per-bundle page ceiling. It also fixes the ordinal ranges: page ordinals
	// occupy 0..ImportMaxPages-1 and stale result ordinals start at ImportMaxPages.
	ImportMaxPages = 5000
	// ImportMaxMappingsPerSource caps retained mappings per ImportSource so stale anti-joins,
	// terminal stale rows, and restart work stay bounded.
	ImportMaxMappingsPerSource = 5000

	// ImportDetailsMaxBytes bounds the serialized Details JSON on a result or issue row, so a single
	// finding cannot grow a report without limit.
	ImportDetailsMaxBytes = 4 * 1024
	// ImportIssueTextMaxBytes bounds a generated message or remediation string.
	ImportIssueTextMaxBytes = 2 * 1024

	// importRetainedRowOverheadBytes is the fixed cost of one retained report row: its share of the
	// page header, the job id, the ordinal and timestamps, and the short enum columns (stage, severity,
	// entity type, planned/actual action, outcome, issue code, local id).
	importRetainedRowOverheadBytes = 512

	// ImportRetainedIssueRowMaxBytes is the largest DOCS_ImportIssue row ImportIssueRecord.IsValid
	// admits: bounded Details JSON, a message, a remediation, a title, and an external id. Admission
	// reservations must be built from this rather than from a guessed average — a single permitted
	// issue is an order of magnitude larger than any plausible flat figure, so a reservation that
	// under-counts it promises room it cannot deliver.
	ImportRetainedIssueRowMaxBytes = importRetainedRowOverheadBytes + ImportDetailsMaxBytes +
		2*ImportIssueTextMaxBytes + 4*PageTitleMaxRunes + ImportExternalIDMaxBytes

	// ImportRetainedResultRowMaxBytes is the largest DOCS_ImportResult row ImportResultRecord.IsValid
	// admits. A result row carries no message or remediation, so it is materially smaller than an issue.
	ImportRetainedResultRowMaxBytes = importRetainedRowOverheadBytes + ImportDetailsMaxBytes +
		4*PageTitleMaxRunes + ImportExternalIDMaxBytes

	// ImportRetainedIssueBudgetBytes is the per-job allowance for *discretionary* retained rows: the
	// inspection, preflight, and execution issues that explain a job's outcomes. It is a budget, not an
	// estimate — see ImportJob.IssueBudgetRemaining.
	//
	// It is flat rather than per-entity on purpose. Reserving the hard per-page cap
	// (ImportMaxIssueCodesPerPage codes in each of two stages) at worst-case row size would need
	// hundreds of megabytes for one large bundle, which no admission budget could grant; a flat
	// allowance large enough for tens of thousands of realistic issues, with visible truncation past it,
	// is both honest and admissible.
	ImportRetainedIssueBudgetBytes = 16 * 1024 * 1024
)

// validateImportDetails enforces the serialized-size bound on a result/issue Details map.
func validateImportDetails(where, field string, details mmmodel.StringInterface) *mmmodel.AppError {
	if len(details) == 0 {
		return nil
	}
	raw, err := json.Marshal(details)
	if err != nil {
		return mmmodel.NewAppError(where, "model.import_details.invalid.app_error", map[string]any{"Field": field}, "", http.StatusBadRequest)
	}
	if len(raw) > ImportDetailsMaxBytes {
		return mmmodel.NewAppError(where, "model.import_details.too_large.app_error",
			map[string]any{"Field": field, "MaxBytes": ImportDetailsMaxBytes}, "", http.StatusBadRequest)
	}
	return nil
}

// importIdentifierPattern is the ASCII contract every non-empty external identifier must match.
// Confluence page IDs, Atlassian account IDs, organization IDs, and Space keys emitted by the
// authoritative producer all fit this set, so bounding it keeps index sizing deterministic and
// rejects incompatible future producer identifiers until the contract is deliberately revised.
// '~' is included because Confluence keys personal spaces as "~username"/"~accountid"; omitting it
// would reject every personal-space bundle outright.
var importIdentifierPattern = regexp.MustCompile(`^[A-Za-z0-9._:@~-]+$`)

// IsValidImportIdentifier reports whether id is a non-empty, contract-conforming identifier no
// longer than maxBytes. Callers that permit absence check for "" separately.
func IsValidImportIdentifier(id string, maxBytes int) bool {
	if id == "" || len(id) > maxBytes {
		return false
	}
	return importIdentifierPattern.MatchString(id)
}

// hexSHA256 matches exactly 64 lowercase hexadecimal characters. Every non-empty SHA-256 column is
// validated against this at the model/application boundary so a malformed or CHAR-padded value
// never enters a comparison.
var hexSHA256 = regexp.MustCompile(`^[0-9a-f]{64}$`)

// IsValidImportHash reports whether s is empty or exactly 64 lowercase hex characters. Use it for
// the optional preflight baselines; required hashes must additionally be non-empty.
func IsValidImportHash(s string) bool {
	return s == "" || hexSHA256.MatchString(s)
}

// IsStorableText reports whether s is safe to persist: valid UTF-8 with no NUL (U+0000). PostgreSQL
// cannot store a NUL in a TEXT/VARCHAR value and rejects the escaped-NUL code point inside a JSONB
// string, and invalid UTF-8 would be silently replaced or mutated. Both are rejected rather than
// sanitized away, so what the user supplied is either stored exactly or refused with a clear reason —
// silently altered content is worse than a rejected request.
func IsStorableText(s string) bool {
	return utf8.ValidString(s) && !strings.ContainsRune(s, 0)
}

// --- typed JSONB columns ---

// Named byte limits for the typed JSONB columns. They are enforced at the model boundary rather than
// relying on any driver-side cap: mmmodel.StringInterface is deliberately not used as the database
// value type for these columns because its internal valuer rejects anything over 1 MiB, which is
// below the worst-case approved-conflict list.
const (
	// ImportSummaryMaxBytes bounds the bundle/preflight/final summaries. They are fixed-shape count
	// structures, so this is generous headroom rather than a working constraint.
	ImportSummaryMaxBytes = 256 * 1024
	// ImportConfirmationMaxBytes bounds the persisted confirmation. The worst case is one approved
	// external ID per page: ImportMaxPages identifiers bounded to ImportExternalIDMaxBytes each,
	// plus JSON overhead, which fits well inside 8 MiB. The confirm handler caps its request body to
	// the same value so an over-limit confirmation is rejected at the edge, not at insert.
	ImportConfirmationMaxBytes = 8 * 1024 * 1024
)

// marshalJSONColumn marshals v for a jsonb column, enforcing maxBytes. An empty/zero value still
// marshals to a JSON object, preserving each column's NOT NULL DEFAULT '{}' invariant. The bytes are
// returned as a string, which lib/pq sends to jsonb.
func marshalJSONColumn(v any, maxBytes int, what string) (driver.Value, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal %s: %w", what, err)
	}
	if len(raw) > maxBytes {
		return nil, fmt.Errorf("%s of %d bytes exceeds the %d byte limit", what, len(raw), maxBytes)
	}
	return string(raw), nil
}

// scanJSONColumn unmarshals a jsonb column into dst. A NULL or empty column leaves dst at its zero
// value rather than erroring, so a row written before a field existed still scans.
func scanJSONColumn(src any, dst any, what string) error {
	var raw []byte
	switch v := src.(type) {
	case nil:
		return nil
	case []byte:
		raw = v
	case string:
		raw = []byte(v)
	default:
		return fmt.Errorf("unsupported Scan type %T for %s", src, what)
	}
	if len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return fmt.Errorf("failed to unmarshal %s: %w", what, err)
	}
	return nil
}

// --- persisted rows ---

// ImportSource identifies one Confluence Space chosen by a user within one target Docs Space. It —
// not organization id or space key — scopes page mappings (DOCS_ImportSource).
type ImportSource struct {
	Id                  string `json:"id"`
	SpaceId             string `json:"space_id"`
	SourceType          string `json:"source_type"`
	DisplayName         string `json:"display_name"`
	OrganizationId      string `json:"organization_id,omitempty"`
	ExternalSpaceKey    string `json:"external_space_key"`
	ExternalSpaceName   string `json:"external_space_name"`
	CreatedBy           string `json:"created_by"`
	CreateAt            int64  `json:"create_at"`
	UpdateAt            int64  `json:"update_at"`
	LastImportAt        int64  `json:"last_import_at"`
	LastSuccessfulJobId string `json:"last_successful_job_id,omitempty"`
	// MappingRevision is the optimistic-invalidation token that replaced full-lifecycle source
	// ownership. Preflight captures it, confirmation and execution start require an exact match, and
	// terminalization increments it once per job that committed a mapping-affecting decision.
	MappingRevision int64                   `json:"mapping_revision"`
	Props           mmmodel.StringInterface `json:"props"`
}

// ImportJob is one restartable import lifecycle (DOCS_ImportJob).
type ImportJob struct {
	Id      string `json:"id"`
	ActorId string `json:"actor_id"`
	TeamId  string `json:"team_id"`

	TargetKind                ImportTargetKind `json:"target_kind"`
	TargetSpaceId             string           `json:"target_space_id"`
	TargetSpaceExisted        bool             `json:"target_space_existed"`
	ConfirmedSpaceTitle       string           `json:"confirmed_space_title,omitempty"`
	ConfirmedSpaceDescription string           `json:"confirmed_space_description,omitempty"`
	ProvisionedChannelId      string           `json:"-"`

	SourceSelectionMode       ImportSourceSelectionMode `json:"source_selection_mode"`
	SelectedImportSourceId    string                    `json:"selected_import_source_id,omitempty"`
	SelectedSourceDisplayName string                    `json:"selected_source_display_name,omitempty"`
	PreflightMappingRevision  int64                     `json:"-"`

	State                ImportJobState       `json:"state"`
	Phase                ImportJobPhase       `json:"phase,omitempty"`
	TerminalIntent       ImportTerminalIntent `json:"-"`
	MappingInputsChanged bool                 `json:"-"`
	InvalidationPending  bool                 `json:"-"`
	ProgressCurrent      int64                `json:"progress_current"`
	ProgressTotal        int64                `json:"progress_total"`

	StagedBytes   int64 `json:"-"`
	RetainedBytes int64 `json:"-"`
	// RetainedIssueBytes is the discretionary share of RetainedBytes: how much of
	// ImportRetainedIssueBudgetBytes the job's issue rows have already spent. It is tracked separately
	// so issue writers cannot borrow the capacity reserved for mandatory outcomes.
	RetainedIssueBytes int64 `json:"-"`
	// PreflightRetainedBytes and PreflightRetainedIssueBytes are what the job's current preflight-stage
	// rows contribute to the two totals above. Preflight is republished wholesale on recomputation, so the
	// previous plan's charge must be known in order to be replaced rather than double-counted.
	PreflightRetainedBytes      int64 `json:"-"`
	PreflightRetainedIssueBytes int64 `json:"-"`
	RetainedReservedBytes       int64 `json:"-"`

	BundleSha256      string                 `json:"-"`
	BundleSummary     ImportBundleSummary    `json:"bundle_summary"`
	PreflightSummary  ImportPreflightSummary `json:"preflight_summary"`
	PreflightRevision string                 `json:"preflight_revision,omitempty"`
	Confirmation      ImportConfirmation     `json:"-"`
	FinalSummary      ImportFinalSummary     `json:"final_summary"`

	ErrorCode         string `json:"error_code,omitempty"`
	ErrorMessage      string `json:"-"`
	CancelRequestedAt int64  `json:"cancel_requested_at"`

	CreateAt    int64 `json:"create_at"`
	UpdateAt    int64 `json:"update_at"`
	ConfirmedAt int64 `json:"confirmed_at"`
	StartedAt   int64 `json:"started_at"`
	FinishedAt  int64 `json:"finished_at"`
	RetainUntil int64 `json:"-"`
}

// IssueBudgetRemaining reports how many bytes of issue rows this job may still write.
//
// Retained rows fall into two classes and only the first is unconditional. *Mandatory* rows — one
// result per staged page and per stale mapping, plus the final summary — are reserved worst-case at
// admission, so they are always written and simply charged. *Discretionary* rows — the issues that
// explain those outcomes — share ImportRetainedIssueBudgetBytes, and a writer that would exceed what
// remains must stop emitting per-entity issues and record one aggregate truncation issue instead.
//
// The two pools are deliberately measured against different totals. A single "unspent reservation"
// figure would let issue writers consume the capacity held for mandatory outcomes and leave a job
// unable to record what happened to its pages — exactly the failure the reservation exists to prevent.
func (j *ImportJob) IssueBudgetRemaining() int64 {
	return max(ImportRetainedIssueBudgetBytes-j.RetainedIssueBytes, 0)
}

// ImportChannelAttempt is one durable external channel-create attempt (DOCS_ImportChannelAttempt).
type ImportChannelAttempt struct {
	JobId       string                    `json:"job_id"`
	AttemptId   string                    `json:"attempt_id"`
	ChannelName string                    `json:"-"`
	ChannelId   string                    `json:"-"`
	State       ImportChannelAttemptState `json:"state"`
	ErrorCode   string                    `json:"error_code,omitempty"`
	CreateAt    int64                     `json:"create_at"`
	UpdateAt    int64                     `json:"update_at"`
}

// ImportCapacity is the singleton admission-accounting row (DOCS_ImportCapacity). It bounds
// aggregate resource use; it is not worker ownership and carries no HA semantics.
type ImportCapacity struct {
	Id                    int16 `json:"id"`
	ReservedStagedBytes   int64 `json:"reserved_staged_bytes"`
	ReservedRetainedBytes int64 `json:"reserved_retained_bytes"`
	UpdateAt              int64 `json:"update_at"`
}

// ImportStagedPage is one normalized staged page (DOCS_ImportStagedPage), retained until terminal
// staged-body cleanup.
type ImportStagedPage struct {
	JobId string `json:"job_id"`
	// Ordinal is the zero-based page ordinal used by execution checkpoints and issue ranges.
	Ordinal int `json:"ordinal"`
	// SourceLine is the one-based JSONL line number, for diagnostics only.
	SourceLine       int    `json:"source_line"`
	ExternalId       string `json:"external_id"`
	ParentExternalId string `json:"parent_external_id"`
	SourceOrdinal    int    `json:"source_ordinal"`
	Restricted       bool   `json:"restricted"`

	Title                 string                  `json:"title"`
	CanonicalBody         string                  `json:"-"`
	SearchText            string                  `json:"-"`
	SourceUserProposal    string                  `json:"source_user_proposal"`
	SourceAuthorAccountId string                  `json:"source_author_account_id"`
	SourceCreateAt        int64                   `json:"source_create_at"`
	SourceUpdateAt        int64                   `json:"source_update_at"`
	SourceProps           mmmodel.StringInterface `json:"-"`

	// Content hashes deliberately exclude parent and ordinal; the structural baselines below are
	// compared separately so a preserved local move never reads as a content conflict.
	IncomingSourceContentHash   string       `json:"-"`
	PreflightCurrentContentHash string       `json:"-"`
	PreflightMappingContentHash string       `json:"-"`
	PreflightCurrentParentId    string       `json:"-"`
	PreflightMappingParentId    string       `json:"-"`
	PreflightMappingUpdateAt    int64        `json:"-"`
	PlannedAction               ImportAction `json:"planned_action,omitempty"`
	PlannedPageId               string       `json:"-"`
	ResolvedUserId              string       `json:"-"`
	AuthorFallbackReason        string       `json:"author_fallback_reason,omitempty"`
}

// ImportManifestUser is one durable manifest user mapping (DOCS_ImportManifestUser). Author
// resolution reads these rows rather than the upload request's in-memory manifest, so it survives a
// process restart.
type ImportManifestUser struct {
	JobId              string `json:"job_id"`
	Ordinal            int    `json:"ordinal"`
	AccountId          string `json:"account_id"`
	ConfluenceUsername string `json:"confluence_username,omitempty"`
	MattermostUsername string `json:"mattermost_username,omitempty"`
}

// ImportEntity is the durable page mapping (DOCS_ImportEntity), the idempotency boundary.
type ImportEntity struct {
	ImportSourceId string `json:"import_source_id"`
	EntityType     string `json:"entity_type"`
	ExternalId     string `json:"external_id"`
	LocalId        string `json:"local_id"`

	LastSourceContentHash  string `json:"-"`
	LastAppliedContentHash string `json:"-"`
	// LastAppliedParentId is the structural baseline: the local parent the importer established.
	// V1 never changes it for an existing page.
	LastAppliedParentId        string `json:"-"`
	LastSourceParentExternalId string `json:"last_source_parent_external_id"`
	// LastSourceTitle keeps source identity available for title-placeholder analysis after a local
	// rename or staged-body cleanup.
	LastSourceTitle   string `json:"last_source_title,omitempty"`
	LastSourceOrdinal int    `json:"last_source_ordinal"`
	FirstJobId        string `json:"first_job_id"`
	LastSeenJobId     string `json:"last_seen_job_id"`
	CreateAt          int64  `json:"create_at"`
	UpdateAt          int64  `json:"update_at"`
}

// ImportResultRecord is one durable entity-level outcome row (DOCS_ImportResult).
type ImportResultRecord struct {
	JobId         string                  `json:"job_id"`
	Stage         ImportIssueStage        `json:"stage"`
	Ordinal       int                     `json:"ordinal"`
	EntityType    string                  `json:"entity_type"`
	ExternalId    string                  `json:"external_id"`
	LocalId       string                  `json:"local_id,omitempty"`
	Title         string                  `json:"title,omitempty"`
	PlannedAction ImportAction            `json:"planned_action,omitempty"`
	ActualAction  ImportAction            `json:"actual_action,omitempty"`
	Outcome       ImportOutcome           `json:"outcome"`
	Details       mmmodel.StringInterface `json:"details,omitempty"`
	CreateAt      int64                   `json:"create_at"`
	UpdateAt      int64                   `json:"update_at"`
}

// ImportIssueRecord is one durable issue row (DOCS_ImportIssue).
type ImportIssueRecord struct {
	JobId       string                  `json:"job_id"`
	Stage       ImportIssueStage        `json:"stage"`
	Ordinal     int                     `json:"ordinal"`
	Severity    ImportIssueSeverity     `json:"severity"`
	Code        string                  `json:"code"`
	EntityType  string                  `json:"entity_type,omitempty"`
	ExternalId  string                  `json:"external_id,omitempty"`
	LocalId     string                  `json:"local_id,omitempty"`
	Title       string                  `json:"title,omitempty"`
	Message     string                  `json:"message"`
	Remediation string                  `json:"remediation,omitempty"`
	Details     mmmodel.StringInterface `json:"details,omitempty"`
}

// --- typed summary/confirmation JSONB payloads ---

// ImportPreflightSummary is the persisted preflight impact summary.
type ImportPreflightSummary struct {
	Manifest ImportBundleCounts `json:"manifest"`
	Actions  ImportActionCounts `json:"actions"`
	Authors  ImportAuthorCounts `json:"authors"`
	Links    ImportLinkCounts   `json:"links"`
}

// Value implements driver.Valuer for the PreflightSummary jsonb column.
func (s ImportPreflightSummary) Value() (driver.Value, error) {
	return marshalJSONColumn(s, ImportSummaryMaxBytes, "import preflight summary")
}

// Scan implements sql.Scanner for the PreflightSummary jsonb column.
func (s *ImportPreflightSummary) Scan(src any) error {
	return scanJSONColumn(src, s, "import preflight summary")
}

// ImportFinalSummary is the persisted final outcome summary. Counts here are actual results, never
// historical preflight classifications.
type ImportFinalSummary struct {
	Manifest ImportBundleCounts `json:"manifest"`
	Actions  ImportActionCounts `json:"actions"`
	Authors  ImportAuthorCounts `json:"authors"`
	Links    ImportLinkCounts   `json:"links"`
	// Outcomes counts durable per-entity outcomes by ImportOutcome value.
	Outcomes map[string]int `json:"outcomes,omitempty"`
}

// Value implements driver.Valuer for the FinalSummary jsonb column.
func (s ImportFinalSummary) Value() (driver.Value, error) {
	return marshalJSONColumn(s, ImportSummaryMaxBytes, "import final summary")
}

// Scan implements sql.Scanner for the FinalSummary jsonb column.
func (s *ImportFinalSummary) Scan(src any) error {
	return scanJSONColumn(src, s, "import final summary")
}

// ImportActionCounts counts planned or actual actions.
type ImportActionCounts struct {
	Create        int `json:"create"`
	Update        int `json:"update"`
	Noop          int `json:"noop"`
	PreserveLocal int `json:"preserve_local"`
	Conflict      int `json:"conflict"`
	Stale         int `json:"stale"`
	Blocked       int `json:"blocked"`
	NotAttempted  int `json:"not_attempted,omitempty"`
}

// ImportAuthorCounts counts author resolution outcomes.
type ImportAuthorCounts struct {
	Mapped          int `json:"mapped"`
	FallbackToActor int `json:"fallback_to_actor"`
}

// ImportLinkCounts counts discovered placeholder links by resolution category.
//
// Every field here is a number the importer actually measures during inspection. The plan also describes
// cross_source_unique and ambiguous counts, which require resolving a placeholder against the durable mappings
// of *other* ImportSources in the same Space (§14/§16); until that resolution exists, those categories are
// absent from the report rather than present and always zero. A count of zero has to mean "none found", or a
// reader cannot use any of them.
type ImportLinkCounts struct {
	SameSource       int `json:"same_source"`
	Unresolved       int `json:"unresolved"`
	FilePlaceholders int `json:"file_placeholders"`
}

// ImportNewSpaceMetadata is the confirmed, user-editable metadata for a new Space.
type ImportNewSpaceMetadata struct {
	Title       string `json:"title"`
	Description string `json:"description"`
}

// ImportAcknowledgements records the explicit acknowledgements the user gave at confirmation.
type ImportAcknowledgements struct {
	ConfirmNewSpaceMetadata bool `json:"confirm_new_space_metadata,omitempty"`
	PageOnlyPartialImport   bool `json:"page_only_partial_import,omitempty"`
	WidenRestrictedPages    bool `json:"widen_restricted_pages,omitempty"`
	ReimportExistingPages   bool `json:"reimport_existing_pages,omitempty"`
}

// ImportConfirmation is the persisted confirmation payload, stored in the
// DOCS_ImportJob.Confirmation jsonb column.
//
// OverwriteConflicts holds only external IDs: the browser never calculates or echoes source, local,
// or mapping hashes. The approved ID grants intent while the server-owned staged baselines — which
// execution rechecks under locks — grant safety.
type ImportConfirmation struct {
	PreflightRevision  string                  `json:"preflight_revision,omitempty"`
	NewSpace           *ImportNewSpaceMetadata `json:"new_space,omitempty"`
	Acknowledgements   ImportAcknowledgements  `json:"acknowledgements"`
	OverwriteConflicts []string                `json:"overwrite_conflicts,omitempty"`
}

// Value implements driver.Valuer for the Confirmation jsonb column.
func (c ImportConfirmation) Value() (driver.Value, error) {
	return marshalJSONColumn(c, ImportConfirmationMaxBytes, "import confirmation")
}

// Scan implements sql.Scanner for the Confirmation jsonb column.
func (c *ImportConfirmation) Scan(src any) error {
	return scanJSONColumn(src, c, "import confirmation")
}

// IsValid checks the confirmation's own shape limits: a well-formed revision and a bounded,
// duplicate-free list of contract-conforming external IDs. Cross-checking those IDs against
// persisted conflict results is the application's job.
func (c *ImportConfirmation) IsValid() *mmmodel.AppError {
	where := "ImportConfirmation.IsValid"
	if !hexSHA256.MatchString(c.PreflightRevision) {
		return mmmodel.NewAppError(where, "model.import_confirmation.is_valid.revision.app_error", nil, "", http.StatusBadRequest)
	}
	if len(c.OverwriteConflicts) > ImportMaxPages {
		return mmmodel.NewAppError(where, "model.import_confirmation.is_valid.too_many_overwrites.app_error",
			map[string]any{"Max": ImportMaxPages}, "", http.StatusBadRequest)
	}
	seen := make(map[string]struct{}, len(c.OverwriteConflicts))
	for _, id := range c.OverwriteConflicts {
		if !IsValidImportIdentifier(id, ImportExternalIDMaxBytes) {
			return mmmodel.NewAppError(where, "model.import_confirmation.is_valid.external_id.app_error", nil, "", http.StatusBadRequest)
		}
		if _, dup := seen[id]; dup {
			return mmmodel.NewAppError(where, "model.import_confirmation.is_valid.duplicate_overwrite.app_error", nil, "", http.StatusBadRequest)
		}
		seen[id] = struct{}{}
	}
	if c.NewSpace != nil && utf8.RuneCountInString(c.NewSpace.Title) > ImportSpaceTitleMaxRunes {
		return mmmodel.NewAppError(where, "model.import_confirmation.is_valid.space_title.app_error",
			map[string]any{"MaxLength": ImportSpaceTitleMaxRunes}, "", http.StatusBadRequest)
	}
	return nil
}

// --- request types ---

// ImportTargetRequest is the caller-supplied target of an import. The request — never a value from
// the bundle — chooses the destination: bundle team values are advisory metadata only.
type ImportTargetRequest struct {
	Kind ImportTargetKind `json:"kind"`
	// SpaceId is required when Kind is "existing" and must be absent otherwise. The Space's team is
	// derived from the Space itself, never from the request.
	SpaceId string `json:"space_id,omitempty"`
	// TeamId is required when Kind is "new" and must be absent otherwise.
	TeamId string `json:"team_id,omitempty"`
}

// ImportUploadRequest is the JSON `request` part of the multipart upload. The new-Space title and
// description are deliberately not accepted here: inspection reads the bundle defaults and
// confirmation supplies the final, user-edited values.
type ImportUploadRequest struct {
	Target ImportTargetRequest `json:"target"`
}

// IsValid checks that the target names exactly the one id its kind requires.
func (r *ImportTargetRequest) IsValid() *mmmodel.AppError {
	where := "ImportTargetRequest.IsValid"
	switch r.Kind {
	case ImportTargetExisting:
		if !mmmodel.IsValidId(r.SpaceId) {
			return mmmodel.NewAppError(where, "model.import_target.is_valid.space_id.app_error", nil, "", http.StatusBadRequest)
		}
		if r.TeamId != "" {
			return mmmodel.NewAppError(where, "model.import_target.is_valid.team_id_not_allowed.app_error", nil, "", http.StatusBadRequest)
		}
	case ImportTargetNew:
		if !mmmodel.IsValidId(r.TeamId) {
			return mmmodel.NewAppError(where, "model.import_target.is_valid.team_id.app_error", nil, "", http.StatusBadRequest)
		}
		if r.SpaceId != "" {
			return mmmodel.NewAppError(where, "model.import_target.is_valid.space_id_not_allowed.app_error", nil, "", http.StatusBadRequest)
		}
	default:
		return mmmodel.NewAppError(where, "model.import_target.is_valid.kind.app_error", nil, "", http.StatusBadRequest)
	}
	return nil
}

// Author fallback reason codes, recorded on a staged page and in the docs_import namespace when the
// Confluence author could not be resolved to a live Mattermost user. They are stable strings because they
// are persisted on the page itself and read back by later imports and reports.
const (
	// ImportFallbackSourceAuthorMissing means the bundle named no author at all for the page.
	ImportFallbackSourceAuthorMissing = "source_author_missing"
	// ImportFallbackManifestUserMissing means the page named a Confluence account the manifest does not map.
	ImportFallbackManifestUserMissing = "manifest_user_missing"
	// ImportFallbackUsernameMissing means the manifest mapped the account but proposed no username.
	ImportFallbackUsernameMissing = "mattermost_username_missing"
	// ImportFallbackUserNotFound means the proposed username matches no Mattermost user.
	ImportFallbackUserNotFound = "mattermost_user_not_found"
	// ImportFallbackUserInactive means the proposed username matches a deactivated user.
	ImportFallbackUserInactive = "mattermost_user_inactive"
)

// Acknowledgement keys the confirmation request must set. The set a given job requires is returned
// to the client rather than assumed, so a client never has to infer which apply.
const (
	ImportAckNewSpaceMetadata = "confirm_new_space_metadata"
	ImportAckPageOnlyPartial  = "page_only_partial_import"
	ImportAckWidenRestricted  = "widen_restricted_pages"
	ImportAckReimportExisting = "reimport_existing_pages"
)

// ImportSourceSelectionRequest is the body of POST /imports/{job_id}/source. An ImportSource is a
// user-confirmed local identity, so it is always chosen explicitly: candidate scores are suggestions
// and nothing is ever selected automatically.
type ImportSourceSelectionRequest struct {
	Mode ImportSourceSelectionMode `json:"mode"`
	// ImportSourceId is required for mode "existing" and must name a source belonging to the job's
	// target Space.
	ImportSourceId string `json:"import_source_id,omitempty"`
	// DisplayName is required for mode "new" and names the source that will be created at execution.
	DisplayName string `json:"display_name,omitempty"`
}

// IsValid checks that the selection names exactly what its mode requires.
func (r *ImportSourceSelectionRequest) IsValid() *mmmodel.AppError {
	where := "ImportSourceSelectionRequest.IsValid"
	switch r.Mode {
	case ImportSourceModeExisting:
		if !mmmodel.IsValidId(r.ImportSourceId) {
			return mmmodel.NewAppError(where, "model.import_source_selection.is_valid.import_source_id.app_error", nil, "", http.StatusBadRequest)
		}
		if r.DisplayName != "" {
			return mmmodel.NewAppError(where, "model.import_source_selection.is_valid.display_name_not_allowed.app_error", nil, "", http.StatusBadRequest)
		}
	case ImportSourceModeNew:
		if strings.TrimSpace(r.DisplayName) == "" {
			return mmmodel.NewAppError(where, "model.import_source_selection.is_valid.display_name.app_error", nil, "", http.StatusBadRequest)
		}
		// The name is persisted as it arrives, so it has to be storable here. A name carrying an escaped
		// NUL is a perfectly valid string to a JSON decoder and an error to PostgreSQL, and without this
		// check that difference surfaces as a 500 from the store rather than the bad request it is.
		if !IsStorableText(r.DisplayName) {
			return mmmodel.NewAppError(where, "model.import_source_selection.is_valid.display_name_unstorable.app_error", nil, "", http.StatusBadRequest)
		}
		if utf8.RuneCountInString(r.DisplayName) > ImportDisplayNameMaxRunes {
			return mmmodel.NewAppError(where, "model.import_source_selection.is_valid.display_name_too_long.app_error",
				map[string]any{"MaxLength": ImportDisplayNameMaxRunes}, "", http.StatusBadRequest)
		}
		if r.ImportSourceId != "" {
			return mmmodel.NewAppError(where, "model.import_source_selection.is_valid.import_source_id_not_allowed.app_error", nil, "", http.StatusBadRequest)
		}
	default:
		return mmmodel.NewAppError(where, "model.import_source_selection.is_valid.mode.app_error", nil, "", http.StatusBadRequest)
	}
	return nil
}

// ImportConfirmRequest is the body of POST /imports/{job_id}/confirm.
//
// Acknowledgements are a map rather than a struct so the request mirrors the job's own
// required_acknowledgements list: a client sets exactly the keys it was told to, and an unknown key is
// rejected instead of being silently ignored as a struct would.
type ImportConfirmRequest struct {
	PreflightRevision string `json:"preflight_revision"`
	// NewSpace carries the final, user-edited Space metadata. Required for a new-Space target and
	// rejected for an existing one.
	NewSpace         *ImportNewSpaceMetadata `json:"new_space,omitempty"`
	Acknowledgements map[string]bool         `json:"acknowledgements"`
	// OverwriteConflicts lists the external IDs whose conflicts the user approved overwriting. There is
	// deliberately no blanket overwrite-all flag: each conflict is approved individually.
	OverwriteConflicts []string `json:"overwrite_conflicts,omitempty"`
}

// IsValid checks the request's own shape. Which acknowledgements a given job requires, and whether the
// approved IDs name real conflicts, are cross-checks the application performs against persisted state.
func (r *ImportConfirmRequest) IsValid() *mmmodel.AppError {
	where := "ImportConfirmRequest.IsValid"
	if !hexSHA256.MatchString(r.PreflightRevision) {
		return mmmodel.NewAppError(where, "model.import_confirm.is_valid.revision.app_error", nil, "", http.StatusBadRequest)
	}
	for key := range r.Acknowledgements {
		if !slices.Contains(importAckKeys, key) {
			return mmmodel.NewAppError(where, "model.import_confirm.is_valid.unknown_acknowledgement.app_error",
				map[string]any{"Key": key}, "", http.StatusBadRequest)
		}
	}
	if r.NewSpace != nil {
		if strings.TrimSpace(r.NewSpace.Title) == "" {
			return mmmodel.NewAppError(where, "model.import_confirm.is_valid.space_title_required.app_error", nil, "", http.StatusBadRequest)
		}
		if utf8.RuneCountInString(r.NewSpace.Title) > ImportSpaceTitleMaxRunes {
			return mmmodel.NewAppError(where, "model.import_confirm.is_valid.space_title.app_error",
				map[string]any{"MaxLength": ImportSpaceTitleMaxRunes}, "", http.StatusBadRequest)
		}
		if utf8.RuneCountInString(r.NewSpace.Description) > SpaceDescriptionMaxRunes {
			return mmmodel.NewAppError(where, "model.import_confirm.is_valid.space_description.app_error",
				map[string]any{"MaxLength": SpaceDescriptionMaxRunes}, "", http.StatusBadRequest)
		}
		if !IsStorableText(r.NewSpace.Title) || !IsStorableText(r.NewSpace.Description) {
			return mmmodel.NewAppError(where, "model.import_confirm.is_valid.space_unstorable.app_error", nil, "", http.StatusBadRequest)
		}
	}
	// Reuse the persisted payload's bounds for the approved-ID list, so the request cannot carry a set
	// the confirmation column would then refuse to store.
	persisted := ImportConfirmation{PreflightRevision: r.PreflightRevision, OverwriteConflicts: r.OverwriteConflicts}
	return persisted.IsValid()
}

// importAckKeys is every acknowledgement key a confirmation may carry.
var importAckKeys = []string{
	ImportAckNewSpaceMetadata, ImportAckPageOnlyPartial, ImportAckWidenRestricted, ImportAckReimportExisting,
}

// Acknowledged reports whether the request set the given acknowledgement key.
func (r *ImportConfirmRequest) Acknowledged(key string) bool {
	return r.Acknowledgements[key]
}

// ToAcknowledgements converts the request's map into the persisted struct.
func (r *ImportConfirmRequest) ToAcknowledgements() ImportAcknowledgements {
	return ImportAcknowledgements{
		ConfirmNewSpaceMetadata: r.Acknowledged(ImportAckNewSpaceMetadata),
		PageOnlyPartialImport:   r.Acknowledged(ImportAckPageOnlyPartial),
		WidenRestrictedPages:    r.Acknowledged(ImportAckWidenRestricted),
		ReimportExistingPages:   r.Acknowledged(ImportAckReimportExisting),
	}
}

// --- API-safe projections ---

// ImportProgress is the public progress projection.
type ImportProgress struct {
	Phase   ImportJobPhase `json:"phase,omitempty"`
	Current int64          `json:"current"`
	Total   int64          `json:"total"`
}

// ImportTargetView is the public target projection.
type ImportTargetView struct {
	Kind    ImportTargetKind `json:"kind"`
	SpaceId string           `json:"space_id,omitempty"`
	TeamId  string           `json:"team_id"`
	Existed bool             `json:"existed"`
}

// ImportBundleSummary is the public inspected-bundle projection, also persisted as the job's
// BundleSummary jsonb column.
type ImportBundleSummary struct {
	Version       int                 `json:"version"`
	Source        ImportReportSource  `json:"source"`
	SpaceDefaults ImportSpaceDefaults `json:"space_defaults"`
	Counts        ImportBundleCounts  `json:"counts"`
	// Links are discovered while the bundle is inspected, so they belong to the bundle rather than to any
	// later stage: the plan and the final report both carry the same figures, because nothing about executing
	// an import changes how many placeholders the bundle contained.
	Links ImportLinkCounts `json:"links"`
}

// Value implements driver.Valuer for the BundleSummary jsonb column.
func (s ImportBundleSummary) Value() (driver.Value, error) {
	return marshalJSONColumn(s, ImportSummaryMaxBytes, "import bundle summary")
}

// Scan implements sql.Scanner for the BundleSummary jsonb column.
func (s *ImportBundleSummary) Scan(src any) error {
	return scanJSONColumn(src, s, "import bundle summary")
}

// ImportSpaceDefaults carries the bundle-derived, editable new-Space metadata.
type ImportSpaceDefaults struct {
	Title       string `json:"title"`
	Description string `json:"description"`
}

// ImportBundleCounts is the public bundle counts projection.
type ImportBundleCounts struct {
	Pages                   int `json:"pages"`
	Comments                int `json:"comments"`
	Attachments             int `json:"attachments"`
	RestrictedManifestTotal int `json:"restricted_manifest_total"`
	RestrictedEmittedPages  int `json:"restricted_emitted_pages"`
	RestrictedManifestOnly  int `json:"restricted_manifest_only"`
}

// ImportSourceCandidate is a suggested existing ImportSource with match reasons (never auto-selected).
type ImportSourceCandidate struct {
	ImportSourceId   string   `json:"import_source_id"`
	DisplayName      string   `json:"display_name"`
	OrganizationId   string   `json:"organization_id,omitempty"`
	ExternalSpaceKey string   `json:"external_space_key"`
	MappedPageCount  int      `json:"mapped_page_count"`
	LastImportAt     int64    `json:"last_import_at"`
	MatchReasons     []string `json:"match_reasons"`
}

// ImportSelectedSource is the public selected-source projection.
type ImportSelectedSource struct {
	Mode           ImportSourceSelectionMode `json:"mode"`
	ImportSourceId string                    `json:"import_source_id,omitempty"`
	DisplayName    string                    `json:"display_name,omitempty"`
}

// ImportPublicError is a scrubbed error projection (stable code only, never internal detail).
type ImportPublicError struct {
	Code string `json:"code"`
}

// ImportJobView is the API-safe projection of a job. It deliberately omits the provisioned channel
// ID, internal SQL errors, bodies, raw source props, persisted content/parent baselines, byte
// accounting, and manifest user rows.
type ImportJobView struct {
	Id                       string                  `json:"id"`
	State                    ImportJobState          `json:"state"`
	Phase                    ImportJobPhase          `json:"phase,omitempty"`
	Progress                 ImportProgress          `json:"progress"`
	Target                   ImportTargetView        `json:"target"`
	Bundle                   ImportBundleSummary     `json:"bundle"`
	SourceCandidates         []ImportSourceCandidate `json:"source_candidates"`
	SelectedSource           *ImportSelectedSource   `json:"selected_source,omitempty"`
	Preflight                *ImportReportSummary    `json:"preflight,omitempty"`
	Final                    *ImportReportSummary    `json:"final,omitempty"`
	RequiredAcknowledgements []string                `json:"required_acknowledgements"`
	Error                    *ImportPublicError      `json:"error,omitempty"`
	CreateAt                 int64                   `json:"create_at"`
	UpdateAt                 int64                   `json:"update_at"`
	FinishedAt               int64                   `json:"finished_at"`
}

// ImportPreflightResultView is one row of the typed, paginated preflight review projection. It
// exposes what the wizard displays plus overwrite eligibility, and never hashes, mapping
// timestamps, bodies, or raw props.
type ImportPreflightResultView struct {
	ExternalId        string       `json:"external_id"`
	LocalId           string       `json:"local_id,omitempty"`
	Title             string       `json:"title"`
	PlannedAction     ImportAction `json:"planned_action"`
	Outcome           string       `json:"outcome"`
	OverwriteEligible bool         `json:"overwrite_eligible"`
	StructuralChanges []string     `json:"structural_changes,omitempty"`
}

// --- validation ---

// IsValid checks an ImportJob's required fields and enumerated values before insert. It does not
// validate the full state machine (transitions are enforced by compare-and-set store updates).
func (j *ImportJob) IsValid() *mmmodel.AppError {
	where := "ImportJob.IsValid"
	if !mmmodel.IsValidId(j.Id) {
		return mmmodel.NewAppError(where, "model.import_job.is_valid.id.app_error", nil, "", http.StatusBadRequest)
	}
	if !mmmodel.IsValidId(j.ActorId) {
		return mmmodel.NewAppError(where, "model.import_job.is_valid.actor_id.app_error", nil, "id="+j.Id, http.StatusBadRequest)
	}
	if !mmmodel.IsValidId(j.TeamId) {
		return mmmodel.NewAppError(where, "model.import_job.is_valid.team_id.app_error", nil, "id="+j.Id, http.StatusBadRequest)
	}
	if !j.TargetKind.IsValid() {
		return mmmodel.NewAppError(where, "model.import_job.is_valid.target_kind.app_error", nil, "id="+j.Id, http.StatusBadRequest)
	}
	if !mmmodel.IsValidId(j.TargetSpaceId) {
		return mmmodel.NewAppError(where, "model.import_job.is_valid.target_space_id.app_error", nil, "id="+j.Id, http.StatusBadRequest)
	}
	if !j.SourceSelectionMode.IsValid() {
		return mmmodel.NewAppError(where, "model.import_job.is_valid.source_mode.app_error", nil, "id="+j.Id, http.StatusBadRequest)
	}
	if !j.State.IsValid() {
		return mmmodel.NewAppError(where, "model.import_job.is_valid.state.app_error", nil, "id="+j.Id, http.StatusBadRequest)
	}
	if !j.TerminalIntent.IsValid() {
		return mmmodel.NewAppError(where, "model.import_job.is_valid.terminal_intent.app_error", nil, "id="+j.Id, http.StatusBadRequest)
	}
	// Terminalizing exists precisely to carry an intent, so an intentless terminalizing job could
	// never decide what outcome to publish.
	if j.State == ImportStateTerminalizing && j.TerminalIntent == ImportIntentNone {
		return mmmodel.NewAppError(where, "model.import_job.is_valid.terminalizing_without_intent.app_error", nil, "id="+j.Id, http.StatusBadRequest)
	}
	// BundleSha256 is always computed during upload inspection, so a persisted job must carry a
	// valid 64-hex digest; an empty value is a bug, not a valid pre-inspection state.
	if !hexSHA256.MatchString(j.BundleSha256) {
		return mmmodel.NewAppError(where, "model.import_job.is_valid.bundle_sha.app_error", nil, "id="+j.Id, http.StatusBadRequest)
	}
	if !IsValidImportHash(j.PreflightRevision) {
		return mmmodel.NewAppError(where, "model.import_job.is_valid.preflight_revision.app_error", nil, "id="+j.Id, http.StatusBadRequest)
	}
	// Enforce the string-length bounds that back the DB VARCHAR columns, so an over-long value is
	// rejected here rather than as an opaque PostgreSQL constraint violation on insert.
	if utf8.RuneCountInString(j.ConfirmedSpaceTitle) > ImportSpaceTitleMaxRunes {
		return mmmodel.NewAppError(where, "model.import_job.is_valid.space_title_length.app_error", map[string]any{"MaxLength": ImportSpaceTitleMaxRunes}, "id="+j.Id, http.StatusBadRequest)
	}
	if utf8.RuneCountInString(j.SelectedSourceDisplayName) > ImportDisplayNameMaxRunes {
		return mmmodel.NewAppError(where, "model.import_job.is_valid.source_display_name_length.app_error", map[string]any{"MaxLength": ImportDisplayNameMaxRunes}, "id="+j.Id, http.StatusBadRequest)
	}
	if utf8.RuneCountInString(j.ErrorCode) > ImportErrorCodeMaxRunes {
		return mmmodel.NewAppError(where, "model.import_job.is_valid.error_code_length.app_error", map[string]any{"MaxLength": ImportErrorCodeMaxRunes}, "id="+j.Id, http.StatusBadRequest)
	}
	if j.StagedBytes < 0 || j.RetainedBytes < 0 || j.RetainedIssueBytes < 0 || j.RetainedReservedBytes < 0 ||
		j.PreflightRetainedBytes < 0 || j.PreflightRetainedIssueBytes < 0 {
		return mmmodel.NewAppError(where, "model.import_job.is_valid.byte_accounting.app_error", nil, "id="+j.Id, http.StatusBadRequest)
	}
	if j.CreateAt == 0 || j.UpdateAt == 0 || j.RetainUntil == 0 {
		return mmmodel.NewAppError(where, "model.import_job.is_valid.timestamps.app_error", nil, "id="+j.Id, http.StatusBadRequest)
	}
	return nil
}

// IsValid checks an ImportSource's required fields before insert.
func (s *ImportSource) IsValid() *mmmodel.AppError {
	where := "ImportSource.IsValid"
	if !mmmodel.IsValidId(s.Id) {
		return mmmodel.NewAppError(where, "model.import_source.is_valid.id.app_error", nil, "", http.StatusBadRequest)
	}
	if !mmmodel.IsValidId(s.SpaceId) {
		return mmmodel.NewAppError(where, "model.import_source.is_valid.space_id.app_error", nil, "id="+s.Id, http.StatusBadRequest)
	}
	if s.SourceType != ImportSourceTypeConfluence {
		return mmmodel.NewAppError(where, "model.import_source.is_valid.source_type.app_error", nil, "id="+s.Id, http.StatusBadRequest)
	}
	if !mmmodel.IsValidId(s.CreatedBy) {
		return mmmodel.NewAppError(where, "model.import_source.is_valid.created_by.app_error", nil, "id="+s.Id, http.StatusBadRequest)
	}
	if !IsValidImportIdentifier(s.ExternalSpaceKey, ImportSpaceKeyMaxBytes) {
		return mmmodel.NewAppError(where, "model.import_source.is_valid.space_key.app_error", nil, "id="+s.Id, http.StatusBadRequest)
	}
	// The organization id is optional metadata, but when present it is indexed and must be bounded.
	if s.OrganizationId != "" && !IsValidImportIdentifier(s.OrganizationId, ImportSpaceKeyMaxBytes) {
		return mmmodel.NewAppError(where, "model.import_source.is_valid.organization_id.app_error", nil, "id="+s.Id, http.StatusBadRequest)
	}
	if utf8.RuneCountInString(s.DisplayName) > ImportDisplayNameMaxRunes {
		return mmmodel.NewAppError(where, "model.import_source.is_valid.display_name_length.app_error", map[string]any{"MaxLength": ImportDisplayNameMaxRunes}, "id="+s.Id, http.StatusBadRequest)
	}
	if s.MappingRevision < 0 {
		return mmmodel.NewAppError(where, "model.import_source.is_valid.mapping_revision.app_error", nil, "id="+s.Id, http.StatusBadRequest)
	}
	if s.CreateAt == 0 || s.UpdateAt == 0 {
		return mmmodel.NewAppError(where, "model.import_source.is_valid.timestamps.app_error", nil, "id="+s.Id, http.StatusBadRequest)
	}
	return nil
}

// IsValid checks a staged page's required fields, bounded identifiers, and hashes before insert.
func (p *ImportStagedPage) IsValid() *mmmodel.AppError {
	where := "ImportStagedPage.IsValid"
	if !mmmodel.IsValidId(p.JobId) {
		return mmmodel.NewAppError(where, "model.import_staged_page.is_valid.job_id.app_error", nil, "", http.StatusBadRequest)
	}
	if p.Ordinal < 0 || p.Ordinal >= ImportMaxPages {
		return mmmodel.NewAppError(where, "model.import_staged_page.is_valid.ordinal.app_error", map[string]any{"Max": ImportMaxPages}, "", http.StatusBadRequest)
	}
	if p.SourceLine < 1 {
		return mmmodel.NewAppError(where, "model.import_staged_page.is_valid.source_line.app_error", nil, "", http.StatusBadRequest)
	}
	if !IsValidImportIdentifier(p.ExternalId, ImportExternalIDMaxBytes) {
		return mmmodel.NewAppError(where, "model.import_staged_page.is_valid.external_id.app_error", nil, "", http.StatusBadRequest)
	}
	if p.ParentExternalId != "" && !IsValidImportIdentifier(p.ParentExternalId, ImportExternalIDMaxBytes) {
		return mmmodel.NewAppError(where, "model.import_staged_page.is_valid.parent_external_id.app_error", nil, "", http.StatusBadRequest)
	}
	if p.SourceAuthorAccountId != "" && !IsValidImportIdentifier(p.SourceAuthorAccountId, ImportExternalIDMaxBytes) {
		return mmmodel.NewAppError(where, "model.import_staged_page.is_valid.author_account_id.app_error", nil, "", http.StatusBadRequest)
	}
	if p.Title == "" || utf8.RuneCountInString(p.Title) > PageTitleMaxRunes {
		return mmmodel.NewAppError(where, "model.import_staged_page.is_valid.title.app_error", map[string]any{"MaxLength": PageTitleMaxRunes}, "", http.StatusBadRequest)
	}
	if !hexSHA256.MatchString(p.IncomingSourceContentHash) {
		return mmmodel.NewAppError(where, "model.import_staged_page.is_valid.incoming_hash.app_error", nil, "", http.StatusBadRequest)
	}
	for _, h := range []string{p.PreflightCurrentContentHash, p.PreflightMappingContentHash} {
		if !IsValidImportHash(h) {
			return mmmodel.NewAppError(where, "model.import_staged_page.is_valid.preflight_hash.app_error", nil, "", http.StatusBadRequest)
		}
	}
	if !p.PlannedAction.IsValid() {
		return mmmodel.NewAppError(where, "model.import_staged_page.is_valid.planned_action.app_error", nil, "", http.StatusBadRequest)
	}
	return nil
}

// IsValid checks a manifest-user row's bounded account identifier.
func (u *ImportManifestUser) IsValid() *mmmodel.AppError {
	where := "ImportManifestUser.IsValid"
	if !mmmodel.IsValidId(u.JobId) {
		return mmmodel.NewAppError(where, "model.import_manifest_user.is_valid.job_id.app_error", nil, "", http.StatusBadRequest)
	}
	if u.Ordinal < 0 {
		return mmmodel.NewAppError(where, "model.import_manifest_user.is_valid.ordinal.app_error", nil, "", http.StatusBadRequest)
	}
	if !IsValidImportIdentifier(u.AccountId, ImportExternalIDMaxBytes) {
		return mmmodel.NewAppError(where, "model.import_manifest_user.is_valid.account_id.app_error", nil, "", http.StatusBadRequest)
	}
	return nil
}

// IsValid checks an ImportIssueRecord's enumerated values and the code-length bound that backs the
// DOCS_ImportIssue.Code VARCHAR(64) column, so an over-long or unknown code is rejected before insert.
func (r *ImportIssueRecord) IsValid() *mmmodel.AppError {
	where := "ImportIssueRecord.IsValid"
	if !r.Stage.IsValid() {
		return mmmodel.NewAppError(where, "model.import_issue.is_valid.stage.app_error", nil, "", http.StatusBadRequest)
	}
	if !r.Severity.IsValid() {
		return mmmodel.NewAppError(where, "model.import_issue.is_valid.severity.app_error", nil, "", http.StatusBadRequest)
	}
	if r.Code == "" || utf8.RuneCountInString(r.Code) > ImportIssueCodeMaxRunes {
		return mmmodel.NewAppError(where, "model.import_issue.is_valid.code.app_error", map[string]any{"MaxLength": ImportIssueCodeMaxRunes}, "", http.StatusBadRequest)
	}
	if r.Message == "" {
		return mmmodel.NewAppError(where, "model.import_issue.is_valid.message.app_error", nil, "", http.StatusBadRequest)
	}
	if r.ExternalId != "" && !IsValidImportIdentifier(r.ExternalId, ImportExternalIDMaxBytes) {
		return mmmodel.NewAppError(where, "model.import_issue.is_valid.external_id.app_error", nil, "", http.StatusBadRequest)
	}
	if r.LocalId != "" && !mmmodel.IsValidId(r.LocalId) {
		return mmmodel.NewAppError(where, "model.import_issue.is_valid.local_id.app_error", nil, "", http.StatusBadRequest)
	}
	if len(r.Message) > ImportIssueTextMaxBytes || len(r.Remediation) > ImportIssueTextMaxBytes {
		return mmmodel.NewAppError(where, "model.import_issue.is_valid.text_too_long.app_error",
			map[string]any{"MaxBytes": ImportIssueTextMaxBytes}, "", http.StatusBadRequest)
	}
	if utf8.RuneCountInString(r.Title) > PageTitleMaxRunes {
		return mmmodel.NewAppError(where, "model.import_issue.is_valid.title_too_long.app_error",
			map[string]any{"MaxLength": PageTitleMaxRunes}, "", http.StatusBadRequest)
	}
	return validateImportDetails(where, "issue details", r.Details)
}

// IsValid checks a result row's stage, action, and outcome enumerations.
func (r *ImportResultRecord) IsValid() *mmmodel.AppError {
	where := "ImportResultRecord.IsValid"
	if !r.Stage.IsResultStage() {
		return mmmodel.NewAppError(where, "model.import_result.is_valid.stage.app_error", nil, "", http.StatusBadRequest)
	}
	if r.Ordinal < 0 {
		return mmmodel.NewAppError(where, "model.import_result.is_valid.ordinal.app_error", nil, "", http.StatusBadRequest)
	}
	if !r.PlannedAction.IsValid() || !r.ActualAction.IsValid() {
		return mmmodel.NewAppError(where, "model.import_result.is_valid.action.app_error", nil, "", http.StatusBadRequest)
	}
	if !r.Outcome.IsValid() {
		return mmmodel.NewAppError(where, "model.import_result.is_valid.outcome.app_error", nil, "", http.StatusBadRequest)
	}
	if !IsValidImportIdentifier(r.ExternalId, ImportExternalIDMaxBytes) {
		return mmmodel.NewAppError(where, "model.import_result.is_valid.external_id.app_error", nil, "", http.StatusBadRequest)
	}
	// A result row is the durable record of what happened to one entity, so an empty outcome would
	// persist a checkpoint that says nothing — the one thing reports must never contain.
	if r.Outcome == "" {
		return mmmodel.NewAppError(where, "model.import_result.is_valid.outcome_required.app_error", nil, "", http.StatusBadRequest)
	}
	if r.LocalId != "" && !mmmodel.IsValidId(r.LocalId) {
		return mmmodel.NewAppError(where, "model.import_result.is_valid.local_id.app_error", nil, "", http.StatusBadRequest)
	}
	if utf8.RuneCountInString(r.Title) > PageTitleMaxRunes {
		return mmmodel.NewAppError(where, "model.import_result.is_valid.title_too_long.app_error",
			map[string]any{"MaxLength": PageTitleMaxRunes}, "", http.StatusBadRequest)
	}
	if r.CreateAt == 0 || r.UpdateAt == 0 {
		return mmmodel.NewAppError(where, "model.import_result.is_valid.timestamps.app_error", nil, "", http.StatusBadRequest)
	}
	if r.EntityType != "" && r.EntityType != ImportEntityTypePage {
		return mmmodel.NewAppError(where, "model.import_result.is_valid.entity_type.app_error", nil, "", http.StatusBadRequest)
	}
	return validateImportDetails(where, "result details", r.Details)
}
