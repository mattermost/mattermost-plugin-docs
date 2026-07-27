// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package importer

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	mmmodel "github.com/mattermost/mattermost/server/public/model"

	"github.com/mattermost/mattermost-plugin-docs/server/model"
)

// MaxManifestWarnings bounds how many producer manifest warnings are materialized as inspection
// issues. A valid sub-limit manifest could otherwise carry hundreds of thousands of warnings; each
// copied into an issue struct, that would let a single upload retain a large multiple of the
// manifest size, and concurrent uploads could exhaust plugin memory. Warnings beyond the cap are
// summarized in one aggregate issue rather than materialized individually.
const MaxManifestWarnings = 1000

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

// InspectError is a hard failure that prevents a job from being created. It carries a stable code.
type InspectError struct {
	Code    string
	Message string
}

func (e *InspectError) Error() string { return e.Message }

func inspectErr(code, format string, args ...any) *InspectError {
	return &InspectError{Code: code, Message: fmt.Sprintf(format, args...)}
}

// Stable inspection hard-failure codes.
const (
	InspectErrManifestInvalid      = "manifest_invalid"
	InspectErrManifestVersion      = "manifest_unsupported_version"
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
	InspectErrPageMissingID        = "page_missing_external_id"
	InspectErrDuplicatePageID      = "page_duplicate_external_id"
	InspectErrPageMissingTitle     = "page_missing_title"
	InspectErrPageTitleTooLong     = "page_title_too_long"
	InspectErrParentNotSeen        = "page_parent_not_seen"
	InspectErrCycle                = "page_cycle"
	InspectErrDepthExceeded        = "page_depth_exceeded"
	InspectErrTipTap               = "page_content_invalid"
	InspectErrSpaceKeyMismatch     = "space_key_mismatch"
	InspectErrSpaceKeyMissing      = "space_key_missing"
	InspectErrCommentMissingPageID = "comment_missing_page_id"
	InspectErrAttachmentPath       = "attachment_invalid_path"
	InspectErrCountMismatch        = "manifest_count_mismatch"
	InspectErrHash                 = "hash_failed"
)

// Inspection issue severities.
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
	// imported in this release. This is the plan's partial-scope code (section 20.2), distinct from
	// the attachment_placeholder_not_imported link code used when a CONF_ATTACHMENT placeholder is
	// discovered in a link (section 13).
	IssueAttachmentsNotImported = "attachments_not_imported"
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

// StagedPage is one normalized page ready to persist to DOCS_ImportStagedPage. It holds no HTTP or
// DB types; the store maps it to a row.
type StagedPage struct {
	Ordinal               int
	ExternalID            string
	ParentExternalID      string
	SourceOrdinal         int
	Title                 string
	CanonicalBody         string
	SearchText            string
	SourceUserProposal    string
	SourceAuthorAccountID string
	SourceCreateAt        int64
	SourceUpdateAt        int64
	SourceProps           map[string]any
	IncomingSourceHash    string
	// Links are the placeholders discovered in this page's approved attributes and text. Not
	// persisted verbatim; used for link counts and per-page link issues during preflight.
	Links []DiscoveredLink
}

// RestrictedSummary partitions manifest restricted entries by whether they intersect emitted pages.
type RestrictedSummary struct {
	ManifestTotal   int      `json:"restricted_manifest_total"`
	EmittedPages    int      `json:"restricted_emitted_pages"`
	ManifestOnly    int      `json:"restricted_manifest_only"`
	EmittedIDs      []string `json:"restricted_emitted_ids,omitempty"`
	ManifestOnlyIDs []string `json:"restricted_manifest_only_ids,omitempty"`
}

// InspectionResult is the complete synchronous inspection output.
type InspectionResult struct {
	Version          int
	OrganizationID   string
	SpaceKey         string
	SpaceName        string
	SpaceTitle       string
	SpaceDescription string

	Pages           []StagedPage
	CommentCount    int
	AttachmentCount int
	Restricted      RestrictedSummary

	Manifest    *Manifest
	JSONLSha256 string

	Issues []InspectionIssue
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
// implausibly in the future — clock skew between the source and this server should be well under a
// day.
const futureTimestampAllowance = int64(24 * 60 * 60 * 1000)

// year2100Millis is the fallback future ceiling used when InspectOptions.Now is not supplied.
const year2100Millis = int64(4102444800000)

// futureCeiling returns the largest source timestamp treated as plausible: now plus a skew
// allowance when now is supplied, otherwise the fixed year-2100 ceiling.
func (o InspectOptions) futureCeiling() int64 {
	if o.Now > 0 {
		return o.Now + futureTimestampAllowance
	}
	return year2100Millis
}

// parsing states for the JSONL sequence.
type parseState int

const (
	stateVersion parseState = iota
	stateSpace
	statePages
	stateComments
	stateDone
)

// Inspect performs full synchronous inspection of an already-safely-decompressed bundle: it parses
// and validates the manifest and JSONL, verifies the JSONL checksum, normalizes pages, and
// reconciles counts. A hard failure returns an *InspectError and no result; recoverable findings
// are collected as issues on the result.
func Inspect(contents *ArchiveContents, opts InspectOptions) (*InspectionResult, error) {
	manifest, err := parseManifest(contents.ManifestBytes)
	if err != nil {
		return nil, err
	}

	// Verify JSONL integrity before doing anything else with the data.
	if manifest.Checksums.JSONLSha256 == "" {
		return nil, inspectErr(InspectErrChecksumMissing, "manifest is missing checksums.jsonl_sha256")
	}
	if !strings.EqualFold(manifest.Checksums.JSONLSha256, contents.JSONLSha256) {
		return nil, inspectErr(InspectErrChecksumMismatch, "import.jsonl checksum does not match the manifest")
	}

	// A producer that reported its own errors yields a failed upload.
	if len(manifest.Errors) > 0 {
		return nil, inspectErr(InspectErrManifestHasErrors, "manifest reports %d producer error(s): %s", len(manifest.Errors), manifest.Errors[0])
	}

	// A source space key is mandatory: it becomes the ImportSource's ExternalSpaceKey, which
	// ImportSource.IsValid requires. The producer omits it only when neither an organization id nor
	// a space key is known — a bundle we cannot map, so reject it here rather than fail later.
	if manifest.Source.SpaceKey == "" {
		return nil, inspectErr(InspectErrSpaceKeyMissing, "manifest source is missing a space key")
	}

	res := &InspectionResult{
		Manifest:       manifest,
		JSONLSha256:    contents.JSONLSha256,
		OrganizationID: manifest.Source.OrganizationID,
		SpaceKey:       manifest.Source.SpaceKey,
		SpaceName:      manifest.Source.SpaceName,
	}

	// Copy manifest warnings into issues, bounded by MaxManifestWarnings so a manifest packed with
	// warnings cannot force unbounded issue allocation.
	warnings := manifest.Warnings
	suppressed := 0
	if len(warnings) > MaxManifestWarnings {
		suppressed = len(warnings) - MaxManifestWarnings
		warnings = warnings[:MaxManifestWarnings]
	}
	for _, w := range warnings {
		res.Issues = append(res.Issues, InspectionIssue{
			Severity: SeverityWarning, Code: IssueManifestWarning, Message: w,
			Remediation: "Review the producer warning; it does not block import.",
		})
	}
	if suppressed > 0 {
		res.Issues = append(res.Issues, InspectionIssue{
			Severity: SeverityWarning, Code: IssueManifestWarning,
			Message:     fmt.Sprintf("%d additional manifest warnings were suppressed (limit %d)", suppressed, MaxManifestWarnings),
			Remediation: "Only the first manifest warnings are reported individually.",
			Details:     map[string]any{"suppressed": suppressed, "limit": MaxManifestWarnings},
		})
	}
	// Attachments are never verified in this release.
	if manifest.Checksums.AttachmentsSha256 != "" {
		res.Issues = append(res.Issues, InspectionIssue{
			Severity: SeverityInfo, Code: IssueAttachmentChecksumNotVerified,
			Message:     "attachment checksum not verified: attachments are out of scope in this release",
			Remediation: "Attachment bytes are neither extracted nor verified; import attachments in a future release.",
			Details:     map[string]any{"reason": "attachments_out_of_scope"},
		})
	}

	if err := parseJSONL(contents.JSONLBytes, manifest, opts, res); err != nil {
		return nil, err
	}

	if err := reconcileCounts(manifest, res); err != nil {
		return nil, err
	}
	summarizeRestricted(manifest, res)

	return res, nil
}

// parseManifest decodes and version-checks the manifest.
func parseManifest(b []byte) (*Manifest, error) {
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
	return &m, nil
}

// lineHasForeignPayload reports whether the line carries a payload field that does not belong to
// its declared type. The version line owns both the version and source fields; every other type
// owns exactly its matching payload. A line declaring one type but carrying another type's payload
// (e.g. {"type":"page","page":{...},"space":{...}}) is rejected so a malformed or smuggled payload
// cannot ride along unnoticed.
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

// parseJSONL runs the strict v2 line sequence state machine, normalizing pages into res.
func parseJSONL(b []byte, manifest *Manifest, opts InspectOptions, res *InspectionResult) error {
	lines := splitJSONLLines(b)
	if len(lines) == 0 {
		return inspectErr(InspectErrJSONLEmpty, "import.jsonl is empty")
	}

	state := stateVersion
	seenPageIDs := make(map[string]struct{})
	parentOf := make(map[string]string)
	siblingCounter := make(map[string]int) // parent external ID ("" for roots) -> next sibling ordinal
	teamValues := make(map[string]struct{})
	// The manifest's advisory target team is also compared against the requested team, alongside the
	// per-line team values, so a mismatch declared only in the manifest is still surfaced.
	collectTeam(teamValues, manifest.Target.Team)

	pageOrdinal := 0

	for i, raw := range lines {
		if raw == "" {
			return inspectErr(InspectErrBlankLine, "import.jsonl line %d is blank", i+1)
		}
		if len(raw) > MaxJSONLLineBytes {
			return inspectErr(InspectErrLineTooLong, "import.jsonl line %d exceeds %d bytes", i+1, MaxJSONLLineBytes)
		}

		var line Line
		if err := json.Unmarshal([]byte(raw), &line); err != nil {
			return inspectErr(InspectErrLineInvalid, "import.jsonl line %d is invalid JSON: %v", i+1, err)
		}
		if lineHasForeignPayload(&line, line.Type) {
			return inspectErr(InspectErrPayloadMismatch, "import.jsonl line %d declares type %q but carries another type's payload", i+1, line.Type)
		}

		switch line.Type {
		case LineTypeVersion:
			if state != stateVersion {
				return inspectErr(InspectErrSequence, "unexpected version line at line %d", i+1)
			}
			if line.Version == nil || *line.Version != ContractVersion {
				return inspectErr(InspectErrVersionValue, "line %d: version must be %d", i+1, ContractVersion)
			}
			if line.Source != nil {
				if err := requireSameSpaceKey("version.source.space_key", stringOrEmpty(line.Source.SpaceKey), manifest.Source.SpaceKey); err != nil {
					return err
				}
			}
			res.Version = *line.Version
			state = stateSpace

		case LineTypeSpace:
			if state != stateSpace {
				return inspectErr(InspectErrSequence, "unexpected space line at line %d", i+1)
			}
			if line.Space == nil {
				return inspectErr(InspectErrPayloadMismatch, "line %d declares type space but has no space payload", i+1)
			}
			if err := handleSpaceLine(line.Space, manifest, teamValues, res); err != nil {
				return err
			}
			state = statePages

		case LineTypePage:
			if state != statePages {
				return inspectErr(InspectErrSequence, "unexpected page line at line %d (pages must follow the space line and precede comments)", i+1)
			}
			if line.Page == nil {
				return inspectErr(InspectErrPayloadMismatch, "line %d declares type page but has no page payload", i+1)
			}
			if len(res.Pages) >= MaxPages {
				return inspectErr(InspectErrTooManyPages, "bundle has more than %d pages", MaxPages)
			}
			sp, err := normalizePage(line.Page, manifest, i+1, pageOrdinal, seenPageIDs, parentOf, siblingCounter, teamValues, opts.futureCeiling(), res)
			if err != nil {
				return err
			}
			res.Pages = append(res.Pages, *sp)
			pageOrdinal++

		case LineTypePageComment:
			if state != statePages && state != stateComments {
				return inspectErr(InspectErrSequence, "unexpected page_comment line at line %d", i+1)
			}
			state = stateComments
			if line.PageComment == nil {
				return inspectErr(InspectErrPayloadMismatch, "line %d declares type page_comment but has no payload", i+1)
			}
			if stringOrEmpty(line.PageComment.PageImportSourceID) == "" {
				return inspectErr(InspectErrCommentMissingPageID, "line %d: page_comment is missing page_import_source_id", i+1)
			}
			res.CommentCount++

		case LineTypeResolveSpacePlaceholders:
			if state != statePages && state != stateComments {
				return inspectErr(InspectErrSequence, "unexpected resolve_space_placeholders line at line %d", i+1)
			}
			if line.ResolveSpacePlaceholders == nil {
				return inspectErr(InspectErrPayloadMismatch, "line %d declares type resolve_space_placeholders but has no payload", i+1)
			}
			collectTeam(teamValues, stringOrEmpty(line.ResolveSpacePlaceholders.Team))
			state = stateDone

		case "":
			return inspectErr(InspectErrUnknownType, "line %d has an empty type", i+1)
		default:
			return inspectErr(InspectErrUnknownType, "line %d has unknown type %q", i+1, line.Type)
		}
	}

	if state != stateDone {
		return inspectErr(InspectErrSequence, "import.jsonl is missing the trailing resolve_space_placeholders line")
	}

	// Independently verify hierarchy depth over the assembled parent map.
	if err := verifyHierarchy(res.Pages, parentOf); err != nil {
		return err
	}

	emitTeamMismatch(teamValues, opts, res)
	return nil
}

// splitJSONLLines splits on newline and drops exactly one trailing terminator newline, so a normal
// file ending in "\n" is not treated as having a blank final line. Any other empty element remains
// and is rejected as a blank line by the caller.
func splitJSONLLines(b []byte) []string {
	s := string(b)
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// handleSpaceLine records the space defaults and validates the space key against the manifest.
func handleSpaceLine(space *SpaceData, manifest *Manifest, teamValues map[string]struct{}, res *InspectionResult) error {
	collectTeam(teamValues, stringOrEmpty(space.Team))
	res.SpaceTitle = stringOrEmpty(space.Title)
	res.SpaceDescription = stringOrEmpty(space.Description)

	spaceKey := propString(derefProps(space.Props), PropImportSourceID)
	if err := requireSameSpaceKey("space.props.import_source_id", spaceKey, manifest.Source.SpaceKey); err != nil {
		return err
	}
	return nil
}

// normalizePage validates and normalizes one page line into a StagedPage.
func normalizePage(
	page *PageData, manifest *Manifest, lineNo, ordinal int,
	seenPageIDs map[string]struct{}, parentOf map[string]string,
	siblingCounter map[string]int, teamValues map[string]struct{}, futureCeiling int64, res *InspectionResult,
) (*StagedPage, error) {
	collectTeam(teamValues, stringOrEmpty(page.Team))

	props := derefProps(page.Props)
	externalID := propString(props, PropImportSourceID)
	if externalID == "" {
		return nil, inspectErr(InspectErrPageMissingID, "line %d: page is missing props.import_source_id", lineNo)
	}
	if _, dup := seenPageIDs[externalID]; dup {
		return nil, inspectErr(InspectErrDuplicatePageID, "line %d: duplicate page external id %q", lineNo, externalID)
	}

	// Normalize the title exactly as Page.PreSave does (SanitizeUnicode then trim). Import uses a
	// dedicated store path that bypasses PreSave, so without this the stored title could carry
	// unsafe Unicode controls, and — more subtly — the incoming source hash would be computed on a
	// value that never matches the applied hash of the normalized title, causing spurious conflicts.
	title := strings.TrimSpace(mmmodel.SanitizeUnicode(stringOrEmpty(page.Title)))
	if title == "" {
		return nil, inspectErr(InspectErrPageMissingTitle, "line %d: page %q is missing a title", lineNo, externalID)
	}
	// Reject an over-long title at inspection so it fails early here rather than at execution, where
	// Page.IsValid enforces the same PageTitleMaxRunes bound.
	if utf8.RuneCountInString(title) > model.PageTitleMaxRunes {
		return nil, inspectErr(InspectErrPageTitleTooLong, "line %d: page %q title exceeds %d runes", lineNo, externalID, model.PageTitleMaxRunes)
	}

	// Space key must match the manifest.
	if err := requireSameSpaceKey(fmt.Sprintf("page %q space_import_source_id", externalID), stringOrEmpty(page.SpaceImportSourceID), manifest.Source.SpaceKey); err != nil {
		return nil, err
	}

	parentID := stringOrEmpty(page.ParentImportSourceID)
	if parentID != "" {
		if _, seen := seenPageIDs[parentID]; !seen {
			return nil, inspectErr(InspectErrParentNotSeen, "line %d: page %q references parent %q that has not appeared earlier", lineNo, externalID, parentID)
		}
	}

	// Decode and canonicalize the TipTap body independently of the line.
	canonicalBody, searchText, links, tErr := CanonicalizeAndExtractSearchText(stringOrEmpty(page.Content))
	if tErr != nil {
		return nil, inspectErr(InspectErrTipTap, "line %d: page %q content invalid: %v", lineNo, externalID, tErr)
	}

	// Timestamp validation (does not discard the page).
	sourceCreateAt := int64OrZero(page.CreateAt)
	if !plausibleTimestamp(sourceCreateAt, futureCeiling) {
		res.Issues = append(res.Issues, InspectionIssue{
			Severity: SeverityWarning, Code: IssueSourceCreateAtInvalid, ExternalID: externalID, Title: title,
			Message:     "source create timestamp is missing, non-positive, or implausibly in the future",
			Remediation: "The page is staged; execution falls back to the import time for CreateAt.",
			Details:     map[string]any{"source_create_at": sourceCreateAt},
		})
	}
	// update_at issue is emitted only when supplied but unusable.
	if page.UpdateAt != nil && !plausibleTimestamp(*page.UpdateAt, futureCeiling) {
		res.Issues = append(res.Issues, InspectionIssue{
			Severity: SeverityWarning, Code: IssueSourceUpdateAtInvalid, ExternalID: externalID, Title: title,
			Message:     "source update timestamp was supplied but is not usable",
			Remediation: "The raw value is preserved in props; it does not affect local timestamps.",
			Details:     map[string]any{"source_update_at": *page.UpdateAt},
		})
	}

	sourceProps := allowlistSourceProps(props)

	incomingHash, hErr := HashSourceState(SourceStateHashInput{
		Title:            title,
		CanonicalBody:    canonicalBody,
		ParentExternalID: parentID,
		AuthorAccountID:  propString(props, PropConfluenceAuthorAccountID),
		AuthorProposal:   stringOrEmpty(page.User),
		SourceCreateAt:   sourceCreateAt,
		SourceUpdateAt:   int64OrZero(page.UpdateAt),
		SourceProps:      sourceProps,
	})
	if hErr != nil {
		return nil, inspectErr(InspectErrHash, "line %d: failed to hash page %q: %v", lineNo, externalID, hErr)
	}

	// Count attachments and validate their paths (never opened).
	if page.Attachments != nil {
		for _, att := range *page.Attachments {
			p := stringOrEmpty(att.Path)
			if err := validateAttachmentPath(p, externalID, lineNo); err != nil {
				return nil, err
			}
			res.AttachmentCount++
		}
		if len(*page.Attachments) > 0 {
			res.Issues = append(res.Issues, InspectionIssue{
				Severity: SeverityInfo, Code: IssueAttachmentsNotImported, ExternalID: externalID, Title: title,
				Message:     fmt.Sprintf("%d attachment(s) counted but not imported in this release", len(*page.Attachments)),
				Remediation: "Attachment import is a future release; bytes are neither extracted nor stored.",
			})
		}
	}

	// Report placeholders that appeared in ordinary text (never rewritten).
	for _, l := range links {
		if l.InText {
			res.Issues = append(res.Issues, InspectionIssue{
				Severity: SeverityInfo, Code: IssuePlaceholderInText, ExternalID: externalID, Title: title,
				Message:     "a Confluence placeholder token appears in ordinary text and is left intact",
				Remediation: "Placeholders in text are not rewritten in this release.",
			})
			break
		}
	}

	seenPageIDs[externalID] = struct{}{}
	parentOf[externalID] = parentID
	sourceOrdinal := siblingCounter[parentID]
	siblingCounter[parentID] = sourceOrdinal + 1

	return &StagedPage{
		Ordinal:               ordinal,
		ExternalID:            externalID,
		ParentExternalID:      parentID,
		SourceOrdinal:         sourceOrdinal,
		Title:                 title,
		CanonicalBody:         canonicalBody,
		SearchText:            searchText,
		SourceUserProposal:    stringOrEmpty(page.User),
		SourceAuthorAccountID: propString(props, PropConfluenceAuthorAccountID),
		SourceCreateAt:        sourceCreateAt,
		SourceUpdateAt:        int64OrZero(page.UpdateAt),
		SourceProps:           sourceProps,
		IncomingSourceHash:    incomingHash,
		Links:                 links,
	}, nil
}

// allowlistSourceProps copies only the allowlisted source props, never arbitrary future producer
// fields. Returns a fresh map (nil-safe).
func allowlistSourceProps(props map[string]any) map[string]any {
	out := make(map[string]any)
	for _, key := range AllowlistedSourceProps {
		if v, ok := props[key]; ok {
			out[key] = v
		}
	}
	return out
}

// MaxHierarchyDepth is the maximum page depth the importer accepts, mirroring app.MaxPageDepth: a
// root page is depth 1, so a chain of 10 pages is the deepest allowed and an 11-page chain is
// rejected. The value is duplicated (rather than imported from the app package) to keep this pure
// package free of a dependency cycle; both must move together.
const MaxHierarchyDepth = 10

// verifyHierarchy independently recomputes each page's depth from the parent map, rejecting cycles,
// missing parents, and depth greater than MaxHierarchyDepth. It counts the root as depth 1 so the
// bound matches the app-layer depth enforced at execution, and does not trust any producer
// flattening claim.
func verifyHierarchy(pages []StagedPage, parentOf map[string]string) error {
	for _, p := range pages {
		depth := 1 // the page itself counts as one level; root is depth 1
		cur := p.ExternalID
		for {
			parent := parentOf[cur]
			if parent == "" {
				break
			}
			if _, ok := parentOf[parent]; !ok {
				return inspectErr(InspectErrParentNotSeen, "page %q references missing parent %q", p.ExternalID, parent)
			}
			depth++
			if depth > MaxHierarchyDepth {
				return inspectErr(InspectErrDepthExceeded, "page %q exceeds maximum hierarchy depth of %d", p.ExternalID, MaxHierarchyDepth)
			}
			if depth > len(parentOf)+1 {
				return inspectErr(InspectErrCycle, "page %q is part of a parent cycle", p.ExternalID)
			}
			cur = parent
		}
	}
	return nil
}

// requireSameSpaceKey rejects a non-empty space key that differs from the manifest's. An empty
// value is tolerated (some lines may omit it); only a present-and-different key is a mismatch.
func requireSameSpaceKey(where, value, manifestKey string) error {
	if value == "" || manifestKey == "" {
		return nil
	}
	if value != manifestKey {
		return inspectErr(InspectErrSpaceKeyMismatch, "%s (%q) does not match manifest source space key (%q)", where, value, manifestKey)
	}
	return nil
}

// validateAttachmentPath rejects an unsafe attachment path without opening the file.
func validateAttachmentPath(p, externalID string, lineNo int) error {
	if p == "" {
		return inspectErr(InspectErrAttachmentPath, "line %d: page %q has an attachment with an empty path", lineNo, externalID)
	}
	if strings.ContainsRune(p, 0) || strings.Contains(p, "\\") || strings.HasPrefix(p, "/") {
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
func reconcileCounts(manifest *Manifest, res *InspectionResult) error {
	check := func(name string, parsed, declared int) error {
		if declared != parsed {
			return inspectErr(InspectErrCountMismatch, "manifest declares %d %s but %d were parsed", declared, name, parsed)
		}
		return nil
	}
	if err := check("pages", len(res.Pages), manifest.Counts.Pages); err != nil {
		return err
	}
	if err := check("comments", res.CommentCount, manifest.Counts.Comments); err != nil {
		return err
	}
	return check("attachments", res.AttachmentCount, manifest.Counts.Attachments)
}

// summarizeRestricted intersects the manifest restricted list with emitted (staged) page IDs.
func summarizeRestricted(manifest *Manifest, res *InspectionResult) {
	staged := make(map[string]struct{}, len(res.Pages))
	for _, p := range res.Pages {
		staged[p.ExternalID] = struct{}{}
	}
	seen := make(map[string]struct{})
	for _, rp := range manifest.RestrictedPages {
		if _, dup := seen[rp.ID]; dup {
			continue
		}
		seen[rp.ID] = struct{}{}
		res.Restricted.ManifestTotal++
		if _, ok := staged[rp.ID]; ok {
			res.Restricted.EmittedPages++
			res.Restricted.EmittedIDs = append(res.Restricted.EmittedIDs, rp.ID)
		} else {
			res.Restricted.ManifestOnly++
			res.Restricted.ManifestOnlyIDs = append(res.Restricted.ManifestOnlyIDs, rp.ID)
		}
	}
}

// collectTeam records a non-empty advisory team value.
func collectTeam(teams map[string]struct{}, team string) {
	if team != "" {
		teams[team] = struct{}{}
	}
}

// emitTeamMismatch adds one aggregate warning when any advisory bundle team differs from the
// requested team name. It never reroutes.
func emitTeamMismatch(teams map[string]struct{}, opts InspectOptions, res *InspectionResult) {
	if opts.RequestedTeamName == "" {
		return
	}
	var mismatched []string
	for t := range teams {
		if t != opts.RequestedTeamName {
			mismatched = append(mismatched, t)
		}
	}
	if len(mismatched) > 0 {
		res.Issues = append(res.Issues, InspectionIssue{
			Severity: SeverityWarning, Code: IssueBundleTeamMismatch,
			Message:     "advisory bundle team values differ from the requested target team",
			Remediation: "The requested target team is authoritative; the bundle team is advisory only.",
			Details:     map[string]any{"requested_team": opts.RequestedTeamName, "bundle_teams": mismatched},
		})
	}
}

// plausibleTimestamp reports whether ms is a positive epoch-millis value at or below futureCeiling
// (now + a skew allowance, or the year-2100 fallback). A non-positive or too-far-future value is
// implausible.
func plausibleTimestamp(ms, futureCeiling int64) bool {
	return ms > 0 && ms <= futureCeiling
}
