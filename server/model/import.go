// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package model

import (
	"net/http"
	"regexp"
	"unicode/utf8"

	mmmodel "github.com/mattermost/mattermost/server/public/model"
)

// ImportJobState is a persisted import-job lifecycle state. The values here must exactly match the
// chk_docs_importjob_state CHECK constraint in migration 000006.
type ImportJobState string

const (
	ImportStateAwaitingSource       ImportJobState = "awaiting_source"
	ImportStateWaitingSourceTurn    ImportJobState = "waiting_source_turn"
	ImportStateQueuedPreflight      ImportJobState = "queued_preflight"
	ImportStatePreflighting         ImportJobState = "preflighting"
	ImportStateAwaitingConfirmation ImportJobState = "awaiting_confirmation"
	ImportStateQueuedImport         ImportJobState = "queued_import"
	ImportStateImporting            ImportJobState = "importing"
	ImportStateCanceling            ImportJobState = "canceling"
	ImportStateCompleted            ImportJobState = "completed"
	ImportStateCompletedWithIssues  ImportJobState = "completed_with_issues"
	ImportStateFailed               ImportJobState = "failed"
	ImportStateCanceled             ImportJobState = "canceled"
)

// validImportStates is the set matching the DB CHECK; used for model-level validation.
var validImportStates = map[ImportJobState]struct{}{
	ImportStateAwaitingSource: {}, ImportStateWaitingSourceTurn: {},
	ImportStateQueuedPreflight: {}, ImportStatePreflighting: {},
	ImportStateAwaitingConfirmation: {},
	ImportStateQueuedImport:         {}, ImportStateImporting: {}, ImportStateCanceling: {},
	ImportStateCompleted: {}, ImportStateCompletedWithIssues: {},
	ImportStateFailed: {}, ImportStateCanceled: {},
}

// IsValid reports whether s is a known import state.
func (s ImportJobState) IsValid() bool {
	_, ok := validImportStates[s]
	return ok
}

// IsTerminal reports whether s is a terminal (finished) state. Source-queue release and cleanup key
// off this predicate.
func (s ImportJobState) IsTerminal() bool {
	switch s {
	case ImportStateCompleted, ImportStateCompletedWithIssues, ImportStateFailed, ImportStateCanceled:
		return true
	default:
		return false
	}
}

// OwnsSourceQueue reports whether a job in state s retains ownership of its ImportSource's
// ActiveJobId (from preflight through the end of execution). Terminal states never own the queue.
func (s ImportJobState) OwnsSourceQueue() bool {
	switch s {
	case ImportStateQueuedPreflight, ImportStatePreflighting,
		ImportStateAwaitingConfirmation,
		ImportStateQueuedImport, ImportStateImporting, ImportStateCanceling:
		return true
	default:
		return false
	}
}

// ImportJobPhase is a free-form, human-facing progress phase label (no DB CHECK).
type ImportJobPhase string

const (
	ImportPhaseInspecting       ImportJobPhase = "inspecting"
	ImportPhaseResolvingUsers   ImportJobPhase = "resolving_users"
	ImportPhaseComputingActions ImportJobPhase = "computing_actions"
	ImportPhaseProvisioning     ImportJobPhase = "provisioning_space"
	ImportPhaseWritingPages     ImportJobPhase = "writing_pages"
	ImportPhaseFinalizing       ImportJobPhase = "finalizing"
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
	ImportActionStale         ImportAction = "stale"
	ImportActionBlocked       ImportAction = "blocked"
)

// Import entity, stage, and severity constants match their respective DB CHECK constraints.
const (
	ImportEntityTypePage = "page"

	ImportStageInspection = "inspection"
	ImportStagePreflight  = "preflight"
	ImportStageExecution  = "execution"

	ImportSeverityInfo    = "info"
	ImportSeverityWarning = "warning"
	ImportSeverityError   = "error"

	ImportSourceTypeConfluence = "confluence"
)

// Field size limits enforced at the model boundary (mirroring the migration's column types).
const (
	ImportDisplayNameMaxRunes = 255
	ImportSpaceTitleMaxRunes  = 128
	ImportErrorCodeMaxRunes   = 64
	ImportIssueCodeMaxRunes   = 64
)

// hexSHA256 matches exactly 64 lowercase hexadecimal characters. Every non-empty SHA-256 column is
// validated against this at the model/application boundary so a malformed or CHAR-padded value
// never enters a comparison.
var hexSHA256 = regexp.MustCompile(`^[0-9a-f]{64}$`)

// IsValidImportHash reports whether s is empty or exactly 64 lowercase hex characters.
func IsValidImportHash(s string) bool {
	return s == "" || hexSHA256.MatchString(s)
}

// ImportSource identifies one Confluence Space chosen by a user within one target Docs Space. It —
// not organization id or space key — scopes page mappings (DOCS_ImportSource).
type ImportSource struct {
	Id                  string                  `json:"id"`
	SpaceId             string                  `json:"space_id"`
	SourceType          string                  `json:"source_type"`
	DisplayName         string                  `json:"display_name"`
	OrganizationId      string                  `json:"organization_id,omitempty"`
	ExternalSpaceKey    string                  `json:"external_space_key"`
	ExternalSpaceName   string                  `json:"external_space_name"`
	CreatedBy           string                  `json:"created_by"`
	CreateAt            int64                   `json:"create_at"`
	UpdateAt            int64                   `json:"update_at"`
	LastImportAt        int64                   `json:"last_import_at"`
	LastSuccessfulJobId string                  `json:"last_successful_job_id,omitempty"`
	ActiveJobId         string                  `json:"active_job_id,omitempty"`
	Props               mmmodel.StringInterface `json:"props"`
}

// ImportJob is one restartable import lifecycle (DOCS_ImportJob). Claim/lease fields are internal
// and never exposed through ImportJobView.
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

	State           ImportJobState `json:"state"`
	Phase           ImportJobPhase `json:"phase,omitempty"`
	ProgressCurrent int64          `json:"progress_current"`
	ProgressTotal   int64          `json:"progress_total"`

	BundleSha256      string                  `json:"-"`
	BundleSummary     mmmodel.StringInterface `json:"bundle_summary"`
	PreflightSummary  mmmodel.StringInterface `json:"preflight_summary"`
	PreflightRevision string                  `json:"preflight_revision,omitempty"`
	Confirmation      mmmodel.StringInterface `json:"-"`
	FinalSummary      mmmodel.StringInterface `json:"final_summary"`

	ErrorCode         string `json:"error_code,omitempty"`
	ErrorMessage      string `json:"-"`
	CancelRequestedAt int64  `json:"cancel_requested_at"`

	ClaimToken     string `json:"-"`
	ClaimedBy      string `json:"-"`
	LeaseExpiresAt int64  `json:"-"`
	HeartbeatAt    int64  `json:"-"`

	CreateAt    int64 `json:"create_at"`
	UpdateAt    int64 `json:"update_at"`
	ConfirmedAt int64 `json:"confirmed_at"`
	StartedAt   int64 `json:"started_at"`
	FinishedAt  int64 `json:"finished_at"`
	RetainUntil int64 `json:"-"`
}

// ImportStagedPage is one normalized staged page (DOCS_ImportStagedPage), retained until cleanup.
type ImportStagedPage struct {
	JobId            string `json:"job_id"`
	Ordinal          int    `json:"ordinal"`
	ExternalId       string `json:"external_id"`
	ParentExternalId string `json:"parent_external_id"`
	SourceOrdinal    int    `json:"source_ordinal"`

	Title                 string                  `json:"title"`
	CanonicalBody         string                  `json:"-"`
	SearchText            string                  `json:"-"`
	SourceUserProposal    string                  `json:"source_user_proposal"`
	SourceAuthorAccountId string                  `json:"source_author_account_id"`
	SourceCreateAt        int64                   `json:"source_create_at"`
	SourceUpdateAt        int64                   `json:"source_update_at"`
	SourceProps           mmmodel.StringInterface `json:"source_props"`

	IncomingSourceHash       string       `json:"incoming_source_hash"`
	PreflightCurrentHash     string       `json:"preflight_current_hash,omitempty"`
	PreflightMappingHash     string       `json:"preflight_mapping_hash,omitempty"`
	PreflightMappingUpdateAt int64        `json:"preflight_mapping_update_at,omitempty"`
	PlannedAction            ImportAction `json:"planned_action,omitempty"`
	PlannedPageId            string       `json:"planned_page_id,omitempty"`
	ResolvedUserId           string       `json:"resolved_user_id,omitempty"`
	AuthorFallbackReason     string       `json:"author_fallback_reason,omitempty"`
}

// ImportEntity is the durable page mapping (DOCS_ImportEntity), the idempotency boundary.
type ImportEntity struct {
	ImportSourceId             string `json:"import_source_id"`
	EntityType                 string `json:"entity_type"`
	ExternalId                 string `json:"external_id"`
	LocalId                    string `json:"local_id"`
	LastSourceHash             string `json:"last_source_hash"`
	LastAppliedHash            string `json:"last_applied_hash"`
	LastSourceParentExternalId string `json:"last_source_parent_external_id"`
	LastSourceOrdinal          int    `json:"last_source_ordinal"`
	FirstJobId                 string `json:"first_job_id"`
	LastSeenJobId              string `json:"last_seen_job_id"`
	CreateAt                   int64  `json:"create_at"`
	UpdateAt                   int64  `json:"update_at"`
}

// ImportResultRecord is one durable entity-level outcome row (DOCS_ImportResult).
type ImportResultRecord struct {
	JobId         string                  `json:"job_id"`
	Stage         string                  `json:"stage"`
	Ordinal       int                     `json:"ordinal"`
	EntityType    string                  `json:"entity_type"`
	ExternalId    string                  `json:"external_id"`
	LocalId       string                  `json:"local_id,omitempty"`
	Title         string                  `json:"title,omitempty"`
	PlannedAction ImportAction            `json:"planned_action,omitempty"`
	ActualAction  ImportAction            `json:"actual_action,omitempty"`
	Outcome       string                  `json:"outcome"`
	Details       mmmodel.StringInterface `json:"details,omitempty"`
	CreateAt      int64                   `json:"create_at"`
	UpdateAt      int64                   `json:"update_at"`
}

// ImportIssueRecord is one durable issue row (DOCS_ImportIssue).
type ImportIssueRecord struct {
	JobId       string                  `json:"job_id"`
	Stage       string                  `json:"stage"`
	Ordinal     int                     `json:"ordinal"`
	Severity    string                  `json:"severity"`
	Code        string                  `json:"code"`
	EntityType  string                  `json:"entity_type,omitempty"`
	ExternalId  string                  `json:"external_id,omitempty"`
	LocalId     string                  `json:"local_id,omitempty"`
	Title       string                  `json:"title,omitempty"`
	Message     string                  `json:"message"`
	Remediation string                  `json:"remediation,omitempty"`
	Details     mmmodel.StringInterface `json:"details,omitempty"`
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

// ImportBundleSummary is the public inspected-bundle projection.
type ImportBundleSummary struct {
	Version       int                 `json:"version"`
	Source        ImportReportSource  `json:"source"`
	SpaceDefaults ImportSpaceDefaults `json:"space_defaults"`
	Counts        ImportBundleCounts  `json:"counts"`
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

// ImportJobView is the API-safe projection of a job. It deliberately omits claim tokens, lease
// owners, provisioned channel IDs, internal SQL errors, bodies, and raw source props.
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
	if s.ExternalSpaceKey == "" {
		return mmmodel.NewAppError(where, "model.import_source.is_valid.space_key.app_error", nil, "id="+s.Id, http.StatusBadRequest)
	}
	if utf8.RuneCountInString(s.DisplayName) > ImportDisplayNameMaxRunes {
		return mmmodel.NewAppError(where, "model.import_source.is_valid.display_name_length.app_error", map[string]any{"MaxLength": ImportDisplayNameMaxRunes}, "id="+s.Id, http.StatusBadRequest)
	}
	if s.CreateAt == 0 || s.UpdateAt == 0 {
		return mmmodel.NewAppError(where, "model.import_source.is_valid.timestamps.app_error", nil, "id="+s.Id, http.StatusBadRequest)
	}
	return nil
}

// IsValid checks an ImportIssueRecord's enumerated values and the code-length bound that backs the
// DOCS_ImportIssue.Code VARCHAR(64) column, so an over-long or unknown code is rejected before insert.
func (r *ImportIssueRecord) IsValid() *mmmodel.AppError {
	where := "ImportIssueRecord.IsValid"
	switch r.Stage {
	case ImportStageInspection, ImportStagePreflight, ImportStageExecution:
	default:
		return mmmodel.NewAppError(where, "model.import_issue.is_valid.stage.app_error", nil, "", http.StatusBadRequest)
	}
	switch r.Severity {
	case ImportSeverityInfo, ImportSeverityWarning, ImportSeverityError:
	default:
		return mmmodel.NewAppError(where, "model.import_issue.is_valid.severity.app_error", nil, "", http.StatusBadRequest)
	}
	if r.Code == "" || utf8.RuneCountInString(r.Code) > ImportIssueCodeMaxRunes {
		return mmmodel.NewAppError(where, "model.import_issue.is_valid.code.app_error", map[string]any{"MaxLength": ImportIssueCodeMaxRunes}, "", http.StatusBadRequest)
	}
	if r.Message == "" {
		return mmmodel.NewAppError(where, "model.import_issue.is_valid.message.app_error", nil, "", http.StatusBadRequest)
	}
	return nil
}
