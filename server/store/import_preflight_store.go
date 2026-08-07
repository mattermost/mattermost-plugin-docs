// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package store

import (
	"database/sql"
	"strings"

	"github.com/jmoiron/sqlx"

	mmmodel "github.com/mattermost/mattermost/server/public/model"
	sq "github.com/mattermost/squirrel"
	"github.com/pkg/errors"

	"github.com/mattermost/mattermost-plugin-docs/server/model"
)

// importEntityColumns are the DOCS_ImportEntity columns, in select/insert order.
var importEntityColumns = []string{
	"ImportSourceId", "EntityType", "ExternalId", "LocalId",
	"LastSourceContentHash", "LastAppliedContentHash", "LastAppliedParentId", "LastSourceParentExternalId",
	"LastSourceTitle", "LastSourceOrdinal", "FirstJobId", "LastSeenJobId", "CreateAt", "UpdateAt",
}

// ErrPreflightStale reports that the reviewed preflight no longer describes the source it was computed
// against, because another job changed that source's mappings in between. It is not a client error: the
// request was well-formed and the job has been returned to preflight so it can be reviewed again.
type ErrPreflightStale struct {
	JobID string
}

func (e *ErrPreflightStale) Error() string {
	return "import preflight is stale and is being recomputed: job_id=" + e.JobID
}

// IsErrPreflightStale reports whether err is an ErrPreflightStale.
func IsErrPreflightStale(err error) bool {
	var e *ErrPreflightStale
	return errors.As(err, &e)
}

// ErrImportSourceMissing reports that a job's selected ImportSource no longer exists.
//
// It is deliberately its own type rather than a plain not-found: a missing *job* means the work vanished and
// the worker should move on, while a missing *source* means this job can never proceed and must be failed.
// Conflating the two makes the worker retry forever, re-selecting the same job on every pass and starving
// everything behind it.
type ErrImportSourceMissing struct {
	JobID    string
	SourceID string
}

func (e *ErrImportSourceMissing) Error() string {
	return "import job " + e.JobID + " selected an import source that no longer exists: " + e.SourceID
}

// IsErrImportSourceMissing reports whether err is an ErrImportSourceMissing.
func IsErrImportSourceMissing(err error) bool {
	var e *ErrImportSourceMissing
	return errors.As(err, &e)
}

// --- source selection ---

// SelectImportSource records the actor's explicit ImportSource choice and queues the job for preflight.
// An ImportSource is a user-confirmed local identity, so V1 never selects one automatically: candidate
// scores are suggestions and this call is the only thing that binds a job to a source.
//
// For mode "new" the source id is generated and persisted on the job but no DOCS_ImportSource row is
// inserted: an unconfirmed job must not create a durable identity that later jobs could match against.
// The row appears at execution.
func (s *Store) SelectImportSource(jobID, actorID string, sel model.ImportSourceSelectionRequest) (_ *model.ImportJob, err error) {
	if jobID == "" {
		return nil, &ErrInvalidInput{Entity: "ImportJob", Field: "id", Value: jobID}
	}
	if validErr := sel.IsValid(); validErr != nil {
		return nil, &ErrInvalidInput{Entity: "ImportSourceSelection", Field: "IsValid", Value: validErr.Error(), Reason: validErr.Id}
	}

	tx, err := s.db.Beginx()
	if err != nil {
		return nil, errors.Wrap(err, "begin_transaction")
	}
	defer s.finalizeTransaction(tx, &err)

	job, err := s.lockImportJobForActor(tx, jobID, actorID)
	if err != nil {
		return nil, err
	}
	if job.State != model.ImportStateAwaitingSource {
		return nil, &ErrConflict{Resource: "ImportJob state=" + string(job.State)}
	}

	sourceID := sel.ImportSourceId
	if sel.Mode == model.ImportSourceModeExisting {
		// The source must belong to this job's target Space. Matching on both ids in one predicate means
		// a source in a Space the actor cannot reach reads as absent rather than as forbidden.
		var owned int
		ownedBuilder := s.getQueryBuilder().
			Select("COUNT(*)").
			From("DOCS_ImportSource").
			Where(sq.Eq{"Id": sourceID, "SpaceId": job.TargetSpaceId})
		if err = s.getBuilder(tx, &owned, ownedBuilder); err != nil {
			return nil, errors.Wrap(err, "unable_to_verify_import_source_space")
		}
		if owned == 0 {
			return nil, &ErrNotFound{EntityName: "ImportSource", ID: sourceID}
		}
	} else {
		sourceID = mmmodel.NewId()
	}

	now := mmmodel.GetMillis()
	updateBuilder := s.getQueryBuilder().
		Update("DOCS_ImportJob").
		Set("SourceSelectionMode", string(sel.Mode)).
		Set("SelectedImportSourceId", sourceID).
		Set("SelectedSourceDisplayName", sel.DisplayName).
		Set("State", string(model.ImportStateQueuedPreflight)).
		Set("UpdateAt", monotonicBump("UpdateAt", now)).
		Where(sq.Eq{"Id": jobID, "State": string(model.ImportStateAwaitingSource)})
	result, err := s.execBuilder(tx, updateBuilder)
	if err != nil {
		return nil, errors.Wrap(err, "unable_to_select_import_source")
	}
	if err = checkRowsAffected(result, "ImportJob", jobID); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, errors.Wrap(err, "commit_transaction")
	}

	job.SourceSelectionMode = sel.Mode
	job.SelectedImportSourceId = sourceID
	job.SelectedSourceDisplayName = sel.DisplayName
	job.State = model.ImportStateQueuedPreflight
	job.UpdateAt = max(job.UpdateAt+1, now)
	return job, nil
}

// lockImportJobForActor locks a job row and enforces actor-only visibility. An empty actorID means
// "system" (the worker and maintenance sweeps) and skips the ownership check. Must be called inside tx.
func (s *Store) lockImportJobForActor(tx sqlx.ExtContext, jobID, actorID string) (*model.ImportJob, error) {
	var job model.ImportJob
	builder := s.importJobSelectQuery().Where(sq.Eq{"Id": jobID}).Suffix("FOR UPDATE")
	if err := s.getBuilder(tx, &job, builder); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, &ErrNotFound{EntityName: "ImportJob", ID: jobID}
		}
		return nil, errors.Wrap(err, "unable_to_get_import_job")
	}
	if actorID != "" && job.ActorId != actorID {
		// Report as absent rather than forbidden, matching read visibility.
		return nil, &ErrNotFound{EntityName: "ImportJob", ID: jobID}
	}
	return &job, nil
}

// --- worker work selection ---

// importWorkPriority is the order the worker resumes states in, and it contains *only* states this release
// can actually advance.
//
// That restriction is load-bearing. Selection returns the first non-empty state in this order, so a state
// the worker cannot advance would be selected on every pass and starve everything below it — one confirmed
// job would wedge the importer permanently, since queued_import is not expirable either. Execution states
// join this list in the same change that implements them; until then they are unreachable by construction
// (nothing transitions into importing) or reclaimed by cancellation and expiry.
//
// Within the list, active states come before newly queued ones so a job interrupted mid-flight is finished
// before new work starts: it holds staged input and capacity that stay in limbo until it completes.
// Terminalizing is first because it is the only state that releases those holds.
var importWorkPriority = []model.ImportJobState{
	model.ImportStateTerminalizing,
	model.ImportStatePreflighting,
	model.ImportStateQueuedPreflight,
}

// importActiveStates are every worker-owned state, including those importWorkPriority deliberately omits.
// The invariant check counts these rather than the selectable ones, so a job parked in an unimplemented
// state is still visible as active work.
var importActiveStates = []model.ImportJobState{
	model.ImportStateTerminalizing,
	model.ImportStateImporting,
	model.ImportStatePreflighting,
	model.ImportStateQueuedImport,
	model.ImportStateQueuedPreflight,
}

// GetNextImportWork returns the single job the worker should act on next, or nil when there is none.
//
// V1 deliberately has no leases, claim tokens, heartbeats, or FOR UPDATE SKIP LOCKED: there is exactly
// one worker on one node, so selection is an ordered read and every transition is a compare-and-set
// against the state this read observed. A stale read therefore loses its CAS rather than duplicating work.
func (s *Store) GetNextImportWork() (*model.ImportJob, error) {
	for _, state := range importWorkPriority {
		builder := s.importJobSelectQuery().
			Where(sq.Eq{"State": string(state)}).
			OrderBy("CreateAt ASC", "Id ASC")
		builder = applyLimitOffset(builder, 0, 1)

		jobs := []*model.ImportJob{}
		if err := s.selectBuilder(s.db, &jobs, builder); err != nil {
			return nil, errors.Wrap(err, "unable_to_select_next_import_work")
		}
		if len(jobs) > 0 {
			return jobs[0], nil
		}
	}
	return nil, nil
}

// CountActiveImportJobs returns how many jobs occupy a worker-owned state. The supported topology has at
// most one; more means either an unsupported multi-node deployment or a bug, and the caller logs it as an
// invariant violation rather than silently processing them.
func (s *Store) CountActiveImportJobs() (int, error) {
	active := make([]string, 0, len(importActiveStates))
	for _, state := range importActiveStates {
		if state.IsWorkerOwned() {
			active = append(active, string(state))
		}
	}
	var count int
	builder := s.getQueryBuilder().
		Select("COUNT(*)").
		From("DOCS_ImportJob").
		Where(sq.Eq{"State": active})
	if err := s.getBuilder(s.db, &count, builder); err != nil {
		return 0, errors.Wrap(err, "unable_to_count_active_import_jobs")
	}
	return count, nil
}

// --- preflight inputs ---

// BeginImportPreflight moves a queued job into preflighting and returns it together with the mapping
// revision the computation must be performed against.
//
// The revision is read here and rechecked in the publication transaction. That pairing — not a lock held
// across the whole computation — is what keeps a long preflight from blocking other work while still
// refusing to publish a result computed against mappings that have since changed. A source that does not
// exist yet has revision zero, which is stable by construction: nothing else can change a source that
// has not been created.
func (s *Store) BeginImportPreflight(jobID string) (_ *model.ImportJob, _ int64, err error) {
	tx, err := s.db.Beginx()
	if err != nil {
		return nil, 0, errors.Wrap(err, "begin_transaction")
	}
	defer s.finalizeTransaction(tx, &err)

	job, err := s.lockImportJobForActor(tx, jobID, "")
	if err != nil {
		return nil, 0, err
	}
	if job.State != model.ImportStateQueuedPreflight {
		return nil, 0, &ErrConflict{Resource: "ImportJob state=" + string(job.State)}
	}

	revision, err := s.readImportSourceRevision(tx, job, false)
	if err != nil {
		return nil, 0, err
	}

	now := mmmodel.GetMillis()
	updateBuilder := s.getQueryBuilder().
		Update("DOCS_ImportJob").
		Set("State", string(model.ImportStatePreflighting)).
		Set("Phase", string(model.ImportPhaseComputingActions)).
		Set("ProgressCurrent", 0).
		Set("UpdateAt", monotonicBump("UpdateAt", now)).
		Where(sq.Eq{"Id": jobID, "State": string(model.ImportStateQueuedPreflight)})
	result, err := s.execBuilder(tx, updateBuilder)
	if err != nil {
		return nil, 0, errors.Wrap(err, "unable_to_begin_import_preflight")
	}
	if err = checkRowsAffected(result, "ImportJob", jobID); err != nil {
		return nil, 0, err
	}
	if err = tx.Commit(); err != nil {
		return nil, 0, errors.Wrap(err, "commit_transaction")
	}

	job.State = model.ImportStatePreflighting
	job.Phase = model.ImportPhaseComputingActions
	job.ProgressCurrent = 0
	job.UpdateAt = max(job.UpdateAt+1, now)
	return job, revision, nil
}

// readImportSourceRevision returns the selected source's current MappingRevision, locking the row when
// forUpdate is set. A job whose source does not exist yet reports revision zero. Must be called inside tx.
func (s *Store) readImportSourceRevision(tx sqlx.ExtContext, job *model.ImportJob, forUpdate bool) (int64, error) {
	if job.SourceSelectionMode != model.ImportSourceModeExisting || job.SelectedImportSourceId == "" {
		return 0, nil
	}
	builder := s.getQueryBuilder().
		Select("MappingRevision").
		From("DOCS_ImportSource").
		Where(sq.Eq{"Id": job.SelectedImportSourceId})
	if forUpdate {
		builder = builder.Suffix("FOR UPDATE")
	}
	var revision int64
	if err := s.getBuilder(tx, &revision, builder); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// A selected source that has vanished is a hard failure rather than "revision zero": zero would
			// silently reclassify every mapped page as a create.
			return 0, &ErrImportSourceMissing{JobID: job.Id, SourceID: job.SelectedImportSourceId}
		}
		return 0, errors.Wrap(err, "unable_to_read_import_source_revision")
	}
	return revision, nil
}

// GetImportSource returns one ImportSource by id.
func (s *Store) GetImportSource(sourceID string) (*model.ImportSource, error) {
	if sourceID == "" {
		return nil, &ErrInvalidInput{Entity: "ImportSource", Field: "id", Value: sourceID}
	}
	var source model.ImportSource
	builder := s.getQueryBuilder().Select(importSourceColumns...).From("DOCS_ImportSource").Where(sq.Eq{"Id": sourceID})
	if err := s.getBuilder(s.db, &source, builder); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, &ErrNotFound{EntityName: "ImportSource", ID: sourceID}
		}
		return nil, errors.Wrap(err, "unable_to_get_import_source")
	}
	return &source, nil
}

// GetImportEntitiesForSource bulk-loads a source's page mappings, newest-created last. The result is
// bounded by model.ImportMaxMappingsPerSource + 1: the extra row lets the caller detect a source that has
// somehow exceeded the retained-mapping cap instead of silently working with a truncated set.
func (s *Store) GetImportEntitiesForSource(sourceID string) ([]*model.ImportEntity, error) {
	if sourceID == "" {
		return nil, &ErrInvalidInput{Entity: "ImportEntity", Field: "importSourceID", Value: sourceID}
	}
	builder := s.getQueryBuilder().
		Select(importEntityColumns...).
		From("DOCS_ImportEntity").
		Where(sq.Eq{"ImportSourceId": sourceID, "EntityType": model.ImportEntityTypePage}).
		OrderBy("CreateAt ASC", "ExternalId ASC")
	builder = applyLimitOffset(builder, 0, model.ImportMaxMappingsPerSource+1)

	entities := []*model.ImportEntity{}
	if err := s.selectBuilder(s.db, &entities, builder); err != nil {
		return nil, errors.Wrap(err, "unable_to_get_import_entities")
	}
	return entities, nil
}

// GetImportStagedPages returns one page of a job's staged pages in ordinal order. Preflight walks them in
// batches so a five-thousand-page bundle is never fully resident, and ordinal order means a page's source
// parent has always been seen before the page itself (the producer emits parents first).
func (s *Store) GetImportStagedPages(jobID string, offset, limit int) ([]*model.ImportStagedPage, error) {
	if jobID == "" {
		return nil, &ErrInvalidInput{Entity: "ImportStagedPage", Field: "jobID", Value: jobID}
	}
	if err := requirePositiveLimit("ImportStagedPage", limit); err != nil {
		return nil, err
	}
	builder := s.getQueryBuilder().
		Select(importStagedPageColumns...).
		From("DOCS_ImportStagedPage").
		Where(sq.Eq{"JobId": jobID}).
		OrderBy("Ordinal ASC")
	builder = applyLimitOffset(builder, offset, limit)

	pages := []*model.ImportStagedPage{}
	if err := s.selectBuilder(s.db, &pages, builder); err != nil {
		return nil, errors.Wrap(err, "unable_to_get_import_staged_pages")
	}
	return pages, nil
}

// ImportLocalPage is the current local state preflight compares a mapped page against. It carries only
// what classification and capacity checks need, so a five-thousand-mapping source does not pull five
// thousand page bodies into memory beyond the body itself, which the applied-content hash requires.
type ImportLocalPage struct {
	Id       string
	SpaceId  string
	ParentId string
	Title    string
	Body     string
	Props    mmmodel.StringInterface
	UpdateAt int64
	DeleteAt int64
}

// GetImportLocalPages bulk-loads the current state of the given page ids, including soft-deleted ones so
// a mapping pointing at a deleted page can be classified as blocked rather than as missing. Snapshot rows
// (OriginalId != ”) are excluded: they are history, not the live page a mapping refers to.
func (s *Store) GetImportLocalPages(pageIDs []string) (map[string]*ImportLocalPage, error) {
	out := make(map[string]*ImportLocalPage, len(pageIDs))
	if len(pageIDs) == 0 {
		return out, nil
	}
	for chunk := range slicesChunked(pageIDs, importIDChunkSize) {
		builder := s.getQueryBuilder().
			Select("Id", "SpaceId", "COALESCE(ParentId, '') AS ParentId", "Title", "Body", "Props", "UpdateAt", "DeleteAt").
			From("DOCS_Page").
			Where(sq.Eq{"Id": chunk, "OriginalId": ""})

		rows := []*ImportLocalPage{}
		if err := s.selectBuilder(s.db, &rows, builder); err != nil {
			return nil, errors.Wrap(err, "unable_to_get_import_local_pages")
		}
		for _, row := range rows {
			out[row.Id] = row
		}
	}
	return out, nil
}

// CountLivePageChildren returns the number of live direct children each given parent has in a Space. The
// empty string counts Space roots. Preflight uses it to project whether a planned create would push a
// sibling group past MaxPageSiblingsLimit; execution rechecks under locks, because an interactive edit can
// consume the remaining room between review and execution.
func (s *Store) CountLivePageChildren(spaceID string, parentIDs []string) (map[string]int, error) {
	out := make(map[string]int, len(parentIDs))
	if spaceID == "" || len(parentIDs) == 0 {
		return out, nil
	}
	for chunk := range slicesChunked(parentIDs, importIDChunkSize) {
		// ParentId is nullable for roots, so the group key is normalized to '' on both sides of the
		// comparison rather than relying on a NULL match, which would never be equal.
		builder := s.getQueryBuilder().
			Select("COALESCE(ParentId, '') AS ParentKey", "COUNT(*) AS ChildCount").
			From("DOCS_Page").
			Where(sq.Eq{"SpaceId": spaceID, "DeleteAt": 0, "OriginalId": ""}).
			Where(sq.Eq{"COALESCE(ParentId, '')": chunk}).
			GroupBy("COALESCE(ParentId, '')")

		var rows []struct {
			ParentKey  string
			ChildCount int
		}
		if err := s.selectBuilder(s.db, &rows, builder); err != nil {
			return nil, errors.Wrap(err, "unable_to_count_live_page_children")
		}
		for _, row := range rows {
			out[row.ParentKey] = row.ChildCount
		}
	}
	return out, nil
}

// GetLivePageDepths returns each given page's depth in its hierarchy, with a Space root at depth 1.
// Preflight needs it to project whether a planned create would breach model.MaxPageDepth beneath an
// existing local parent.
func (s *Store) GetLivePageDepths(pageIDs []string) (map[string]int, error) {
	out := make(map[string]int, len(pageIDs))
	if len(pageIDs) == 0 {
		return out, nil
	}
	// One recursive walk per page, matching pageDepth's shape. The count is bounded by the number of
	// distinct local parents in a bundle rather than by its page count, and squirrel cannot express
	// WITH RECURSIVE — which is how every other hierarchy read in this package is built too.
	query := moveAncestorsCTE + `
	SELECT COALESCE(MAX(depth), 0) FROM ancestors`
	for _, pageID := range pageIDs {
		var depth int
		if err := s.get(s.db, &depth, query, pageID); err != nil {
			return nil, errors.Wrap(err, "unable_to_get_live_page_depth")
		}
		// Depth 0 means the page is not a live, non-snapshot row; leave it absent so a caller cannot
		// mistake "no such page" for "a root page".
		if depth > 0 {
			out[pageID] = depth
		}
	}
	return out, nil
}

// importIDChunkSize bounds how many ids one IN clause carries, keeping bind-parameter counts well below
// PostgreSQL's 65535 limit even for a full five-thousand-page bundle.
const importIDChunkSize = 500

// slicesChunked yields successive chunks of at most size elements.
func slicesChunked[T any](items []T, size int) func(func([]T) bool) {
	return func(yield func([]T) bool) {
		for start := 0; start < len(items); start += size {
			end := min(start+size, len(items))
			if !yield(items[start:end]) {
				return
			}
		}
	}
}

// --- preflight publication ---

// ImportStagedPagePlan is the per-page preflight decision persisted back onto the staged row. Every field
// is a reviewed baseline: execution rechecks them under locks and refuses to apply anything whose baseline
// has moved, which is what makes a reviewed preflight safe to act on later.
type ImportStagedPagePlan struct {
	Ordinal                     int
	PlannedAction               model.ImportAction
	PlannedPageId               string
	ResolvedUserId              string
	AuthorFallbackReason        string
	IncomingSourceContentHash   string
	PreflightCurrentContentHash string
	PreflightMappingContentHash string
	PreflightCurrentParentId    string
	PreflightMappingParentId    string
	PreflightMappingUpdateAt    int64
}

// ImportPreflightPublication is the complete, deterministic output of one preflight computation. It is
// published all-or-nothing: a partially applied preflight would leave some pages carrying baselines from
// this run and others from the previous one, and nothing downstream could tell which.
type ImportPreflightPublication struct {
	JobID string
	// MappingRevision is the revision the computation was performed against. Publication refuses if the
	// source has moved since.
	MappingRevision int64
	Plans           []ImportStagedPagePlan
	Results         []*model.ImportResultRecord
	Issues          []*model.ImportIssueRecord
	Summary         model.ImportPreflightSummary
	Revision        string
}

// PublishImportPreflight persists a complete preflight and transitions the job to awaiting_confirmation.
//
// It replaces any prior preflight-stage derived set for the job, so a recomputation after invalidation
// leaves no rows from the discarded run. Execution-stage rows are untouched: they are immutable
// checkpoints and a preflight never has any.
func (s *Store) PublishImportPreflight(pub *ImportPreflightPublication) (_ *model.ImportJob, err error) {
	if pub == nil || pub.JobID == "" {
		return nil, &ErrInvalidInput{Entity: "ImportPreflight", Field: "publication", Value: nil}
	}
	if !model.IsValidImportHash(pub.Revision) || pub.Revision == "" {
		return nil, &ErrInvalidInput{Entity: "ImportPreflight", Field: "revision", Value: pub.Revision}
	}

	tx, err := s.db.Beginx()
	if err != nil {
		return nil, errors.Wrap(err, "begin_transaction")
	}
	defer s.finalizeTransaction(tx, &err)

	job, err := s.lockImportJobForActor(tx, pub.JobID, "")
	if err != nil {
		return nil, err
	}
	if job.State != model.ImportStatePreflighting {
		return nil, &ErrConflict{Resource: "ImportJob state=" + string(job.State)}
	}
	if job.TerminalIntent != model.ImportIntentNone {
		return nil, &ErrConflict{Resource: "ImportJob terminal_intent=" + string(job.TerminalIntent)}
	}

	// Lock the source and require the revision the computation used. A mismatch means another job changed
	// these mappings while this preflight ran, so its classifications may already be wrong.
	current, err := s.readImportSourceRevision(tx, job, true)
	if err != nil {
		return nil, err
	}
	if current != pub.MappingRevision {
		return nil, &ErrPreflightStale{JobID: pub.JobID}
	}

	if err = s.deleteImportPreflightRows(tx, pub.JobID); err != nil {
		return nil, err
	}
	if err = s.insertImportResults(tx, pub.Results); err != nil {
		return nil, err
	}
	if err = s.insertImportIssues(tx, pub.Issues); err != nil {
		return nil, err
	}
	if err = s.applyImportStagedPlans(tx, pub.JobID, pub.Plans); err != nil {
		return nil, err
	}

	// Charge what this plan retains, replacing the previous plan's charge rather than adding to it: a
	// recompute deletes those rows, so accumulating would over-count and ignoring them would leave rows the
	// accounting never knew about — which is what lets repeated preflight/cancel cycles retain storage for
	// ninety days against a figure that barely moves.
	charge, err := measureImportPreflightCharge(pub)
	if err != nil {
		return nil, err
	}
	job.RetainedBytes += charge.total - job.PreflightRetainedBytes
	job.RetainedIssueBytes += charge.issues - job.PreflightRetainedIssueBytes
	job.PreflightRetainedBytes = charge.total
	job.PreflightRetainedIssueBytes = charge.issues

	now := mmmodel.GetMillis()
	updateBuilder := s.getQueryBuilder().
		Update("DOCS_ImportJob").
		Set("State", string(model.ImportStateAwaitingConfirmation)).
		Set("Phase", string(model.ImportPhaseAwaitingConfirmation)).
		Set("PreflightSummary", pub.Summary).
		Set("PreflightRevision", pub.Revision).
		Set("PreflightMappingRevision", pub.MappingRevision).
		Set("MappingInputsChanged", false).
		Set("ProgressCurrent", int64(len(pub.Plans))).
		Set("RetainedBytes", job.RetainedBytes).
		Set("RetainedIssueBytes", job.RetainedIssueBytes).
		Set("PreflightRetainedBytes", job.PreflightRetainedBytes).
		Set("PreflightRetainedIssueBytes", job.PreflightRetainedIssueBytes).
		Set("UpdateAt", monotonicBump("UpdateAt", now)).
		Where(sq.Eq{"Id": pub.JobID, "State": string(model.ImportStatePreflighting)})
	result, err := s.execBuilder(tx, updateBuilder)
	if err != nil {
		return nil, errors.Wrap(err, "unable_to_publish_import_preflight")
	}
	if err = checkRowsAffected(result, "ImportJob", pub.JobID); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, errors.Wrap(err, "commit_transaction")
	}

	job.State = model.ImportStateAwaitingConfirmation
	job.Phase = model.ImportPhaseAwaitingConfirmation
	job.PreflightSummary = pub.Summary
	job.PreflightRevision = pub.Revision
	job.PreflightMappingRevision = pub.MappingRevision
	job.MappingInputsChanged = false
	job.ProgressCurrent = int64(len(pub.Plans))
	job.UpdateAt = max(job.UpdateAt+1, now)
	return job, nil
}

// importPreflightCharge is what one published preflight retains, split by budget pool.
type importPreflightCharge struct {
	total  int64
	issues int64
}

// measureImportPreflightCharge measures a publication's retained cost: its result rows, its issue rows, and the
// summary it persists. Measuring rather than estimating matters here for the same reason it does at upload —
// issue text spans orders of magnitude, so a flat per-row figure would be wrong in both directions.
func measureImportPreflightCharge(pub *ImportPreflightPublication) (importPreflightCharge, error) {
	var charge importPreflightCharge
	for _, r := range pub.Results {
		detailsBytes, err := jsonByteLen(jsonbMap(r.Details))
		if err != nil {
			return charge, err
		}
		charge.total += retainedResultRowBytes(r, detailsBytes)
	}
	for _, i := range pub.Issues {
		detailsBytes, err := jsonByteLen(jsonbMap(i.Details))
		if err != nil {
			return charge, err
		}
		charge.issues += retainedIssueRowBytes(i, detailsBytes)
	}
	summaryBytes, err := summaryByteLen(pub.Summary)
	if err != nil {
		return charge, err
	}
	charge.total += charge.issues + summaryBytes
	return charge, nil
}

// deleteImportPreflightRows removes a job's prior preflight-stage results and issues. Inspection and
// execution rows are deliberately left alone: inspection findings describe the bundle rather than the
// plan, and execution results are immutable checkpoints. Must be called inside tx.
func (s *Store) deleteImportPreflightRows(tx sqlx.ExtContext, jobID string) error {
	for _, table := range []string{"DOCS_ImportResult", "DOCS_ImportIssue"} {
		builder := s.getQueryBuilder().
			Delete(table).
			Where(sq.Eq{"JobId": jobID, "Stage": string(model.ImportStagePreflight)})
		if _, err := s.execBuilder(tx, builder); err != nil {
			return errors.Wrap(err, "unable_to_delete_import_preflight_rows")
		}
	}
	return nil
}

// insertImportResults writes result rows in bounded batches. Must be called inside tx.
func (s *Store) insertImportResults(tx sqlx.ExtContext, records []*model.ImportResultRecord) error {
	for chunk := range slicesChunked(records, importRowBatchRows) {
		batch := s.getQueryBuilder().Insert("DOCS_ImportResult").Columns(importResultColumns...)
		for _, r := range chunk {
			if validErr := r.IsValid(); validErr != nil {
				return &ErrInvalidInput{Entity: "ImportResult", Field: "IsValid", Value: validErr.Error(), Reason: validErr.Id}
			}
			batch = batch.Values(
				r.JobId, string(r.Stage), r.Ordinal, r.EntityType, r.ExternalId, r.LocalId, r.Title,
				string(r.PlannedAction), string(r.ActualAction), string(r.Outcome),
				jsonbMap(r.Details), r.CreateAt, r.UpdateAt,
			)
		}
		if _, err := s.execBuilder(tx, batch); err != nil {
			if isUniqueViolation(err) {
				return &ErrConflict{Resource: "ImportResult"}
			}
			return errors.Wrap(err, "unable_to_save_import_results")
		}
	}
	return nil
}

// insertImportIssues writes issue rows in bounded batches. Must be called inside tx.
func (s *Store) insertImportIssues(tx sqlx.ExtContext, records []*model.ImportIssueRecord) error {
	for chunk := range slicesChunked(records, importRowBatchRows) {
		batch := s.getQueryBuilder().Insert("DOCS_ImportIssue").Columns(importIssueColumns...)
		for _, i := range chunk {
			if validErr := i.IsValid(); validErr != nil {
				return &ErrInvalidInput{Entity: "ImportIssue", Field: "IsValid", Value: validErr.Error(), Reason: validErr.Id}
			}
			batch = batch.Values(
				i.JobId, string(i.Stage), i.Ordinal, string(i.Severity), i.Code,
				i.EntityType, i.ExternalId, i.LocalId, i.Title, i.Message, i.Remediation, jsonbMap(i.Details),
			)
		}
		if _, err := s.execBuilder(tx, batch); err != nil {
			if isUniqueViolation(err) {
				return &ErrConflict{Resource: "ImportIssue"}
			}
			return errors.Wrap(err, "unable_to_save_import_issues")
		}
	}
	return nil
}

// applyImportStagedPlans writes each page's reviewed plan back onto its staged row.
//
// One UPDATE ... FROM (VALUES ...) per batch rather than one statement per page: a five-thousand-page
// bundle would otherwise hold the job-row lock across five thousand round trips. squirrel cannot express
// that form, so the statement is built directly; every value is bound as a parameter and the ordinals come
// from the job's own staged rows rather than from any request. Must be called inside tx.
func (s *Store) applyImportStagedPlans(tx sqlx.ExtContext, jobID string, plans []ImportStagedPagePlan) error {
	for chunk := range slicesChunked(plans, importRowBatchRows) {
		var b strings.Builder
		b.WriteString(`UPDATE DOCS_ImportStagedPage SET
			PlannedAction = v.planned_action,
			PlannedPageId = v.planned_page_id,
			ResolvedUserId = v.resolved_user_id,
			AuthorFallbackReason = v.author_fallback_reason,
			IncomingSourceContentHash = v.incoming_source_content_hash,
			PreflightCurrentContentHash = v.preflight_current_content_hash,
			PreflightMappingContentHash = v.preflight_mapping_content_hash,
			PreflightCurrentParentId = v.preflight_current_parent_id,
			PreflightMappingParentId = v.preflight_mapping_parent_id,
			PreflightMappingUpdateAt = v.preflight_mapping_update_at
		FROM (VALUES `)
		args := make([]any, 0, len(chunk)*11)
		for i, p := range chunk {
			if i > 0 {
				b.WriteString(", ")
			}
			if i == 0 {
				// Cast the first row so the VALUES column types are unambiguous; later rows inherit them.
				b.WriteString("(?::integer, ?::varchar, ?::varchar, ?::varchar, ?::varchar, ?::varchar, ?::varchar, ?::varchar, ?::varchar, ?::varchar, ?::bigint)")
			} else {
				b.WriteString("(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)")
			}
			args = append(args, p.Ordinal, string(p.PlannedAction), p.PlannedPageId, p.ResolvedUserId,
				p.AuthorFallbackReason, p.IncomingSourceContentHash, p.PreflightCurrentContentHash,
				p.PreflightMappingContentHash, p.PreflightCurrentParentId, p.PreflightMappingParentId,
				p.PreflightMappingUpdateAt)
		}
		b.WriteString(`) AS v(ordinal, planned_action, planned_page_id, resolved_user_id, author_fallback_reason,
			incoming_source_content_hash, preflight_current_content_hash, preflight_mapping_content_hash,
			preflight_current_parent_id, preflight_mapping_parent_id, preflight_mapping_update_at)
		WHERE DOCS_ImportStagedPage.JobId = ? AND DOCS_ImportStagedPage.Ordinal = v.ordinal`)
		args = append(args, jobID)

		if _, err := s.exec(tx, b.String(), args...); err != nil {
			return errors.Wrap(err, "unable_to_apply_import_staged_plans")
		}
	}
	return nil
}

// GetImportPreflightResults returns one page of a job's preflight results in ordinal order, for the
// review projection. limit is expected to be perPage+1 so the caller derives has-more from a probe row.
func (s *Store) GetImportPreflightResults(jobID string, offset, limit int) ([]*model.ImportResultRecord, error) {
	if jobID == "" {
		return nil, &ErrInvalidInput{Entity: "ImportResult", Field: "jobID", Value: jobID}
	}
	if err := requirePositiveLimit("ImportResult", limit); err != nil {
		return nil, err
	}
	builder := s.getQueryBuilder().
		Select(importResultColumns...).
		From("DOCS_ImportResult").
		Where(sq.Eq{"JobId": jobID, "Stage": string(model.ImportStagePreflight)}).
		OrderBy("Ordinal ASC")
	builder = applyLimitOffset(builder, offset, limit)

	results := []*model.ImportResultRecord{}
	if err := s.selectBuilder(s.db, &results, builder); err != nil {
		return nil, errors.Wrap(err, "unable_to_get_import_preflight_results")
	}
	return results, nil
}

// GetImportConflictExternalIDs returns the external IDs of a job's preflight conflicts, which is the exact
// set confirmation may approve for overwrite. Reading it from persisted results rather than trusting the
// request is what keeps an approval from applying to a page the user never reviewed as a conflict.
func (s *Store) GetImportConflictExternalIDs(jobID string) (map[string]struct{}, error) {
	if jobID == "" {
		return nil, &ErrInvalidInput{Entity: "ImportResult", Field: "jobID", Value: jobID}
	}
	builder := s.getQueryBuilder().
		Select("ExternalId").
		From("DOCS_ImportResult").
		Where(sq.Eq{
			"JobId":         jobID,
			"Stage":         string(model.ImportStagePreflight),
			"PlannedAction": string(model.ImportActionConflict),
		})
	builder = applyLimitOffset(builder, 0, model.ImportMaxPages)

	ids := []string{}
	if err := s.selectBuilder(s.db, &ids, builder); err != nil {
		return nil, errors.Wrap(err, "unable_to_get_import_conflict_ids")
	}
	out := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		out[id] = struct{}{}
	}
	return out, nil
}

// CountImportPlannedActions returns how many preflight results carry each planned action, which is what
// decides the required acknowledgements without re-reading every row.
func (s *Store) CountImportPlannedActions(jobID string) (map[model.ImportAction]int, error) {
	if jobID == "" {
		return nil, &ErrInvalidInput{Entity: "ImportResult", Field: "jobID", Value: jobID}
	}
	builder := s.getQueryBuilder().
		Select("PlannedAction", "COUNT(*) AS ActionCount").
		From("DOCS_ImportResult").
		Where(sq.Eq{"JobId": jobID, "Stage": string(model.ImportStagePreflight)}).
		GroupBy("PlannedAction")

	var rows []struct {
		PlannedAction string
		ActionCount   int
	}
	if err := s.selectBuilder(s.db, &rows, builder); err != nil {
		return nil, errors.Wrap(err, "unable_to_count_import_planned_actions")
	}
	out := make(map[model.ImportAction]int, len(rows))
	for _, row := range rows {
		out[model.ImportAction(row.PlannedAction)] = row.ActionCount
	}
	return out, nil
}

// --- confirmation ---

// ConfirmImportJob records the user's confirmation and queues the job for import.
//
// The mapping revision is rechecked under the source lock: a preflight reviewed against mappings another
// job has since changed must not be acted on. On mismatch the confirmation-specific fields are cleared and
// the job returns to queued_preflight in the same transaction, and ErrPreflightStale is returned — so the
// client is never left holding a revision that can no longer be confirmed.
func (s *Store) ConfirmImportJob(jobID, actorID string, confirmation model.ImportConfirmation) (_ *model.ImportJob, err error) {
	if jobID == "" {
		return nil, &ErrInvalidInput{Entity: "ImportJob", Field: "id", Value: jobID}
	}
	if validErr := confirmation.IsValid(); validErr != nil {
		return nil, &ErrInvalidInput{Entity: "ImportConfirmation", Field: "IsValid", Value: validErr.Error(), Reason: validErr.Id}
	}

	tx, err := s.db.Beginx()
	if err != nil {
		return nil, errors.Wrap(err, "begin_transaction")
	}
	defer s.finalizeTransaction(tx, &err)

	job, err := s.lockImportJobForActor(tx, jobID, actorID)
	if err != nil {
		return nil, err
	}
	if job.State != model.ImportStateAwaitingConfirmation {
		return nil, &ErrConflict{Resource: "ImportJob state=" + string(job.State)}
	}
	if job.PreflightRevision != confirmation.PreflightRevision {
		// The reviewed plan is not the current one. This is a precondition failure rather than staleness:
		// the job still has a confirmable revision, just not the one the client sent.
		return nil, &ErrConflict{Resource: "ImportJob preflight_revision"}
	}

	now := mmmodel.GetMillis()
	current, err := s.readImportSourceRevision(tx, job, true)
	if err != nil {
		return nil, err
	}
	if current != job.PreflightMappingRevision {
		if err = s.resetImportToPreflight(tx, job, now); err != nil {
			return nil, err
		}
		if err = tx.Commit(); err != nil {
			return nil, errors.Wrap(err, "commit_transaction")
		}
		return nil, &ErrPreflightStale{JobID: jobID}
	}

	updateBuilder := s.getQueryBuilder().
		Update("DOCS_ImportJob").
		Set("State", string(model.ImportStateQueuedImport)).
		Set("Phase", string(model.ImportPhaseQueuedImport)).
		Set("Confirmation", confirmation).
		Set("ConfirmedAt", now).
		Set("UpdateAt", monotonicBump("UpdateAt", now)).
		Where(sq.Eq{"Id": jobID, "State": string(model.ImportStateAwaitingConfirmation)})
	if confirmation.NewSpace != nil {
		updateBuilder = updateBuilder.
			Set("ConfirmedSpaceTitle", confirmation.NewSpace.Title).
			Set("ConfirmedSpaceDescription", confirmation.NewSpace.Description)
	}
	result, err := s.execBuilder(tx, updateBuilder)
	if err != nil {
		return nil, errors.Wrap(err, "unable_to_confirm_import_job")
	}
	if err = checkRowsAffected(result, "ImportJob", jobID); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, errors.Wrap(err, "commit_transaction")
	}

	job.State = model.ImportStateQueuedImport
	job.Phase = model.ImportPhaseQueuedImport
	job.Confirmation = confirmation
	job.ConfirmedAt = now
	job.UpdateAt = max(job.UpdateAt+1, now)
	if confirmation.NewSpace != nil {
		job.ConfirmedSpaceTitle = confirmation.NewSpace.Title
		job.ConfirmedSpaceDescription = confirmation.NewSpace.Description
	}
	return job, nil
}

// resetImportToPreflight clears everything derived from a now-invalid preflight and returns the job to the
// preflight queue. The confirmation is cleared with it: a confirmation names a specific revision, so
// keeping it would let a later transition act on an approval the user gave for a different plan. Must be
// called inside tx.
func (s *Store) resetImportToPreflight(tx sqlx.ExtContext, job *model.ImportJob, now int64) error {
	if err := s.deleteImportPreflightRows(tx, job.Id); err != nil {
		return err
	}
	// The deleted rows are no longer retained, so their charge is returned. Leaving it would make every
	// discarded plan permanently inflate the job's usage.
	builder := s.getQueryBuilder().
		Update("DOCS_ImportJob").
		Set("State", string(model.ImportStateQueuedPreflight)).
		Set("Phase", string(model.ImportPhaseComputingActions)).
		Set("RetainedBytes", sq.Expr("GREATEST(RetainedBytes - PreflightRetainedBytes, 0)")).
		Set("RetainedIssueBytes", sq.Expr("GREATEST(RetainedIssueBytes - PreflightRetainedIssueBytes, 0)")).
		Set("PreflightRetainedBytes", 0).
		Set("PreflightRetainedIssueBytes", 0).
		Set("Confirmation", model.ImportConfirmation{}).
		Set("PreflightSummary", model.ImportPreflightSummary{}).
		Set("PreflightRevision", "").
		Set("PreflightMappingRevision", 0).
		Set("ConfirmedAt", 0).
		Set("MappingInputsChanged", true).
		Set("ProgressCurrent", 0).
		Set("UpdateAt", monotonicBump("UpdateAt", now)).
		Where(sq.Eq{"Id": job.Id})
	if _, err := s.execBuilder(tx, builder); err != nil {
		return errors.Wrap(err, "unable_to_reset_import_to_preflight")
	}
	return nil
}

// --- terminalization entry ---

// EnterImportTerminalizing records a terminal intent and moves the job into terminalizing from any state
// it currently occupies.
//
// Terminalization is worker work rather than a direct jump to a terminal state: durable outcomes and the
// final report must be written first, and doing that in the worker means a crash mid-terminalization
// resumes deterministically instead of leaving a job terminal with an empty report.
func (s *Store) EnterImportTerminalizing(jobID string, intent model.ImportTerminalIntent, errorCode string) (_ *model.ImportJob, err error) {
	if !intent.IsValid() || intent == model.ImportIntentNone {
		return nil, &ErrInvalidInput{Entity: "ImportJob", Field: "terminalIntent", Value: string(intent)}
	}

	tx, err := s.db.Beginx()
	if err != nil {
		return nil, errors.Wrap(err, "begin_transaction")
	}
	defer s.finalizeTransaction(tx, &err)

	job, err := s.lockImportJobForActor(tx, jobID, "")
	if err != nil {
		return nil, err
	}
	if job.State.IsTerminal() || job.State == model.ImportStateTerminalizing {
		return nil, &ErrConflict{Resource: "ImportJob state=" + string(job.State)}
	}

	now := mmmodel.GetMillis()
	builder := s.getQueryBuilder().
		Update("DOCS_ImportJob").
		Set("State", string(model.ImportStateTerminalizing)).
		Set("TerminalIntent", string(intent)).
		Set("ErrorCode", errorCode).
		Set("UpdateAt", monotonicBump("UpdateAt", now)).
		Where(sq.Eq{"Id": jobID, "State": string(job.State)})
	result, err := s.execBuilder(tx, builder)
	if err != nil {
		return nil, errors.Wrap(err, "unable_to_enter_import_terminalizing")
	}
	if err = checkRowsAffected(result, "ImportJob", jobID); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, errors.Wrap(err, "commit_transaction")
	}

	job.State = model.ImportStateTerminalizing
	job.TerminalIntent = intent
	job.ErrorCode = errorCode
	job.UpdateAt = max(job.UpdateAt+1, now)
	return job, nil
}

// RequeueImportPreflight returns a job that was computing preflight to the preflight queue, discarding
// whatever it had derived.
//
// This is the recovery path for a computation whose inputs moved: nothing was published, so there is
// nothing to keep, and the next worker pass recomputes from the current mappings. It is a no-op conflict
// if the job has since been canceled or advanced, which is why the transition is a compare-and-set on the
// state the caller observed rather than an unconditional write.
func (s *Store) RequeueImportPreflight(jobID string) (err error) {
	tx, err := s.db.Beginx()
	if err != nil {
		return errors.Wrap(err, "begin_transaction")
	}
	defer s.finalizeTransaction(tx, &err)

	job, err := s.lockImportJobForActor(tx, jobID, "")
	if err != nil {
		return err
	}
	if job.State != model.ImportStatePreflighting {
		return &ErrConflict{Resource: "ImportJob state=" + string(job.State)}
	}
	if err = s.resetImportToPreflight(tx, job, mmmodel.GetMillis()); err != nil {
		return err
	}
	return tx.Commit()
}
