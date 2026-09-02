// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// The wire shapes of the Confluence import API, mirroring server/model/import.go and
// server/model/import_report.go.
//
// These describe what the server actually sends, not what the wizard would find convenient. The
// server deliberately withholds a great deal — content hashes, page bodies, mapping baselines — so a
// field's absence here is usually a decision rather than an omission: approval carries intent while
// the baselines that make applying it safe stay server-side.

// ImportJobState is the job lifecycle. A client should treat this as the authoritative source of
// "what can I do now", rather than inferring it from which fields are populated.
export type ImportJobState =
    | 'awaiting_source'
    | 'queued_preflight'
    | 'preflighting'
    | 'awaiting_confirmation'
    | 'queued_import'
    | 'importing'
    | 'terminalizing'
    | 'completed'
    | 'completed_with_issues'
    | 'failed'
    | 'canceled';

// TERMINAL_IMPORT_STATES are the states from which a job will never move again.
export const TERMINAL_IMPORT_STATES: readonly ImportJobState[] = [
    'completed',
    'completed_with_issues',
    'failed',
    'canceled',
];

export const isTerminalImportState = (state: ImportJobState): boolean => TERMINAL_IMPORT_STATES.includes(state);

// AWAITING_USER_IMPORT_STATES are the states where the job is waiting on a person, not on the worker.
// Polling can slow right down in these.
export const AWAITING_USER_IMPORT_STATES: readonly ImportJobState[] = [
    'awaiting_source',
    'awaiting_confirmation',
];

export const isAwaitingUserImportState = (state: ImportJobState): boolean =>
    AWAITING_USER_IMPORT_STATES.includes(state);

// ImportJobPhase is finer-grained progress within a state, for a status line. It is advisory: the
// state is what gates the UI.
export type ImportJobPhase =
    | 'inspecting'
    | 'resolving_users'
    | 'computing_actions'
    | 'awaiting_confirmation'
    | 'queued_import'
    | 'provisioning_space'
    | 'writing_pages'
    | 'finalizing';

export type ImportTargetKind = 'new' | 'existing';

export type ImportSourceSelectionMode = 'new' | 'existing';

// ImportAction is what the plan intends, or what execution actually did.
export type ImportAction =
    | 'create'
    | 'update'
    | 'noop'
    | 'preserve_local'
    | 'conflict'
    | 'blocked'
    | 'stale'
    | 'not_attempted';

// ImportOutcome is the per-entity result. Preflight rows carry the outcome the plan *would* produce;
// execution rows carry what happened.
export type ImportOutcome =
    | 'created'
    | 'updated'
    | 'unchanged'
    | 'local_preserved'
    | 'conflict_skipped'
    | 'blocked'
    | 'stale'
    | 'not_attempted_canceled'
    | 'not_attempted_failed';

export type ImportIssueSeverity = 'info' | 'warning' | 'error';

export type ImportIssueStage = 'inspection' | 'preflight' | 'execution';

// The acknowledgement keys a confirmation may have to set. The job tells the client which ones it
// requires; the client must never guess, because the server refuses a confirmation missing any of them
// and equally refuses unknown keys.
export const IMPORT_ACK_NEW_SPACE_METADATA = 'confirm_new_space_metadata';
export const IMPORT_ACK_PAGE_ONLY_PARTIAL = 'page_only_partial_import';
export const IMPORT_ACK_WIDEN_RESTRICTED = 'widen_restricted_pages';
export const IMPORT_ACK_REIMPORT_EXISTING = 'reimport_existing_pages';

export type ImportAcknowledgementKey =
    | typeof IMPORT_ACK_NEW_SPACE_METADATA
    | typeof IMPORT_ACK_PAGE_ONLY_PARTIAL
    | typeof IMPORT_ACK_WIDEN_RESTRICTED
    | typeof IMPORT_ACK_REIMPORT_EXISTING;

// ImportBundleCounts is what the uploaded bundle declared.
export type ImportBundleCounts = {
    pages: number;
    comments: number;
    attachments: number;
    restricted_manifest_total: number;
    restricted_emitted_pages: number;
    restricted_manifest_only: number;
};

export type ImportSpaceDefaults = {
    title: string;
    description: string;
};

export type ImportReportSource = {
    organization_id: string;
    space_key: string;
    space_name: string;
    import_source_id?: string;
};

export type ImportBundleSummary = {
    version: number;
    source: ImportReportSource;
    space_defaults: ImportSpaceDefaults;
    counts: ImportBundleCounts;
};

// ImportFidelity is the mandatory disclosure every job and report carries. full_fidelity is always
// false: this release imports pages only, and the block states that policy rather than any particular
// job's outcome.
export type ImportFidelity = {
    scope: string;
    comments: string;
    attachments: string;
    restricted_emitted_pages: string;
    restricted_manifest_only_entries: string;
    full_fidelity: boolean;
};

export type ImportReportCounts = {
    pages: number;
    comments: number;
    attachments: number;
    restricted_manifest_total: number;
    restricted_emitted_pages: number;
    restricted_manifest_only: number;
    actions: Record<string, number>;
    outcomes?: Record<string, number>;
    authors?: Record<string, number>;
    links?: Record<string, number>;
    issues_by_severity?: Record<string, number>;
};

// ImportReportSummary is the compact projection embedded in a job view. `revision` appears only on the
// preflight summary and is the value a confirmation must echo back — it names *which* plan was
// reviewed, and is the one piece of internal job state a client legitimately needs.
export type ImportReportSummary = {
    stage: string;
    generated_at: number;
    fidelity: ImportFidelity;
    counts: ImportReportCounts;
    revision?: string;
};

export type ImportProgress = {
    phase: ImportJobPhase | '';
    current: number;
    total: number;
};

export type ImportTargetView = {
    kind: ImportTargetKind;
    space_id?: string;
    team_id: string;
    existed: boolean;
};

// ImportSourceCandidate is a *suggestion* only. Two Confluence instances can share an organization id,
// a space key and a display name while being genuinely different sources, so match_reasons order the
// list and nothing more — selection is always an explicit user choice.
export type ImportSourceCandidate = {
    import_source_id: string;
    display_name: string;
    organization_id: string;
    external_space_key: string;
    mapped_page_count: number;
    last_import_at: number;
    match_reasons: string[];
};

export type ImportSelectedSource = {
    mode: ImportSourceSelectionMode;
    import_source_id?: string;
    display_name?: string;
};

// ImportPublicError is the stable, machine-readable reason a job stopped. Only the code is exposed;
// internal messages are never sent.
export type ImportPublicError = {
    code: string;
};

// ImportJobView is the job as the API presents it.
//
// An actor who has lost access to the target gets a deliberately minimal projection of this — id,
// state, error and timestamps, with everything target- and source-identifying absent — so a client
// must treat `target.space_id`, `bundle` and `selected_source` as possibly empty even for a job it
// just created.
export type ImportJobView = {
    id: string;
    state: ImportJobState;
    phase?: ImportJobPhase | '';
    progress: ImportProgress;
    target: ImportTargetView;
    bundle: ImportBundleSummary;
    source_candidates: ImportSourceCandidate[];
    selected_source?: ImportSelectedSource;
    error?: ImportPublicError;
    preflight?: ImportReportSummary;
    final?: ImportReportSummary;
    required_acknowledgements: string[];
    create_at: number;
    update_at: number;
    finished_at: number;
};

// ImportPreflightResultView is one row of the review table. It says what will happen to a page, and
// deliberately carries no hashes or mapping timestamps.
export type ImportPreflightResultView = {
    external_id: string;
    local_id?: string;
    title?: string;
    planned_action: ImportAction;
    outcome: string;

    // overwrite_eligible marks a conflict the user may approve for overwriting. Only a conflict is ever
    // eligible, and approval is per page: there is no blanket overwrite-all, because each approval
    // discards a specific person's edits.
    overwrite_eligible?: boolean;
    structural_changes?: string[];
};

export type ImportEntityRef = {
    type: string;
    external_id?: string;
    local_id?: string;
    title?: string;
};

// ImportIssue is one structured finding. `code` is stable and is what a localized client should key
// off; `message` and `remediation` are the server's own wording, persisted with the finding so a report
// downloaded months later reads the same.
export type ImportIssue = {
    stage: ImportIssueStage;
    severity: ImportIssueSeverity;
    code: string;
    entity?: ImportEntityRef;
    message: string;
    remediation?: string;
    details?: Record<string, unknown>;
};

// Paginated is the list envelope every paginated Docs endpoint uses. has_more comes from the server
// rather than being guessed from array length.
export type Paginated<T> = {
    items: T[];
    page: number;
    per_page: number;
    has_more: boolean;
};

// --- request shapes ---

export type ImportTargetRequest =
    | {kind: 'new'; team_id: string}
    | {kind: 'existing'; space_id: string};

export type ImportUploadRequest = {
    target: ImportTargetRequest;
};

export type ImportSourceSelectionRequest = {
    mode: ImportSourceSelectionMode;
    display_name?: string;
    import_source_id?: string;
};

export type ImportNewSpaceMetadata = {
    title: string;
    description: string;
};

// ImportConfirmRequest is the point of no return.
//
// `preflight_revision` must be the exact revision that was reviewed; the server refuses anything else,
// which is what stops a plan being confirmed after it changed. `overwrite_conflicts` lists the external
// ids whose local edits the user has agreed to discard — per page, never wholesale.
export type ImportConfirmRequest = {
    preflight_revision: string;
    new_space?: ImportNewSpaceMetadata;
    acknowledgements: Partial<Record<string, boolean>>;
    overwrite_conflicts?: string[];
};
