// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package importer

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"unicode/utf8"

	mmmodel "github.com/mattermost/mattermost/server/public/model"

	"github.com/mattermost/mattermost-plugin-docs/server/model"
)

// MaxManifestWarnings bounds how many producer manifest warnings are materialized as inspection
// issues. A valid sub-limit manifest could otherwise carry hundreds of thousands of warnings; each
// copied into an issue row, that would let a single upload consume a large multiple of the manifest
// size. Warnings beyond the cap are summarized in one aggregate issue.
const MaxManifestWarnings = 1000

// MaxHierarchyDepth is the maximum page depth the importer accepts, mirroring model.MaxPageDepth: a
// root page is depth 1, so a chain of 10 pages is the deepest allowed and an 11-page chain is
// rejected. The value is duplicated (rather than imported from the app package) to keep this pure
// package dependency-light; the model constant is the canonical one and both must move together.
const MaxHierarchyDepth = model.MaxPageDepth

// MaxComments bounds how many comment lines a bundle may declare. Comments are counted but never
// staged, yet every comment's source id is held in memory to enforce uniqueness and reply ordering,
// so the count must be bounded even though nothing is persisted per comment.
const MaxComments = 50_000

// maxIssueTextBytes bounds a generated issue message or remediation string. Producer-supplied text
// (a manifest warning) is truncated to fit rather than rejected: a verbose warning should not fail an
// otherwise valid bundle.
const maxIssueTextBytes = 2048

// Manifest mirrors the fields of the producer's import-manifest.json that the importer reads.
// Unknown fields are ignored (forward-compatible).
type Manifest struct {
	Version         string                   `json:"version"`
	Source          ManifestSource           `json:"source"`
	Target          ManifestTarget           `json:"target"`
	Counts          ManifestCounts           `json:"counts"`
	Checksums       ManifestChecksums        `json:"checksums"`
	Users           []ManifestUser           `json:"users"`
	RestrictedPages []ManifestRestrictedPage `json:"restricted_pages"`
	Warnings        []string                 `json:"warnings"`
	Errors          []string                 `json:"errors"`
}

// ManifestSource is the bundle's source namespace metadata.
type ManifestSource struct {
	Type           string `json:"type"`
	OrganizationID string `json:"organization_id"`
	SpaceKey       string `json:"space_key"`
	SpaceName      string `json:"space_name"`
	ExportFile     string `json:"export_file"`
}

// ManifestTarget carries advisory destination metadata (never authoritative).
type ManifestTarget struct {
	Team string `json:"team"`
}

// ManifestCounts holds the producer's entity counts, reconciled against parsed values.
type ManifestCounts struct {
	Spaces      int `json:"spaces"`
	Pages       int `json:"pages"`
	Comments    int `json:"comments"`
	Attachments int `json:"attachments"`
}

// ManifestChecksums carries the JSONL checksum used to verify archive integrity.
type ManifestChecksums struct {
	JSONLSha256       string `json:"jsonl_sha256"`
	AttachmentsSha256 string `json:"attachments_sha256"`
}

// ManifestUser maps a source Confluence account to a proposed Mattermost username.
type ManifestUser struct {
	AccountID          string `json:"account_id"`
	ConfluenceUsername string `json:"confluence_username"`
	MattermostUsername string `json:"mattermost_username"`
}

// ManifestRestrictedPage records a page that carried a Confluence view restriction.
type ManifestRestrictedPage struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// InspectError is a hard failure that prevents a job from being created. It carries a stable code, and
// optionally the more specific error it was raised for.
type InspectError struct {
	Code    string
	Message string
	// Cause is the underlying failure, when one exists. It is preserved so a caller mapping errors onto
	// an HTTP contract can reach the specific code — a document rejected for exceeding a size limit and
	// one rejected as malformed both surface here as page_content_invalid, and only the cause tells them
	// apart.
	Cause error
}

func (e *InspectError) Error() string { return e.Message }

// Unwrap exposes Cause to errors.As, which is what lets the more specific code drive the status.
func (e *InspectError) Unwrap() error { return e.Cause }

func inspectErr(code, format string, args ...any) *InspectError {
	return &InspectError{Code: code, Message: fmt.Sprintf(format, args...)}
}

// inspectErrCausedBy is inspectErr for a failure raised from a more specific error worth preserving.
func inspectErrCausedBy(code string, cause error, format string, args ...any) *InspectError {
	return &InspectError{Code: code, Message: fmt.Sprintf(format, args...), Cause: cause}
}

// Stable inspection hard-failure codes.
const (
	InspectErrManifestInvalid      = "manifest_invalid"
	InspectErrManifestVersion      = "manifest_unsupported_version"
	InspectErrManifestSourceType   = "manifest_unsupported_source_type"
	InspectErrManifestHasErrors    = "manifest_reports_errors"
	InspectErrChecksumMissing      = "jsonl_checksum_missing"
	InspectErrChecksumMismatch     = "jsonl_checksum_mismatch"
	InspectErrJSONLEmpty           = "jsonl_empty"
	InspectErrBlankLine            = "jsonl_blank_line"
	InspectErrLineTooLong          = "jsonl_line_too_long"
	InspectErrLineInvalid          = "jsonl_line_invalid"
	InspectErrSequence             = "jsonl_bad_sequence"
	InspectErrUnknownType          = "jsonl_unknown_type"
	InspectErrPayloadMismatch      = "jsonl_payload_mismatch"
	InspectErrVersionValue         = "jsonl_unsupported_version"
	InspectErrTooManyPages         = "jsonl_too_many_pages"
	InspectErrTooManyComments      = "jsonl_too_many_comments"
	InspectErrRestrictionInvalid   = "manifest_restriction_invalid"
	InspectErrPageMissingID        = "page_missing_external_id"
	InspectErrPageInvalidID        = "page_invalid_external_id"
	InspectErrDuplicatePageID      = "page_duplicate_external_id"
	InspectErrPageMissingTitle     = "page_missing_title"
	InspectErrPageTitleTooLong     = "page_title_too_long"
	InspectErrParentNotSeen        = "page_parent_not_seen"
	InspectErrCycle                = "page_cycle"
	InspectErrDepthExceeded        = "page_depth_exceeded"
	InspectErrTipTap               = "page_content_invalid"
	InspectErrSpaceKeyMismatch     = "space_key_mismatch"
	InspectErrSpaceKeyMissing      = "space_key_missing"
	InspectErrSpaceNameTooLong     = "space_name_too_long"
	InspectErrSpaceTextTooLong     = "space_text_too_long"
	InspectErrCommentInvalid       = "comment_invalid"
	InspectErrAttachmentPath       = "attachment_invalid_path"
	InspectErrAttachmentID         = "attachment_invalid_source_id"
	InspectErrCountMismatch        = "manifest_count_mismatch"
	InspectErrManifestUserInvalid  = "manifest_user_invalid"
	InspectErrManifestUserConflict = "manifest_user_conflict"
	InspectErrTooManyManifestUsers = "manifest_too_many_users"
	InspectErrUnstorableText       = "unstorable_text"
	InspectErrSourcePropShape      = "source_prop_invalid_shape"
	InspectErrHash                 = "hash_failed"
)

// Inspection issue severities, mirroring the persisted enumeration.
const (
	SeverityInfo    = "info"
	SeverityWarning = "warning"
	SeverityError   = "error"
)

// Stable inspection-stage issue codes (non-fatal; surfaced in the report).
const (
	IssueBundleTeamMismatch            = "bundle_team_mismatch"
	IssueManifestWarning               = "manifest_warning"
	IssueAttachmentChecksumNotVerified = "attachment_checksum_not_verified"
	IssueSourceCreateAtInvalid         = "source_create_at_invalid"
	IssueSourceUpdateAtInvalid         = "source_update_at_invalid"
	IssuePlaceholderInText             = "placeholder_in_text_not_rewritten"
	// IssueAttachmentsNotImported flags a page that carries attachment records, none of which are
	// imported in this release. It is the plan's partial-scope code, distinct from the
	// attachment_placeholder_not_imported link code used for a discovered CONF_ATTACHMENT placeholder.
	IssueAttachmentsNotImported = "attachments_not_imported"
	// IssueRestrictedManifestEntryNotEmitted records a manifest restriction identity that matches no
	// emitted page, so it cannot claim widened access.
	IssueRestrictedManifestEntryNotEmitted = "restricted_manifest_entry_not_emitted"
	// IssueCommentsNotImported records that comments were counted but not imported.
	IssueCommentsNotImported = "comments_not_imported"
)

// InspectionIssue is one non-fatal finding recorded during inspection.
type InspectionIssue struct {
	Severity    string         `json:"severity"`
	Code        string         `json:"code"`
	ExternalID  string         `json:"external_id,omitempty"`
	Title       string         `json:"title,omitempty"`
	Message     string         `json:"message"`
	Remediation string         `json:"remediation,omitempty"`
	Details     map[string]any `json:"details,omitempty"`
}

// StagedPage is one normalized page handed to the sink. It is not retained by the inspector: the
// caller persists it and the inspector moves on, which is what keeps peak memory bounded regardless
// of bundle size.
type StagedPage struct {
	Ordinal    int
	SourceLine int
	ExternalID string
	// ParentExternalID is structural metadata: it is compared through its own baseline and is
	// deliberately absent from the source content hash.
	ParentExternalID          string
	SourceOrdinal             int
	Restricted                bool
	Title                     string
	CanonicalBody             string
	SearchText                string
	SourceUserProposal        string
	SourceAuthorAccountID     string
	SourceCreateAt            int64
	SourceUpdateAt            int64
	SourceProps               map[string]any
	IncomingSourceContentHash string
	// Links are the placeholders discovered in this page's approved attributes and text. They are not
	// persisted verbatim; the caller uses them for link counts and per-page link issues.
	Links []DiscoveredLink
}

// StagedManifestUser is one validated manifest user mapping handed to the sink for persistence.
type StagedManifestUser struct {
	Ordinal            int
	AccountID          string
	ConfluenceUsername string
	MattermostUsername string
}

// StreamSink receives normalized inspection output incrementally. The inspector stays a pure
// package: the store is injected here as a sink rather than imported, so staging rows can be written
// inside the caller's transaction while parsing proceeds. Any error a callback returns aborts
// inspection and is returned to the caller unchanged.
type StreamSink struct {
	Page         func(*StagedPage) error
	ManifestUser func(*StagedManifestUser) error
	Issue        func(*InspectionIssue) error
}

// RestrictedSummary partitions manifest restricted entries by whether they intersect emitted pages.
type RestrictedSummary struct {
	ManifestTotal int `json:"restricted_manifest_total"`
	EmittedPages  int `json:"restricted_emitted_pages"`
	ManifestOnly  int `json:"restricted_manifest_only"`
}

// InspectionSummary is the aggregate inspection outcome. It carries counts and metadata only: pages,
// users, and issues were streamed to the sink.
type InspectionSummary struct {
	Version          int
	OrganizationID   string
	SpaceKey         string
	SpaceName        string
	SpaceTitle       string
	SpaceDescription string

	PageCount       int
	CommentCount    int
	AttachmentCount int
	Restricted      RestrictedSummary

	JSONLSha256 string
	Links       LinkCounts
}

// LinkCounts aggregates discovered placeholder links across the bundle.
type LinkCounts struct {
	SameSource       int
	Unresolved       int
	FilePlaceholders int
	InText           int
}

// InspectOptions carries optional context the pure inspector uses only for advisory checks.
type InspectOptions struct {
	// RequestedTeamName, when non-empty, is compared against the advisory bundle team values to
	// emit a single aggregate bundle_team_mismatch warning. The value is never used to route.
	RequestedTeamName string
	// Now is the caller's current time in epoch milliseconds, used only to judge whether a source
	// timestamp is implausibly in the future. Passing it (rather than reading the wall clock) keeps
	// this package pure and deterministic in tests. When zero, a fixed year-2100 ceiling is used.
	Now int64
}

// futureTimestampAllowance is how far past Now a source timestamp may sit before it is judged
// implausibly in the future — clock skew between the source and this server should be well under a day.
const futureTimestampAllowance = int64(24 * 60 * 60 * 1000)

// year2100Millis is the fallback future ceiling used when InspectOptions.Now is not supplied.
const year2100Millis = int64(4102444800000)

// futureCeiling returns the largest source timestamp treated as plausible.
func (o InspectOptions) futureCeiling() int64 {
	if o.Now > 0 {
		return o.Now + futureTimestampAllowance
	}
	return year2100Millis
}

// Inspect validates a bundle and streams its normalized contents to sink, returning the aggregate
// summary. It is memory-bounded: the manifest is the only entry read whole, the JSONL is streamed
// once to verify its checksum and once more to parse, and each page is canonicalized and handed off
// individually rather than accumulated.
//
// A hard failure returns an *InspectError (or *ArchiveError) and the caller must discard everything
// already streamed — the plan requires the whole staging transaction to roll back.
func Inspect(a *Archive, opts InspectOptions, sink StreamSink) (*InspectionSummary, error) {
	manifestBytes, err := a.ReadManifest()
	if err != nil {
		return nil, err
	}
	manifest, err := parseManifest(manifestBytes)
	if err != nil {
		return nil, err
	}

	// A producer that reported its own errors yields a failed upload.
	if len(manifest.Errors) > 0 {
		return nil, inspectErr(InspectErrManifestHasErrors, "manifest reports %d producer error(s): %s", len(manifest.Errors), manifest.Errors[0])
	}
	// A source space key is mandatory: it becomes the ImportSource's ExternalSpaceKey. It is also
	// indexed, so it must satisfy the bounded-identifier contract.
	if manifest.Source.SpaceKey == "" {
		return nil, inspectErr(InspectErrSpaceKeyMissing, "manifest source is missing a space key")
	}
	if !IsValidIdentifier(manifest.Source.SpaceKey, SpaceKeyMaxBytes) {
		return nil, inspectErr(InspectErrSpaceKeyMismatch, "manifest source space key %q is not a valid bounded identifier", manifest.Source.SpaceKey)
	}
	if manifest.Source.OrganizationID != "" && !IsValidIdentifier(manifest.Source.OrganizationID, SpaceKeyMaxBytes) {
		return nil, inspectErr(InspectErrSpaceKeyMismatch, "manifest source organization id is not a valid bounded identifier")
	}
	// The Space name is display text rather than an identifier, but it is still persisted (as
	// ImportSource.ExternalSpaceName and inside the bundle summary), so it needs the same storability and
	// size checks. Without them a NUL reaches PostgreSQL as a write failure and an unbounded value
	// overflows the summary column — malformed input arriving as a 500 instead of a rejection.
	if !IsStorableText(manifest.Source.SpaceName) {
		return nil, inspectErr(InspectErrUnstorableText, "manifest source space name contains invalid UTF-8 or a NUL character")
	}
	if utf8.RuneCountInString(manifest.Source.SpaceName) > SpaceNameMaxRunes {
		return nil, inspectErr(InspectErrSpaceNameTooLong, "manifest source space name exceeds %d characters", SpaceNameMaxRunes)
	}

	// Verify JSONL integrity before parsing anything from it.
	if manifest.Checksums.JSONLSha256 == "" {
		return nil, inspectErr(InspectErrChecksumMissing, "manifest is missing checksums.jsonl_sha256")
	}
	jsonlSha, err := a.JSONLSha256()
	if err != nil {
		return nil, err
	}
	if !strings.EqualFold(manifest.Checksums.JSONLSha256, jsonlSha) {
		return nil, inspectErr(InspectErrChecksumMismatch, "import.jsonl checksum does not match the manifest")
	}

	summary := &InspectionSummary{
		OrganizationID: manifest.Source.OrganizationID,
		SpaceKey:       manifest.Source.SpaceKey,
		SpaceName:      manifest.Source.SpaceName,
		JSONLSha256:    jsonlSha,
	}

	if issueErr := emitManifestIssues(manifest, sink); issueErr != nil {
		return nil, issueErr
	}
	if userErr := emitManifestUsers(manifest, sink); userErr != nil {
		return nil, userErr
	}

	restricted, err := restrictedIDSet(manifest)
	if err != nil {
		return nil, err
	}
	summary.Restricted.ManifestTotal = len(restricted)

	emittedRestricted := make(map[string]struct{}, len(restricted))
	if err := streamJSONL(a, manifest, opts, sink, summary, restricted, emittedRestricted); err != nil {
		return nil, err
	}

	summary.Restricted.EmittedPages = len(emittedRestricted)
	summary.Restricted.ManifestOnly = summary.Restricted.ManifestTotal - summary.Restricted.EmittedPages

	// Manifest restriction identities that match no emitted page cannot claim widened access; they are
	// reported individually so an operator can see exactly which pages they were.
	if err := emitManifestOnlyRestrictions(manifest, emittedRestricted, sink); err != nil {
		return nil, err
	}
	if err := reconcileCounts(manifest, summary); err != nil {
		return nil, err
	}
	if summary.CommentCount > 0 {
		if err := sink.Issue(&InspectionIssue{
			Severity: SeverityWarning, Code: IssueCommentsNotImported,
			Message:     fmt.Sprintf("%d comment(s) were counted but are not imported in this release", summary.CommentCount),
			Remediation: "Comment import is a future release; the counts are recorded in the report.",
			Details:     map[string]any{"comments": summary.CommentCount},
		}); err != nil {
			return nil, err
		}
	}
	return summary, nil
}

// parseManifest decodes and version-checks the manifest.
func parseManifest(b []byte) (*Manifest, error) {
	if !utf8.Valid(b) {
		return nil, inspectErr(InspectErrManifestInvalid, "manifest is not valid UTF-8")
	}
	var m Manifest
	dec := json.NewDecoder(strings.NewReader(string(b)))
	if err := dec.Decode(&m); err != nil {
		return nil, inspectErr(InspectErrManifestInvalid, "manifest is not valid JSON: %v", err)
	}
	// Reject trailing data after the manifest object. json.Decoder.More() is not a trailing-data
	// check — it returns false before a closing "]"/"}" delimiter, so "{...}]" would pass. Decoding
	// a second value and requiring io.EOF rejects any trailing token while tolerating whitespace.
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, inspectErr(InspectErrManifestInvalid, "manifest has trailing data after the JSON object")
	}
	if m.Version != ManifestVersion {
		return nil, inspectErr(InspectErrManifestVersion, "manifest version %q is unsupported; require %q", m.Version, ManifestVersion)
	}
	// The source type is required, not merely checked when present: an absent type means the bundle
	// never declared what it came from, and this importer only understands Confluence.
	if m.Source.Type != model.ImportSourceTypeConfluence {
		return nil, inspectErr(InspectErrManifestSourceType, "manifest source type %q is unsupported; require %q", m.Source.Type, model.ImportSourceTypeConfluence)
	}
	return &m, nil
}

// emitManifestIssues streams the manifest-derived issues: producer warnings (bounded) and the
// attachment-checksum notice.
func emitManifestIssues(manifest *Manifest, sink StreamSink) error {
	warnings := manifest.Warnings
	suppressed := 0
	if len(warnings) > MaxManifestWarnings {
		suppressed = len(warnings) - MaxManifestWarnings
		warnings = warnings[:MaxManifestWarnings]
	}
	for _, w := range warnings {
		if !IsStorableText(w) {
			return inspectErr(InspectErrUnstorableText, "a manifest warning contains invalid UTF-8 or a NUL character")
		}
		if err := sink.Issue(&InspectionIssue{
			Severity: SeverityWarning, Code: IssueManifestWarning, Message: truncateIssueText(w),
			Remediation: "Review the producer warning; it does not block import.",
		}); err != nil {
			return err
		}
	}
	if suppressed > 0 {
		if err := sink.Issue(&InspectionIssue{
			Severity: SeverityWarning, Code: IssueManifestWarning,
			Message:     fmt.Sprintf("%d additional manifest warnings were suppressed (limit %d)", suppressed, MaxManifestWarnings),
			Remediation: "Only the first manifest warnings are reported individually.",
			Details:     map[string]any{"suppressed": suppressed, "limit": MaxManifestWarnings},
		}); err != nil {
			return err
		}
	}
	if manifest.Checksums.AttachmentsSha256 != "" {
		if err := sink.Issue(&InspectionIssue{
			Severity: SeverityInfo, Code: IssueAttachmentChecksumNotVerified,
			Message:     "attachment checksum not verified: attachments are out of scope in this release",
			Remediation: "Attachment bytes are neither extracted nor verified; import attachments in a future release.",
			Details:     map[string]any{"reason": "attachments_out_of_scope"},
		}); err != nil {
			return err
		}
	}
	return nil
}

// emitManifestUsers validates and streams the manifest user mappings in deterministic manifest
// order. A duplicate account id carrying conflicting values is a contract violation, because author
// resolution would then depend on which row won.
func emitManifestUsers(manifest *Manifest, sink StreamSink) error {
	if len(manifest.Users) > MaxManifestUsers {
		return inspectErr(InspectErrTooManyManifestUsers, "manifest has %d users, limit is %d", len(manifest.Users), MaxManifestUsers)
	}
	seen := make(map[string]ManifestUser, len(manifest.Users))
	ordinal := 0
	for _, u := range manifest.Users {
		if !IsValidIdentifier(u.AccountID, ExternalIDMaxBytes) {
			return inspectErr(InspectErrManifestUserInvalid, "manifest user account id %q is not a valid bounded identifier", u.AccountID)
		}
		if !IsStorableText(u.ConfluenceUsername) || !IsStorableText(u.MattermostUsername) {
			return inspectErr(InspectErrUnstorableText, "manifest user %q contains invalid UTF-8 or a NUL character", u.AccountID)
		}
		if prev, dup := seen[u.AccountID]; dup {
			if prev != u {
				return inspectErr(InspectErrManifestUserConflict, "manifest lists account id %q twice with conflicting values", u.AccountID)
			}
			// An exact duplicate is redundant but harmless; skip it so the unique constraint holds.
			continue
		}
		seen[u.AccountID] = u
		if err := sink.ManifestUser(&StagedManifestUser{
			Ordinal:            ordinal,
			AccountID:          u.AccountID,
			ConfluenceUsername: u.ConfluenceUsername,
			MattermostUsername: u.MattermostUsername,
		}); err != nil {
			return err
		}
		ordinal++
	}
	return nil
}

// restrictedIDSet returns the deduplicated manifest restriction identities, validating each one. Both
// the id and the title are persisted (as an issue's external id and title), so an out-of-contract id
// or NUL-bearing title has to be rejected here rather than surfacing as an opaque database failure.
func restrictedIDSet(manifest *Manifest) (map[string]string, error) {
	set := make(map[string]string, len(manifest.RestrictedPages))
	for _, rp := range manifest.RestrictedPages {
		if !IsValidIdentifier(rp.ID, ExternalIDMaxBytes) {
			return nil, inspectErr(InspectErrRestrictionInvalid, "manifest restriction id %q is not a valid bounded identifier", rp.ID)
		}
		if !IsStorableText(rp.Title) {
			return nil, inspectErr(InspectErrUnstorableText, "manifest restriction %q has a title containing invalid UTF-8 or a NUL character", rp.ID)
		}
		if _, dup := set[rp.ID]; dup {
			continue
		}
		set[rp.ID] = rp.Title
	}
	return set, nil
}

// emitManifestOnlyRestrictions reports each restriction identity that never matched an emitted page,
// in deterministic id order.
func emitManifestOnlyRestrictions(manifest *Manifest, emitted map[string]struct{}, sink StreamSink) error {
	all, err := restrictedIDSet(manifest)
	if err != nil {
		return err
	}
	ids := make([]string, 0, len(all))
	for id := range all {
		if _, ok := emitted[id]; !ok {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	for _, id := range ids {
		if err := sink.Issue(&InspectionIssue{
			Severity: SeverityInfo, Code: IssueRestrictedManifestEntryNotEmitted,
			ExternalID: id, Title: all[id],
			Message:     "a manifest restriction entry does not correspond to any emitted page, so no access was widened for it",
			Remediation: "The producer may have listed a draft, deleted, or historical page that the export omitted.",
		}); err != nil {
			return err
		}
	}
	return nil
}

// parseState tracks the required JSONL line order.
type parseState int

const (
	stateVersion parseState = iota
	stateSpace
	statePages
	stateComments
	stateDone
)

// streamParser holds the cross-line state the JSONL state machine needs. Its maps are bounded by the
// page and comment caps, so they stay small relative to the bodies that are never accumulated.
type streamParser struct {
	manifest *Manifest
	opts     InspectOptions
	sink     StreamSink
	summary  *InspectionSummary

	state parseState
	// depthOf gives each emitted page's depth (root = 1). Because a parent must appear before its
	// child, a child's depth is derived from its parent's in one step, so the limit is enforced as
	// pages stream rather than in a second pass.
	depthOf map[string]int
	// siblingCounter maps a parent external id ("" for roots) to the next sibling ordinal.
	siblingCounter map[string]int
	// commentIDs enforces comment source-id uniqueness and reply-parent ordering.
	commentIDs  map[string]struct{}
	teamValues  map[string]struct{}
	pageOrdinal int
	// pendingTeamMismatch holds the aggregate advisory-team mismatch until every line is parsed,
	// so the issue is emitted once at a deterministic position rather than per line.
	pendingTeamMismatch []string
}

// streamJSONL reopens import.jsonl and runs the strict v2 line sequence, handing each normalized page
// to the sink as it is parsed.
func streamJSONL(a *Archive, manifest *Manifest, opts InspectOptions, sink StreamSink, summary *InspectionSummary, restricted map[string]string, emittedRestricted map[string]struct{}) error {
	rc, err := a.OpenJSONL()
	if err != nil {
		return err
	}
	// Best-effort close: a parse failure is already being returned, and a CRC error on an entry we
	// deliberately stopped reading early would be misleading.
	defer func() { _ = rc.Close() }()

	p := &streamParser{
		manifest: manifest, opts: opts, sink: sink, summary: summary,
		depthOf:        make(map[string]int),
		siblingCounter: make(map[string]int),
		commentIDs:     make(map[string]struct{}),
		teamValues:     make(map[string]struct{}),
	}
	// The manifest's advisory target team is compared alongside the per-line values, so a mismatch
	// declared only in the manifest is still surfaced.
	p.collectTeam(manifest.Target.Team)

	scanner := bufio.NewScanner(rc)
	// Permit a single line up to the documented limit, and one byte beyond so an exactly-oversized
	// line is reported as too long rather than silently truncated.
	scanner.Buffer(make([]byte, 0, 64*1024), MaxJSONLLineBytes+1)

	lineNo := 0
	total := 0
	for scanner.Scan() {
		lineNo++
		raw := scanner.Bytes()
		total += len(raw) + 1
		if total > MaxJSONLBytes {
			return inspectErr(ArchiveErrJSONLTooLarge, "import.jsonl exceeds %d bytes", MaxJSONLBytes)
		}
		if err := p.handleLine(raw, lineNo, restricted, emittedRestricted); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		if errors.Is(err, bufio.ErrTooLong) {
			return inspectErr(InspectErrLineTooLong, "an import.jsonl line exceeds %d bytes", MaxJSONLLineBytes)
		}
		return inspectErr(InspectErrLineInvalid, "failed to read import.jsonl: %v", err)
	}
	if lineNo == 0 {
		return inspectErr(InspectErrJSONLEmpty, "import.jsonl is empty")
	}
	if p.state != stateDone {
		return inspectErr(InspectErrSequence, "import.jsonl is missing the trailing resolve_space_placeholders line")
	}

	summary.PageCount = p.pageOrdinal
	p.emitTeamMismatch()
	return p.flushTeamMismatch()
}

// handleLine dispatches one JSONL line through the sequence state machine.
func (p *streamParser) handleLine(raw []byte, lineNo int, restricted map[string]string, emittedRestricted map[string]struct{}) error {
	if len(raw) == 0 {
		return inspectErr(InspectErrBlankLine, "import.jsonl line %d is blank", lineNo)
	}
	if len(raw) > MaxJSONLLineBytes {
		return inspectErr(InspectErrLineTooLong, "import.jsonl line %d exceeds %d bytes", lineNo, MaxJSONLLineBytes)
	}
	if !utf8.Valid(raw) {
		return inspectErr(InspectErrUnstorableText, "import.jsonl line %d is not valid UTF-8", lineNo)
	}

	var line Line
	if err := json.Unmarshal(raw, &line); err != nil {
		return inspectErr(InspectErrLineInvalid, "import.jsonl line %d is invalid JSON: %v", lineNo, err)
	}
	if lineHasForeignPayload(&line, line.Type) {
		return inspectErr(InspectErrPayloadMismatch, "import.jsonl line %d declares type %q but carries another type's payload", lineNo, line.Type)
	}

	switch line.Type {
	case LineTypeVersion:
		if p.state != stateVersion {
			return inspectErr(InspectErrSequence, "unexpected version line at line %d", lineNo)
		}
		if line.Version == nil || *line.Version != ContractVersion {
			return inspectErr(InspectErrVersionValue, "line %d: version must be %d", lineNo, ContractVersion)
		}
		// The version line's source block is required, not optional: it is the bundle's own declaration of
		// which source namespace every following line belongs to.
		if line.Source == nil {
			return inspectErr(InspectErrSpaceKeyMissing, "line %d: version line is missing its source block", lineNo)
		}
		if err := requireSameSpaceKey("version.source.space_key", stringOrEmpty(line.Source.SpaceKey), p.manifest.Source.SpaceKey); err != nil {
			return err
		}
		p.summary.Version = *line.Version
		p.state = stateSpace

	case LineTypeSpace:
		if p.state != stateSpace {
			return inspectErr(InspectErrSequence, "unexpected space line at line %d", lineNo)
		}
		if line.Space == nil {
			return inspectErr(InspectErrPayloadMismatch, "line %d declares type space but has no space payload", lineNo)
		}
		if err := p.handleSpaceLine(line.Space); err != nil {
			return err
		}
		p.state = statePages

	case LineTypePage:
		if p.state != statePages {
			return inspectErr(InspectErrSequence, "unexpected page line at line %d (pages must follow the space line and precede comments)", lineNo)
		}
		if line.Page == nil {
			return inspectErr(InspectErrPayloadMismatch, "line %d declares type page but has no page payload", lineNo)
		}
		if p.pageOrdinal >= MaxPages {
			return inspectErr(InspectErrTooManyPages, "bundle has more than %d pages", MaxPages)
		}
		return p.handlePageLine(line.Page, lineNo, restricted, emittedRestricted)

	case LineTypePageComment:
		if p.state != statePages && p.state != stateComments {
			return inspectErr(InspectErrSequence, "unexpected page_comment line at line %d", lineNo)
		}
		p.state = stateComments
		if line.PageComment == nil {
			return inspectErr(InspectErrPayloadMismatch, "line %d declares type page_comment but has no payload", lineNo)
		}
		return p.handleCommentLine(line.PageComment, lineNo)

	case LineTypeResolveSpacePlaceholders:
		if p.state != statePages && p.state != stateComments {
			return inspectErr(InspectErrSequence, "unexpected resolve_space_placeholders line at line %d", lineNo)
		}
		if line.ResolveSpacePlaceholders == nil {
			return inspectErr(InspectErrPayloadMismatch, "line %d declares type resolve_space_placeholders but has no payload", lineNo)
		}
		if err := requireSameSpaceKey("resolve_space_placeholders.space_import_source_id",
			stringOrEmpty(line.ResolveSpacePlaceholders.SpaceImportSourceID), p.manifest.Source.SpaceKey); err != nil {
			return err
		}
		p.collectTeam(stringOrEmpty(line.ResolveSpacePlaceholders.Team))
		p.state = stateDone

	case "":
		return inspectErr(InspectErrUnknownType, "line %d has an empty type", lineNo)
	default:
		return inspectErr(InspectErrUnknownType, "line %d has unknown type %q", lineNo, line.Type)
	}
	return nil
}

// handleSpaceLine records the Space defaults and validates its space key against the manifest.
func (p *streamParser) handleSpaceLine(space *SpaceData) error {
	p.collectTeam(stringOrEmpty(space.Team))
	title := stringOrEmpty(space.Title)
	description := stringOrEmpty(space.Description)
	if !IsStorableText(title) || !IsStorableText(description) {
		return inspectErr(InspectErrUnstorableText, "the space title or description contains invalid UTF-8 or a NUL character")
	}
	// Both are persisted (as the editable new-Space defaults and inside the bundle summary), so they are
	// bounded here for the same reason the source Space name is: an unbounded value would otherwise
	// surface as a column or summary write failure rather than a rejection.
	if utf8.RuneCountInString(title) > SpaceTitleMaxRunes {
		return inspectErr(InspectErrSpaceTextTooLong, "the space title exceeds %d characters", SpaceTitleMaxRunes)
	}
	if utf8.RuneCountInString(description) > SpaceDescriptionMaxRunes {
		return inspectErr(InspectErrSpaceTextTooLong, "the space description exceeds %d characters", SpaceDescriptionMaxRunes)
	}
	p.summary.SpaceTitle = title
	p.summary.SpaceDescription = description
	return requireSameSpaceKey("space.props.import_source_id",
		propString(derefProps(space.Props), PropImportSourceID), p.manifest.Source.SpaceKey)
}

// handlePageLine validates and normalizes one page, then streams it to the sink.
func (p *streamParser) handlePageLine(page *PageData, lineNo int, restricted map[string]string, emittedRestricted map[string]struct{}) error {
	p.collectTeam(stringOrEmpty(page.Team))

	props := derefProps(page.Props)
	externalID := propString(props, PropImportSourceID)
	if externalID == "" {
		return inspectErr(InspectErrPageMissingID, "line %d: page is missing props.import_source_id", lineNo)
	}
	if !IsValidIdentifier(externalID, ExternalIDMaxBytes) {
		return inspectErr(InspectErrPageInvalidID, "line %d: page external id %q is not a valid bounded identifier", lineNo, externalID)
	}
	if _, dup := p.depthOf[externalID]; dup {
		return inspectErr(InspectErrDuplicatePageID, "line %d: duplicate page external id %q", lineNo, externalID)
	}

	// Normalize the title exactly as Page.PreSave does (SanitizeUnicode then trim). Import uses a
	// dedicated store path that bypasses PreSave, so without this the stored title could carry unsafe
	// Unicode controls, and the source hash would be computed on a value that never matches the
	// applied hash of the normalized title.
	rawTitle := stringOrEmpty(page.Title)
	if !IsStorableText(rawTitle) {
		return inspectErr(InspectErrUnstorableText, "line %d: page %q title contains invalid UTF-8 or a NUL character", lineNo, externalID)
	}
	title := strings.TrimSpace(mmmodel.SanitizeUnicode(rawTitle))
	if title == "" {
		return inspectErr(InspectErrPageMissingTitle, "line %d: page %q is missing a title", lineNo, externalID)
	}
	if utf8.RuneCountInString(title) > model.PageTitleMaxRunes {
		return inspectErr(InspectErrPageTitleTooLong, "line %d: page %q title exceeds %d runes", lineNo, externalID, model.PageTitleMaxRunes)
	}

	if err := requireSameSpaceKey(fmt.Sprintf("page %q space_import_source_id", externalID),
		stringOrEmpty(page.SpaceImportSourceID), p.manifest.Source.SpaceKey); err != nil {
		return err
	}

	parentID := stringOrEmpty(page.ParentImportSourceID)
	depth := 1
	if parentID != "" {
		if !IsValidIdentifier(parentID, ExternalIDMaxBytes) {
			return inspectErr(InspectErrPageInvalidID, "line %d: page %q parent id is not a valid bounded identifier", lineNo, externalID)
		}
		parentDepth, seen := p.depthOf[parentID]
		if !seen {
			return inspectErr(InspectErrParentNotSeen, "line %d: page %q references parent %q that has not appeared earlier", lineNo, externalID, parentID)
		}
		depth = parentDepth + 1
		if depth > MaxHierarchyDepth {
			return inspectErr(InspectErrDepthExceeded, "line %d: page %q exceeds maximum hierarchy depth of %d", lineNo, externalID, MaxHierarchyDepth)
		}
	}

	content := stringOrEmpty(page.Content)
	if !IsStorableText(content) {
		return inspectErr(InspectErrUnstorableText, "line %d: page %q content contains invalid UTF-8 or a NUL character", lineNo, externalID)
	}
	canonicalBody, searchText, links, tErr := CanonicalizeAndExtractSearchText(content)
	if tErr != nil {
		// Keep the TipTap error reachable: "too many nodes" and "not a doc" are the same inspection code
		// but a different HTTP contract (an over-limit document is unprocessable, a malformed one is a
		// bad request), and the distinction is only recoverable from the cause.
		return inspectErrCausedBy(InspectErrTipTap, tErr, "line %d: page %q content invalid: %v", lineNo, externalID, tErr)
	}

	sourceCreateAt := int64OrZero(page.CreateAt)
	if !plausibleTimestamp(sourceCreateAt, p.opts.futureCeiling()) {
		if err := p.sink.Issue(&InspectionIssue{
			Severity: SeverityWarning, Code: IssueSourceCreateAtInvalid, ExternalID: externalID, Title: title,
			Message:     "source create timestamp is missing, non-positive, or implausibly in the future",
			Remediation: "The page is staged; execution falls back to the import time for CreateAt.",
			Details:     map[string]any{"source_create_at": sourceCreateAt},
		}); err != nil {
			return err
		}
	}
	if page.UpdateAt != nil && !plausibleTimestamp(*page.UpdateAt, p.opts.futureCeiling()) {
		if err := p.sink.Issue(&InspectionIssue{
			Severity: SeverityWarning, Code: IssueSourceUpdateAtInvalid, ExternalID: externalID, Title: title,
			Message:     "source update timestamp was supplied but is not usable",
			Remediation: "The raw value is preserved in props; it does not affect local timestamps.",
			Details:     map[string]any{"source_update_at": *page.UpdateAt},
		}); err != nil {
			return err
		}
	}

	sourceProps, propErr := allowlistSourceProps(props, externalID, lineNo)
	if propErr != nil {
		return propErr
	}
	authorAccountID := propString(props, PropConfluenceAuthorAccountID)
	if authorAccountID != "" && !IsValidIdentifier(authorAccountID, ExternalIDMaxBytes) {
		return inspectErr(InspectErrPageInvalidID, "line %d: page %q author account id is not a valid bounded identifier", lineNo, externalID)
	}
	userProposal := stringOrEmpty(page.User)
	if !IsStorableText(userProposal) {
		return inspectErr(InspectErrUnstorableText, "line %d: page %q user proposal contains invalid UTF-8 or a NUL character", lineNo, externalID)
	}

	incomingHash, hErr := HashSourceContent(SourceContentHashInput{
		Title:           title,
		CanonicalBody:   canonicalBody,
		AuthorAccountID: authorAccountID,
		AuthorProposal:  userProposal,
		SourceCreateAt:  sourceCreateAt,
		SourceUpdateAt:  int64OrZero(page.UpdateAt),
		SourceProps:     sourceProps,
	})
	if hErr != nil {
		return inspectErr(InspectErrHash, "line %d: failed to hash page %q: %v", lineNo, externalID, hErr)
	}

	attachmentCount, attErr := p.countAttachments(page, externalID, title, lineNo)
	if attErr != nil {
		return attErr
	}
	p.summary.AttachmentCount += attachmentCount

	if err := p.emitLinkIssues(links, externalID, title); err != nil {
		return err
	}

	_, isRestricted := restricted[externalID]
	if isRestricted {
		emittedRestricted[externalID] = struct{}{}
	}

	p.depthOf[externalID] = depth
	sourceOrdinal := p.siblingCounter[parentID]
	p.siblingCounter[parentID] = sourceOrdinal + 1

	staged := &StagedPage{
		Ordinal:                   p.pageOrdinal,
		SourceLine:                lineNo,
		ExternalID:                externalID,
		ParentExternalID:          parentID,
		SourceOrdinal:             sourceOrdinal,
		Restricted:                isRestricted,
		Title:                     title,
		CanonicalBody:             canonicalBody,
		SearchText:                searchText,
		SourceUserProposal:        userProposal,
		SourceAuthorAccountID:     authorAccountID,
		SourceCreateAt:            sourceCreateAt,
		SourceUpdateAt:            int64OrZero(page.UpdateAt),
		SourceProps:               sourceProps,
		IncomingSourceContentHash: incomingHash,
		Links:                     links,
	}
	if err := p.sink.Page(staged); err != nil {
		return err
	}
	p.pageOrdinal++
	return nil
}

// countAttachments validates and counts a page's attachment records without opening any file.
func (p *streamParser) countAttachments(page *PageData, externalID, title string, lineNo int) (int, error) {
	if page.Attachments == nil || len(*page.Attachments) == 0 {
		return 0, nil
	}
	for _, att := range *page.Attachments {
		path := stringOrEmpty(att.Path)
		if err := validateAttachmentPath(path, externalID, lineNo); err != nil {
			return 0, err
		}
		// The attachment's own source id is indexed in a future release and must already conform.
		if sourceID := propString(derefProps(att.Props), PropImportSourceID); sourceID != "" {
			if !IsValidIdentifier(sourceID, ExternalIDMaxBytes) {
				return 0, inspectErr(InspectErrAttachmentID, "line %d: page %q has an attachment with an invalid source id", lineNo, externalID)
			}
		}
	}
	count := len(*page.Attachments)
	if err := p.sink.Issue(&InspectionIssue{
		Severity: SeverityInfo, Code: IssueAttachmentsNotImported, ExternalID: externalID, Title: title,
		Message:     fmt.Sprintf("%d attachment(s) counted but not imported in this release", count),
		Remediation: "Attachment import is a future release; bytes are neither extracted nor stored.",
		Details:     map[string]any{"attachments": count},
	}); err != nil {
		return 0, err
	}
	return count, nil
}

// emitLinkIssues reports placeholders left in ordinary text and accumulates the link counters.
func (p *streamParser) emitLinkIssues(links []DiscoveredLink, externalID, title string) error {
	inText := false
	for _, l := range links {
		switch {
		case l.InText:
			inText = true
		case l.Kind == LinkKindPageID || l.Kind == LinkKindPageTitle:
			p.summary.Links.SameSource++
		case l.Kind == LinkKindFile || l.Kind == LinkKindAttachment:
			p.summary.Links.FilePlaceholders++
		default:
			p.summary.Links.Unresolved++
		}
	}
	if inText {
		p.summary.Links.InText++
		return p.sink.Issue(&InspectionIssue{
			Severity: SeverityInfo, Code: IssuePlaceholderInText, ExternalID: externalID, Title: title,
			Message:     "a Confluence placeholder token appears in ordinary text and is left intact",
			Remediation: "Placeholders in text are not rewritten in this release.",
		})
	}
	return nil
}

// handleCommentLine validates one comment enough to count it. Comments are never staged in this
// release, but a malformed comment still indicates a broken bundle.
func (p *streamParser) handleCommentLine(c *PageCommentData, lineNo int) error {
	pageID := stringOrEmpty(c.PageImportSourceID)
	if pageID == "" {
		return inspectErr(InspectErrCommentInvalid, "line %d: page_comment is missing page_import_source_id", lineNo)
	}
	if _, known := p.depthOf[pageID]; !known {
		return inspectErr(InspectErrCommentInvalid, "line %d: page_comment references unknown page %q", lineNo, pageID)
	}
	if stringOrEmpty(c.User) == "" || stringOrEmpty(c.Content) == "" {
		return inspectErr(InspectErrCommentInvalid, "line %d: page_comment on page %q is missing a user or content", lineNo, pageID)
	}
	commentID := propString(derefProps(c.Props), PropImportSourceID)
	if !IsValidIdentifier(commentID, ExternalIDMaxBytes) {
		return inspectErr(InspectErrCommentInvalid, "line %d: page_comment on page %q has a missing or invalid source id", lineNo, pageID)
	}
	if _, dup := p.commentIDs[commentID]; dup {
		return inspectErr(InspectErrCommentInvalid, "line %d: duplicate comment source id %q", lineNo, commentID)
	}
	// A reply must follow the comment it answers, matching the producer's parent-before-reply order.
	if parent := stringOrEmpty(c.ParentCommentImportSourceID); parent != "" {
		if _, seen := p.commentIDs[parent]; !seen {
			return inspectErr(InspectErrCommentInvalid, "line %d: comment %q replies to %q which has not appeared earlier", lineNo, commentID, parent)
		}
	}
	if p.summary.CommentCount >= MaxComments {
		return inspectErr(InspectErrTooManyComments, "bundle has more than %d comments", MaxComments)
	}
	p.commentIDs[commentID] = struct{}{}
	p.summary.CommentCount++
	return nil
}

// collectTeam records a non-empty advisory team value.
func (p *streamParser) collectTeam(team string) {
	if team != "" {
		p.teamValues[team] = struct{}{}
	}
}

// emitTeamMismatch computes the aggregate advisory-team mismatch, storing it for flushTeamMismatch.
func (p *streamParser) emitTeamMismatch() {
	if p.opts.RequestedTeamName == "" {
		return
	}
	var mismatched []string
	for t := range p.teamValues {
		if t != p.opts.RequestedTeamName {
			mismatched = append(mismatched, t)
		}
	}
	sort.Strings(mismatched)
	p.pendingTeamMismatch = mismatched
}

// flushTeamMismatch emits the aggregate team-mismatch warning, if any, after all lines are parsed.
func (p *streamParser) flushTeamMismatch() error {
	if len(p.pendingTeamMismatch) == 0 {
		return nil
	}
	return p.sink.Issue(&InspectionIssue{
		Severity: SeverityWarning, Code: IssueBundleTeamMismatch,
		Message:     "advisory bundle team values differ from the requested target team",
		Remediation: "The requested target team is authoritative; the bundle team is advisory only.",
		Details:     map[string]any{"requested_team": p.opts.RequestedTeamName, "bundle_teams": p.pendingTeamMismatch},
	})
}

// allowlistSourceProps copies only the allowlisted source props, validating each one's shape so a
// malformed value cannot reach a JSONB column or a hash input.
func allowlistSourceProps(props map[string]any, externalID string, lineNo int) (map[string]any, error) {
	out := make(map[string]any)
	for _, key := range AllowlistedSourceProps {
		v, ok := props[key]
		if !ok {
			continue
		}
		if bad, storable := findUnstorableValue(v); !storable {
			return nil, inspectErr(InspectErrUnstorableText, "line %d: page %q prop %q contains invalid UTF-8 or a NUL character (%q)", lineNo, externalID, key, bad)
		}
		// import_labels is contractually an array of strings; a different shape means the producer
		// changed and must be handled deliberately rather than persisted opaquely.
		if key == PropImportLabels {
			items, isArray := v.([]any)
			if !isArray {
				return nil, inspectErr(InspectErrSourcePropShape, "line %d: page %q prop %q must be an array", lineNo, externalID, key)
			}
			for _, item := range items {
				if _, isString := item.(string); !isString {
					return nil, inspectErr(InspectErrSourcePropShape, "line %d: page %q prop %q must contain only strings", lineNo, externalID, key)
				}
			}
		}
		out[key] = v
	}
	return out, nil
}

// requireSameSpaceKey rejects a non-empty space key that differs from the manifest's. An empty value
// is tolerated (some lines may omit it); only a present-and-different key is a mismatch.
func requireSameSpaceKey(where, value, manifestKey string) error {
	if manifestKey == "" {
		// Unreachable in practice: parseManifest rejects an absent manifest key before any line is read.
		return inspectErr(InspectErrSpaceKeyMissing, "%s cannot be checked because the manifest has no source space key", where)
	}
	// An absent mirror is a rejection, not a pass. Tolerating it left the bundle's own statement of which
	// source namespace it belongs to unverified on exactly the lines that carry it, so a bundle could be
	// staged against a namespace no line ever agreed to.
	if value == "" {
		return inspectErr(InspectErrSpaceKeyMissing, "%s is missing; every source namespace mirror must repeat the manifest space key (%q)", where, manifestKey)
	}
	if value != manifestKey {
		return inspectErr(InspectErrSpaceKeyMismatch, "%s (%q) does not match manifest source space key (%q)", where, value, manifestKey)
	}
	return nil
}

// validateAttachmentPath rejects an unsafe attachment path without opening the file. The
// authoritative producer emits "/" separators on every OS, so a backslash is a producer bug.
func validateAttachmentPath(p, externalID string, lineNo int) error {
	if p == "" {
		return inspectErr(InspectErrAttachmentPath, "line %d: page %q has an attachment with an empty path", lineNo, externalID)
	}
	if !IsStorableText(p) || strings.Contains(p, "\\") || strings.HasPrefix(p, "/") {
		return inspectErr(InspectErrAttachmentPath, "line %d: page %q has an unsafe attachment path %q", lineNo, externalID, p)
	}
	for seg := range strings.SplitSeq(p, "/") {
		if seg == "." || seg == ".." {
			return inspectErr(InspectErrAttachmentPath, "line %d: page %q attachment path %q contains a %q segment", lineNo, externalID, p, seg)
		}
	}
	return nil
}

// reconcileCounts rejects the bundle when a parsed entity count disagrees with the manifest. The
// JSONL checksum is already verified, and the producer writes exactly one page/comment line per
// counted entity (and one attachment count per emitted attachment), so for any well-formed bundle
// the manifest counts equal the parsed counts exactly. A mismatch therefore signals a corrupt or
// internally inconsistent producer bundle, which must be rejected rather than imported partially.
func reconcileCounts(manifest *Manifest, summary *InspectionSummary) error {
	check := func(name string, parsed, declared int) error {
		if declared != parsed {
			return inspectErr(InspectErrCountMismatch, "manifest declares %d %s but %d were parsed", declared, name, parsed)
		}
		return nil
	}
	if err := check("pages", summary.PageCount, manifest.Counts.Pages); err != nil {
		return err
	}
	if err := check("comments", summary.CommentCount, manifest.Counts.Comments); err != nil {
		return err
	}
	return check("attachments", summary.AttachmentCount, manifest.Counts.Attachments)
}

// lineHasForeignPayload reports whether the line carries a payload field that does not belong to its
// declared type. The version line owns both the version and source fields; every other type owns
// exactly its matching payload.
func lineHasForeignPayload(l *Line, declaredType string) bool {
	present := map[string]bool{
		LineTypeVersion:                  l.Version != nil || l.Source != nil,
		LineTypeSpace:                    l.Space != nil,
		LineTypePage:                     l.Page != nil,
		LineTypePageComment:              l.PageComment != nil,
		LineTypeResolveSpacePlaceholders: l.ResolveSpacePlaceholders != nil,
	}
	for typ, set := range present {
		if typ != declaredType && set {
			return true
		}
	}
	return false
}

// plausibleTimestamp reports whether ms is a positive epoch-millis value at or below futureCeiling.
func plausibleTimestamp(ms, futureCeiling int64) bool {
	return ms > 0 && ms <= futureCeiling
}

// truncateIssueText clips producer-supplied text to the issue-text bound, marking that it was cut so
// a reader knows the message is partial.
func truncateIssueText(s string) string {
	if len(s) <= maxIssueTextBytes {
		return s
	}
	// Trim to a rune boundary so the stored text stays valid UTF-8.
	cut := maxIssueTextBytes - 3
	for cut > 0 && !utf8.ValidString(s[:cut]) {
		cut--
	}
	return s[:cut] + "..."
}
