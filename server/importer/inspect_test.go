// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package importer

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"strconv"
	"strings"
	"testing"

	mmmodel "github.com/mattermost/mattermost/server/public/model"
)

// nopWriteCloser adapts an io.Writer to io.WriteCloser for a passthrough zip compressor in tests.
type nopWriteCloser struct{ w io.Writer }

func (n nopWriteCloser) Write(p []byte) (int, error) { return n.w.Write(p) }
func (n nopWriteCloser) Close() error                { return nil }

// inspectErrCode returns the stable code of an *InspectError or *ArchiveError, or "" otherwise.
func inspectErrCode(err error) string {
	var ie *InspectError
	if errors.As(err, &ie) {
		return ie.Code
	}
	var ae *ArchiveError
	if errors.As(err, &ae) {
		return ae.Code
	}
	return ""
}

func validBundle(t *testing.T) *bundleBuilder {
	t.Helper()
	jsonl := joinLines(
		versionLine(),
		spaceLine(),
		pageLine(t, "100", "", "Home", docString("Welcome home")),
		pageLine(t, "101", "100", "Child", docString("A child page")),
		`{"type":"page_comment","page_comment":{"page_import_source_id":"100","user":"jdoe","content":"hi","create_at":1704625200000,"props":{"import_source_id":"201"}}}`,
		resolveLine(),
	)
	return newBundle(jsonl, baseManifest(2, 1, 0))
}

func TestInspect_ValidBundle(t *testing.T) {
	res, err := validBundle(t).inspect(t, InspectOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Version() != 2 {
		t.Errorf("version = %d, want 2", res.Version())
	}
	if len(res.Pages) != 2 {
		t.Fatalf("pages = %d, want 2", len(res.Pages))
	}
	if res.CommentCount() != 1 {
		t.Errorf("comments = %d, want 1", res.CommentCount())
	}
	if res.SpaceKey() != "DOCS" {
		t.Errorf("space key = %q, want DOCS", res.SpaceKey())
	}
	if res.SpaceTitle() != "Docs" {
		t.Errorf("space title = %q, want Docs", res.SpaceTitle())
	}
	// Root then child; child's parent + sibling ordinals.
	if res.Pages[0].ExternalID != "100" || res.Pages[1].ExternalID != "101" {
		t.Errorf("page order = %q, %q", res.Pages[0].ExternalID, res.Pages[1].ExternalID)
	}
	if res.Pages[1].ParentExternalID != "100" {
		t.Errorf("child parent = %q, want 100", res.Pages[1].ParentExternalID)
	}
	if res.Pages[0].IncomingSourceContentHash == "" || !IsValidSHA256Hex(res.Pages[0].IncomingSourceContentHash) {
		t.Errorf("incoming hash invalid: %q", res.Pages[0].IncomingSourceContentHash)
	}
}

func TestInspect_ChecksumMismatch(t *testing.T) {
	b := validBundle(t)
	b.manifest.Checksums.JSONLSha256 = strings.Repeat("a", 64)
	b.skipChecksum = true
	_, err := b.inspect(t, InspectOptions{})
	if got := inspectErrCode(err); got != InspectErrChecksumMismatch {
		t.Fatalf("code = %q, want %q", got, InspectErrChecksumMismatch)
	}
}

func TestInspect_MissingChecksum(t *testing.T) {
	b := validBundle(t)
	b.skipChecksum = true // leave checksum empty
	_, err := b.inspect(t, InspectOptions{})
	if got := inspectErrCode(err); got != InspectErrChecksumMissing {
		t.Fatalf("code = %q, want %q", got, InspectErrChecksumMissing)
	}
}

func TestInspect_ManifestReportsErrors(t *testing.T) {
	b := validBundle(t)
	b.manifest.Errors = []string{"conversion failed"}
	_, err := b.inspect(t, InspectOptions{})
	if got := inspectErrCode(err); got != InspectErrManifestHasErrors {
		t.Fatalf("code = %q, want %q", got, InspectErrManifestHasErrors)
	}
}

func TestInspect_WrongManifestVersion(t *testing.T) {
	b := validBundle(t)
	b.manifest.Version = "1"
	_, err := b.inspect(t, InspectOptions{})
	if got := inspectErrCode(err); got != InspectErrManifestVersion {
		t.Fatalf("code = %q, want %q", got, InspectErrManifestVersion)
	}
}

func TestInspect_WrongVersionLine(t *testing.T) {
	jsonl := joinLines(
		`{"type":"version","version":3,"source":{"space_key":"DOCS"}}`,
		spaceLine(),
		resolveLine(),
	)
	_, err := newBundle(jsonl, baseManifest(0, 0, 0)).inspect(t, InspectOptions{})
	if got := inspectErrCode(err); got != InspectErrVersionValue {
		t.Fatalf("code = %q, want %q", got, InspectErrVersionValue)
	}
}

func TestInspect_BadSequence_PageBeforeSpace(t *testing.T) {
	jsonl := joinLines(
		versionLine(),
		pageLine(t, "100", "", "Home", docString("x")),
		spaceLine(),
		resolveLine(),
	)
	_, err := newBundle(jsonl, baseManifest(1, 0, 0)).inspect(t, InspectOptions{})
	if got := inspectErrCode(err); got != InspectErrSequence {
		t.Fatalf("code = %q, want %q", got, InspectErrSequence)
	}
}

func TestInspect_MissingResolveLine(t *testing.T) {
	jsonl := joinLines(versionLine(), spaceLine(), pageLine(t, "100", "", "Home", docString("x")))
	_, err := newBundle(jsonl, baseManifest(1, 0, 0)).inspect(t, InspectOptions{})
	if got := inspectErrCode(err); got != InspectErrSequence {
		t.Fatalf("code = %q, want %q", got, InspectErrSequence)
	}
}

func TestInspect_UnknownType(t *testing.T) {
	jsonl := joinLines(versionLine(), spaceLine(), `{"type":"widget"}`, resolveLine())
	_, err := newBundle(jsonl, baseManifest(0, 0, 0)).inspect(t, InspectOptions{})
	if got := inspectErrCode(err); got != InspectErrUnknownType {
		t.Fatalf("code = %q, want %q", got, InspectErrUnknownType)
	}
}

func TestInspect_BlankLine(t *testing.T) {
	jsonl := versionLine() + "\n" + spaceLine() + "\n\n" + resolveLine() + "\n"
	_, err := newBundle(jsonl, baseManifest(0, 0, 0)).inspect(t, InspectOptions{})
	if got := inspectErrCode(err); got != InspectErrBlankLine {
		t.Fatalf("code = %q, want %q", got, InspectErrBlankLine)
	}
}

func TestInspect_TrailingLineAfterResolve(t *testing.T) {
	jsonl := joinLines(versionLine(), spaceLine(), resolveLine(), spaceLine())
	_, err := newBundle(jsonl, baseManifest(0, 0, 0)).inspect(t, InspectOptions{})
	if got := inspectErrCode(err); got != InspectErrSequence {
		t.Fatalf("code = %q, want %q", got, InspectErrSequence)
	}
}

func TestInspect_DuplicatePageID(t *testing.T) {
	jsonl := joinLines(
		versionLine(), spaceLine(),
		pageLine(t, "100", "", "A", docString("a")),
		pageLine(t, "100", "", "B", docString("b")),
		resolveLine(),
	)
	_, err := newBundle(jsonl, baseManifest(2, 0, 0)).inspect(t, InspectOptions{})
	if got := inspectErrCode(err); got != InspectErrDuplicatePageID {
		t.Fatalf("code = %q, want %q", got, InspectErrDuplicatePageID)
	}
}

func TestInspect_ChildBeforeParent(t *testing.T) {
	jsonl := joinLines(
		versionLine(), spaceLine(),
		pageLine(t, "101", "100", "Child", docString("c")),
		pageLine(t, "100", "", "Parent", docString("p")),
		resolveLine(),
	)
	_, err := newBundle(jsonl, baseManifest(2, 0, 0)).inspect(t, InspectOptions{})
	if got := inspectErrCode(err); got != InspectErrParentNotSeen {
		t.Fatalf("code = %q, want %q", got, InspectErrParentNotSeen)
	}
}

func TestInspect_MissingParent(t *testing.T) {
	jsonl := joinLines(
		versionLine(), spaceLine(),
		pageLine(t, "100", "999", "Orphan", docString("o")),
		resolveLine(),
	)
	_, err := newBundle(jsonl, baseManifest(1, 0, 0)).inspect(t, InspectOptions{})
	if got := inspectErrCode(err); got != InspectErrParentNotSeen {
		t.Fatalf("code = %q, want %q", got, InspectErrParentNotSeen)
	}
}

func TestInspect_DepthExceeded(t *testing.T) {
	// A chain of 11 pages puts the deepest page at depth 11 (root is depth 1), which exceeds the
	// depth-10 limit and must be rejected — matching the app-layer bound enforced at execution.
	lines := []string{versionLine(), spaceLine()}
	prev := ""
	for i := 0; i <= 10; i++ {
		id := string(rune('a'+i)) + "id"
		lines = append(lines, pageLine(t, id, prev, "P", docString("x")))
		prev = id
	}
	lines = append(lines, resolveLine())
	jsonl := joinLines(lines...)
	_, err := newBundle(jsonl, baseManifest(11, 0, 0)).inspect(t, InspectOptions{})
	if got := inspectErrCode(err); got != InspectErrDepthExceeded {
		t.Fatalf("code = %q, want %q", got, InspectErrDepthExceeded)
	}
}

func TestInspect_DepthTenAllowed(t *testing.T) {
	// A chain of 10 pages reaches depth 10 (root is depth 1), the deepest allowed.
	lines := []string{versionLine(), spaceLine()}
	prev := ""
	for i := 0; i <= 9; i++ {
		id := string(rune('a'+i)) + "id"
		lines = append(lines, pageLine(t, id, prev, "P", docString("x")))
		prev = id
	}
	lines = append(lines, resolveLine())
	jsonl := joinLines(lines...)
	_, err := newBundle(jsonl, baseManifest(10, 0, 0)).inspect(t, InspectOptions{})
	if err != nil {
		t.Fatalf("depth 10 should be allowed, got %v", err)
	}
}

func TestInspect_CountsAndAttachments(t *testing.T) {
	pageWithAtt := `{"type":"page","page":{"space_import_source_id":"DOCS","user":"j","title":"WithAtt","content":` +
		mustQuote(docString("x")) +
		`,"props":{"import_source_id":"200"},"attachments":[{"path":"200/a.png"},{"path":"200/b.png"}]}}`
	jsonl := joinLines(versionLine(), spaceLine(), pageWithAtt, resolveLine())
	res, err := newBundle(jsonl, baseManifest(1, 0, 2)).inspect(t, InspectOptions{})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if res.AttachmentCount() != 2 {
		t.Errorf("attachments = %d, want 2", res.AttachmentCount())
	}
	if !hasIssue(res, IssueAttachmentsNotImported) {
		t.Errorf("expected attachments_not_imported issue")
	}
}

func TestInspect_ManifestCountMismatch(t *testing.T) {
	// A checksum-valid JSONL with one page but a manifest declaring five is a corrupt/inconsistent
	// producer bundle and must be rejected, not merely warned about.
	_, err := newBundle(
		joinLines(versionLine(), spaceLine(), pageLine(t, "100", "", "H", docString("x")), resolveLine()),
		baseManifest(5, 0, 0), // declares 5 pages, but only 1 parsed
	).inspect(t, InspectOptions{})
	if got := inspectErrCode(err); got != InspectErrCountMismatch {
		t.Fatalf("code = %q, want %q", got, InspectErrCountMismatch)
	}
}

func TestInspect_RestrictedPages(t *testing.T) {
	b := newBundle(
		joinLines(versionLine(), spaceLine(),
			pageLine(t, "100", "", "H", docString("x")),
			pageLine(t, "101", "100", "C", docString("y")),
			resolveLine()),
		baseManifest(2, 0, 0),
	)
	b.manifest.RestrictedPages = []ManifestRestrictedPage{
		{ID: "100", Title: "H"},    // emitted
		{ID: "999", Title: "Gone"}, // manifest-only
	}
	res, err := b.inspect(t, InspectOptions{})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if res.Restricted().ManifestTotal != 2 || res.Restricted().EmittedPages != 1 || res.Restricted().ManifestOnly != 1 {
		t.Errorf("restricted summary = %+v", res.Restricted())
	}
}

func TestInspect_TeamMismatch(t *testing.T) {
	res, err := validBundle(t).inspect(t, InspectOptions{RequestedTeamName: "other-team"})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if !hasIssue(res, IssueBundleTeamMismatch) {
		t.Errorf("expected bundle_team_mismatch issue")
	}
}

func TestInspect_TeamMatchNoIssue(t *testing.T) {
	res, err := validBundle(t).inspect(t, InspectOptions{RequestedTeamName: "myteam"})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if hasIssue(res, IssueBundleTeamMismatch) {
		t.Errorf("did not expect bundle_team_mismatch issue")
	}
}

func TestInspect_TeamMismatchFromManifestTargetOnly(t *testing.T) {
	// Build a bundle whose per-line team values all match the requested team, but whose manifest
	// target team differs. The mismatch must still be surfaced from the manifest metadata.
	jsonl := joinLines(
		`{"type":"version","version":2,"source":{"space_key":"DOCS"}}`,
		`{"type":"space","space":{"team":"requested","title":"Docs","props":{"import_source_id":"DOCS"}}}`,
		`{"type":"resolve_space_placeholders","resolve_space_placeholders":{"team":"requested","space_import_source_id":"DOCS"}}`,
	)
	b := newBundle(jsonl, baseManifest(0, 0, 0))
	b.manifest.Target.Team = "manifest-team"
	res, err := b.inspect(t, InspectOptions{RequestedTeamName: "requested"})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if !hasIssue(res, IssueBundleTeamMismatch) {
		t.Errorf("expected bundle_team_mismatch from manifest target team")
	}
}

func TestInspect_MissingSourceSpaceKey(t *testing.T) {
	b := validBundle(t)
	b.manifest.Source.SpaceKey = ""
	_, err := b.inspect(t, InspectOptions{})
	if got := inspectErrCode(err); got != InspectErrSpaceKeyMissing {
		t.Fatalf("code = %q, want %q", got, InspectErrSpaceKeyMissing)
	}
}

func TestInspect_MultiPayloadLineRejected(t *testing.T) {
	// A line declaring type "page" but also carrying a "space" payload must be rejected.
	badLine := `{"type":"page","page":{"space_import_source_id":"DOCS","user":"j","title":"H","content":` +
		mustQuote(docString("x")) + `,"props":{"import_source_id":"100"}},"space":{"team":"myteam"}}`
	jsonl := joinLines(versionLine(), spaceLine(), badLine, resolveLine())
	_, err := newBundle(jsonl, baseManifest(1, 0, 0)).inspect(t, InspectOptions{})
	if got := inspectErrCode(err); got != InspectErrPayloadMismatch {
		t.Fatalf("code = %q, want %q", got, InspectErrPayloadMismatch)
	}
}

func TestInspect_ManifestTrailingJSON(t *testing.T) {
	b := validBundle(t)
	// Force a manifest body with trailing data after the object. The builder marshals the manifest,
	// so instead build the archive manually via a helper that appends trailing bytes.
	// A trailing "]" after the manifest object is the case json.Decoder.More() misses (it returns
	// false before a closing delimiter); the io.EOF check must still reject it.
	raw := b.bytesZipWithManifestSuffix(t, "]")
	archive, err := OpenArchive(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatalf("archive open failed: %v", err)
	}
	_, err = collectInspect(archive, InspectOptions{})
	if got := inspectErrCode(err); got != InspectErrManifestInvalid {
		t.Fatalf("code = %q, want %q", got, InspectErrManifestInvalid)
	}
}

func TestInspect_FutureTimestampWithNow(t *testing.T) {
	// A create_at far beyond Now+allowance is implausible and must be flagged.
	now := int64(1704106800000)
	future := now + futureTimestampAllowance + 1_000_000
	page := `{"type":"page","page":{"space_import_source_id":"DOCS","user":"j","title":"H","content":` +
		mustQuote(docString("x")) + `,"create_at":` + strconv.FormatInt(future, 10) + `,"props":{"import_source_id":"100"}}}`
	jsonl := joinLines(versionLine(), spaceLine(), page, resolveLine())
	res, err := newBundle(jsonl, baseManifest(1, 0, 0)).inspect(t, InspectOptions{Now: now})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if !hasIssue(res, IssueSourceCreateAtInvalid) {
		t.Errorf("expected source_create_at_invalid for a future timestamp beyond now+allowance")
	}
}

func TestInspect_SpaceKeyMismatch(t *testing.T) {
	// A page declaring a different space key must be rejected.
	badPage := `{"type":"page","page":{"space_import_source_id":"OTHER","user":"j","title":"H","content":` +
		mustQuote(docString("x")) + `,"props":{"import_source_id":"100"}}}`
	jsonl := joinLines(versionLine(), spaceLine(), badPage, resolveLine())
	_, err := newBundle(jsonl, baseManifest(1, 0, 0)).inspect(t, InspectOptions{})
	if got := inspectErrCode(err); got != InspectErrSpaceKeyMismatch {
		t.Fatalf("code = %q, want %q", got, InspectErrSpaceKeyMismatch)
	}
}

func TestInspect_TitleTooLong(t *testing.T) {
	longTitle := strings.Repeat("x", 300) // > model.PageTitleMaxRunes (255)
	jsonl := joinLines(versionLine(), spaceLine(),
		pageLine(t, "100", "", longTitle, docString("x")), resolveLine())
	_, err := newBundle(jsonl, baseManifest(1, 0, 0)).inspect(t, InspectOptions{})
	if got := inspectErrCode(err); got != InspectErrPageTitleTooLong {
		t.Fatalf("code = %q, want %q", got, InspectErrPageTitleTooLong)
	}
}

func TestInspect_ManifestWarningsCapped(t *testing.T) {
	b := validBundle(t)
	total := MaxManifestWarnings + 500
	b.manifest.Warnings = make([]string, total)
	for i := range b.manifest.Warnings {
		b.manifest.Warnings[i] = "w"
	}
	res, err := b.inspect(t, InspectOptions{})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	warnIssues := 0
	sawSuppressionNote := false
	for _, is := range res.Issues {
		if is.Code == IssueManifestWarning {
			warnIssues++
			if is.Details != nil {
				if _, ok := is.Details["suppressed"]; ok {
					sawSuppressionNote = true
				}
			}
		}
	}
	// At most MaxManifestWarnings individual warnings plus one aggregate suppression note.
	if warnIssues > MaxManifestWarnings+1 {
		t.Errorf("manifest warning issues = %d, want <= %d", warnIssues, MaxManifestWarnings+1)
	}
	if !sawSuppressionNote {
		t.Errorf("expected an aggregate suppression issue when warnings exceed the cap")
	}
}

func TestInspect_TitleSanitizedLikePreSave(t *testing.T) {
	// U+202E (a BIDI control) is stripped by mmmodel.SanitizeUnicode, matching Page.PreSave. The
	// staged title must be sanitized, and the incoming source hash computed on the sanitized value.
	// Build the RIGHT-TO-LEFT OVERRIDE (U+202E) from its code point so no literal BIDI control sits
	// in this source file (which would trip bidichk/gosec).
	rlo := string(rune(0x202e))
	rawTitle := "Hi" + rlo + "There"
	page := `{"type":"page","page":{"space_import_source_id":"DOCS","user":"j","title":` +
		mustQuote(rawTitle) + `,"content":` + mustQuote(docString("x")) + `,"props":{"import_source_id":"100"}}}`
	jsonl := joinLines(versionLine(), spaceLine(), page, resolveLine())
	res, err := newBundle(jsonl, baseManifest(1, 0, 0)).inspect(t, InspectOptions{})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	want := mmmodel.SanitizeUnicode(rawTitle)
	if res.Pages[0].Title != want {
		t.Fatalf("staged title = %q, want sanitized %q", res.Pages[0].Title, want)
	}
	if strings.Contains(res.Pages[0].Title, rlo) {
		t.Errorf("staged title still contains the stripped control rune")
	}
	// The hash must be built on the sanitized title, so it equals a recompute using that title.
	wantHash, herr := HashSourceContent(SourceContentHashInput{
		Title:          want,
		CanonicalBody:  res.Pages[0].CanonicalBody,
		AuthorProposal: "j",
		SourceProps:    map[string]any{},
	})
	if herr != nil {
		t.Fatalf("hash: %v", herr)
	}
	if res.Pages[0].IncomingSourceContentHash != wantHash {
		t.Errorf("incoming hash not based on sanitized title")
	}
}

func TestInspect_RejectsNULInPersistedFields(t *testing.T) {
	// A NUL (U+0000) cannot be stored in a TEXT column and JSONB rejects its escape, and invalid UTF-8
	// would be silently mutated. Both are refused outright rather than sanitized: quietly altering a
	// page body is worse than telling the user the bundle is unusable. The producer emits NUL as a
	// JSON \u escape, so build the line via json.Marshal to mirror a real bundle.
	nul := string(rune(0))
	docJSON := `{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":` +
		mustQuote("hel"+nul+"lo") + `}]}]}`

	cases := map[string]map[string]any{
		"title": {
			"space_import_source_id": "DOCS", "user": "j", "title": "Ti" + nul + "tle",
			"content": docString("x"), "props": map[string]any{"import_source_id": "100"},
		},
		"content": {
			"space_import_source_id": "DOCS", "user": "j", "title": "T",
			"content": docJSON, "props": map[string]any{"import_source_id": "100"},
		},
		"user proposal": {
			"space_import_source_id": "DOCS", "user": "j" + nul + "doe", "title": "T",
			"content": docString("x"), "props": map[string]any{"import_source_id": "100"},
		},
		"source prop": {
			"space_import_source_id": "DOCS", "user": "j", "title": "T",
			"content": docString("x"),
			"props": map[string]any{
				"import_source_id": "100",
				"import_labels":    []any{"la" + nul + "bel"},
			},
		},
	}
	for name, page := range cases {
		t.Run(name, func(t *testing.T) {
			pageBytes, err := json.Marshal(map[string]any{"type": "page", "page": page})
			if err != nil {
				t.Fatalf("marshal page: %v", err)
			}
			jsonl := joinLines(versionLine(), spaceLine(), string(pageBytes), resolveLine())
			_, err = newBundle(jsonl, baseManifest(1, 0, 0)).inspect(t, InspectOptions{})
			code := inspectErrCode(err)
			if code != InspectErrUnstorableText && code != InspectErrTipTap {
				t.Fatalf("code = %q, want an unstorable-text rejection", code)
			}
		})
	}
}

func TestInspect_InvalidTipTap(t *testing.T) {
	jsonl := joinLines(versionLine(), spaceLine(),
		pageLine(t, "100", "", "H", `{"type":"notdoc"}`), resolveLine())
	_, err := newBundle(jsonl, baseManifest(1, 0, 0)).inspect(t, InspectOptions{})
	if got := inspectErrCode(err); got != InspectErrTipTap {
		t.Fatalf("code = %q, want %q", got, InspectErrTipTap)
	}
}

func TestInspect_InvalidCreateAtTimestamp(t *testing.T) {
	page := `{"type":"page","page":{"space_import_source_id":"DOCS","user":"j","title":"H","content":` +
		mustQuote(docString("x")) + `,"create_at":-5,"props":{"import_source_id":"100"}}}`
	jsonl := joinLines(versionLine(), spaceLine(), page, resolveLine())
	res, err := newBundle(jsonl, baseManifest(1, 0, 0)).inspect(t, InspectOptions{})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if !hasIssue(res, IssueSourceCreateAtInvalid) {
		t.Errorf("expected source_create_at_invalid issue")
	}
}

// --- archive-level tests ---

func TestInspectArchive_MissingJSONL(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create(entryManifest)
	_, _ = w.Write([]byte(`{"version":"2"}`))
	_ = zw.Close()
	raw := buf.Bytes()
	_, err := OpenArchive(bytes.NewReader(raw), int64(len(raw)))
	if got := inspectErrCode(err); got != ArchiveErrMissingJSONL {
		t.Fatalf("code = %q, want %q", got, ArchiveErrMissingJSONL)
	}
}

func TestInspectArchive_MissingManifest(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create(entryJSONL)
	_, _ = w.Write([]byte("x"))
	_ = zw.Close()
	raw := buf.Bytes()
	_, err := OpenArchive(bytes.NewReader(raw), int64(len(raw)))
	if got := inspectErrCode(err); got != ArchiveErrMissingManifest {
		t.Fatalf("code = %q, want %q", got, ArchiveErrMissingManifest)
	}
}

func TestInspectArchive_UnexpectedEntry(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, n := range []string{entryManifest, entryJSONL, "notes.txt"} {
		w, _ := zw.Create(n)
		_, _ = w.Write([]byte("x"))
	}
	_ = zw.Close()
	raw := buf.Bytes()
	_, err := OpenArchive(bytes.NewReader(raw), int64(len(raw)))
	if got := inspectErrCode(err); got != ArchiveErrUnexpectedEntry {
		t.Fatalf("code = %q, want %q", got, ArchiveErrUnexpectedEntry)
	}
}

func TestInspectArchive_DataDirAllowedNotOpened(t *testing.T) {
	b := validBundle(t)
	b.withFile("data/100/diagram.png", "not-real-bytes")
	res, err := b.inspect(t, InspectOptions{})
	if err != nil {
		t.Fatalf("data/ should be allowed: %v", err)
	}
	if res == nil {
		t.Fatal("nil result")
	}
}

func TestInspectArchive_Traversal(t *testing.T) {
	cases := map[string]string{
		"absolute":  "/etc/passwd",
		"dotdot":    "../evil",
		"backslash": "a\\b",
		"drive":     "C:evil",
	}
	for name, entry := range cases {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			zw := zip.NewWriter(&buf)
			for _, n := range []string{entryManifest, entryJSONL} {
				w, _ := zw.Create(n)
				_, _ = w.Write([]byte("x"))
			}
			// zip.Writer.Create sanitizes some names; CreateHeader writes the raw name verbatim.
			hw, err := zw.CreateHeader(&zip.FileHeader{Name: entry, Method: zip.Store})
			if err != nil {
				t.Fatalf("zip writer refused name %q: %v", entry, err)
			}
			_, _ = hw.Write([]byte("x"))
			_ = zw.Close()
			raw := buf.Bytes()
			_, err = OpenArchive(bytes.NewReader(raw), int64(len(raw)))
			if err == nil {
				t.Fatalf("expected rejection of entry %q", entry)
			}
			code := inspectErrCode(err)
			if code != ArchiveErrUnsafeEntry && code != ArchiveErrBadEntryName {
				t.Fatalf("code = %q, want unsafe/bad-name for entry %q", code, entry)
			}
		})
	}
}

func TestInspectArchive_DuplicateNormalizedEntry(t *testing.T) {
	// "data//x" and "data/x" normalize to the same path and must be rejected as a duplicate.
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, n := range []string{entryManifest, entryJSONL, "data/x", "data//x"} {
		w, _ := zw.Create(n)
		_, _ = w.Write([]byte("x"))
	}
	_ = zw.Close()
	raw := buf.Bytes()
	_, err := OpenArchive(bytes.NewReader(raw), int64(len(raw)))
	if got := inspectErrCode(err); got != ArchiveErrDuplicateEntry {
		t.Fatalf("code = %q, want %q", got, ArchiveErrDuplicateEntry)
	}
}

func TestInspectArchive_DataEntryUnsupportedMethod(t *testing.T) {
	// A data/ entry using an unsupported compression method is rejected even though its bytes are
	// never read.
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, n := range []string{entryManifest, entryJSONL} {
		w, _ := zw.Create(n)
		_, _ = w.Write([]byte("x"))
	}
	// Method 99 is neither Store nor Deflate. Register a passthrough compressor so the writer emits
	// a central-directory entry advertising method 99; the reader rejects it before opening bytes.
	zw.RegisterCompressor(99, func(w io.Writer) (io.WriteCloser, error) {
		return nopWriteCloser{w}, nil
	})
	hw, err := zw.CreateHeader(&zip.FileHeader{Name: "data/blob.bin", Method: 99})
	if err != nil {
		t.Fatalf("create header: %v", err)
	}
	_, _ = hw.Write([]byte("x"))
	_ = zw.Close()
	raw := buf.Bytes()
	_, err = OpenArchive(bytes.NewReader(raw), int64(len(raw)))
	if got := inspectErrCode(err); got != ArchiveErrUnsupportedMethod {
		t.Fatalf("code = %q, want %q", got, ArchiveErrUnsupportedMethod)
	}
}

func TestInspectArchive_SymlinkDirEntryRejected(t *testing.T) {
	// A symlink whose name ends in "/" must not slip past the file checks by looking like a
	// directory.
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, n := range []string{entryManifest, entryJSONL} {
		w, _ := zw.Create(n)
		_, _ = w.Write([]byte("x"))
	}
	hdr := &zip.FileHeader{Name: "data/", Method: zip.Store}
	hdr.SetMode(fs.ModeSymlink | 0o777)
	hw, err := zw.CreateHeader(hdr)
	if err != nil {
		t.Fatalf("create header: %v", err)
	}
	_, _ = hw.Write([]byte("/etc"))
	_ = zw.Close()
	raw := buf.Bytes()
	_, err = OpenArchive(bytes.NewReader(raw), int64(len(raw)))
	if got := inspectErrCode(err); got != ArchiveErrUnsafeEntry {
		t.Fatalf("code = %q, want %q", got, ArchiveErrUnsafeEntry)
	}
}

func TestInspectArchive_DuplicateEntry(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, n := range []string{entryManifest, entryJSONL, entryJSONL} {
		w, _ := zw.Create(n)
		_, _ = w.Write([]byte("x"))
	}
	_ = zw.Close()
	raw := buf.Bytes()
	_, err := OpenArchive(bytes.NewReader(raw), int64(len(raw)))
	if got := inspectErrCode(err); got != ArchiveErrDuplicateEntry {
		t.Fatalf("code = %q, want %q", got, ArchiveErrDuplicateEntry)
	}
}

// helpers

func hasIssue(res *collected, code string) bool {
	for _, i := range res.Issues {
		if i.Code == code {
			return true
		}
	}
	return false
}

// mustQuote JSON-encodes s as a JSON string literal (including surrounding quotes).
func mustQuote(s string) string {
	b, _ := jsonMarshalString(s)
	return b
}

// TestInspect_RequiresSourceNamespaceMirrors covers the producer contract's redundant space-key mirrors.
// Tolerating an absent mirror left the bundle's own statement of which source namespace it belongs to
// unverified on exactly the lines that carry it, so a bundle could be staged against a namespace no line
// ever agreed to. A present-but-different value was already rejected; absence now is too.
func TestInspect_RequiresSourceNamespaceMirrors(t *testing.T) {
	cases := map[string]string{
		"version line has no source block": strings.Join([]string{
			`{"type":"version","version":2}`,
			spaceLine(),
			pageLine(t, "100", "", "Root", docString("hi")),
			resolveLine(),
		}, "\n"),
		"version source has an empty space key": strings.Join([]string{
			`{"type":"version","version":2,"source":{"space_key":""}}`,
			spaceLine(),
			pageLine(t, "100", "", "Root", docString("hi")),
			resolveLine(),
		}, "\n"),
		"space line has no import_source_id": strings.Join([]string{
			versionLine(),
			`{"type":"space","space":{"team":"myteam","title":"Docs","description":"Migrated","props":{}}}`,
			pageLine(t, "100", "", "Root", docString("hi")),
			resolveLine(),
		}, "\n"),
		"resolve line has no space_import_source_id": strings.Join([]string{
			versionLine(),
			spaceLine(),
			pageLine(t, "100", "", "Root", docString("hi")),
			`{"type":"resolve_space_placeholders","resolve_space_placeholders":{}}`,
		}, "\n"),
	}
	for name, jsonl := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := newBundle(jsonl, baseManifest(1, 0, 0)).inspect(t, InspectOptions{})
			ie, ok := err.(*InspectError)
			if !ok || ie.Code != InspectErrSpaceKeyMissing {
				t.Fatalf("err = %v, want %s", err, InspectErrSpaceKeyMissing)
			}
		})
	}
}

// TestInspect_ValidatesSpaceDisplayText covers the manifest's source space name and the bundle's proposed
// Space title and description. All three are persisted but none is an indexed identifier, so they were
// left unchecked — a NUL then surfaced as a PostgreSQL write failure and an unbounded value as a summary
// column overflow, i.e. malformed input arriving as a 500 rather than a rejection.
func TestInspect_ValidatesSpaceDisplayText(t *testing.T) {
	goodJSONL := strings.Join([]string{
		versionLine(), spaceLine(), pageLine(t, "100", "", "Root", docString("hi")), resolveLine(),
	}, "\n")

	t.Run("manifest space name with a NUL", func(t *testing.T) {
		m := baseManifest(1, 0, 0)
		m.Source.SpaceName = "Docs\x00hidden"
		_, err := newBundle(goodJSONL, m).inspect(t, InspectOptions{})
		ie, ok := err.(*InspectError)
		if !ok || ie.Code != InspectErrUnstorableText {
			t.Fatalf("err = %v, want %s", err, InspectErrUnstorableText)
		}
	})

	t.Run("manifest space name too long", func(t *testing.T) {
		m := baseManifest(1, 0, 0)
		m.Source.SpaceName = strings.Repeat("é", SpaceNameMaxRunes+1)
		_, err := newBundle(goodJSONL, m).inspect(t, InspectOptions{})
		ie, ok := err.(*InspectError)
		if !ok || ie.Code != InspectErrSpaceNameTooLong {
			t.Fatalf("err = %v, want %s", err, InspectErrSpaceNameTooLong)
		}
	})

	t.Run("space title too long", func(t *testing.T) {
		jsonl := strings.Join([]string{
			versionLine(),
			`{"type":"space","space":{"team":"myteam","title":"` + strings.Repeat("t", SpaceTitleMaxRunes+1) +
				`","description":"Migrated","props":{"import_source_id":"DOCS"}}}`,
			pageLine(t, "100", "", "Root", docString("hi")),
			resolveLine(),
		}, "\n")
		_, err := newBundle(jsonl, baseManifest(1, 0, 0)).inspect(t, InspectOptions{})
		ie, ok := err.(*InspectError)
		if !ok || ie.Code != InspectErrSpaceTextTooLong {
			t.Fatalf("err = %v, want %s", err, InspectErrSpaceTextTooLong)
		}
	})

	t.Run("a name at the limit is accepted", func(t *testing.T) {
		m := baseManifest(1, 0, 0)
		m.Source.SpaceName = strings.Repeat("é", SpaceNameMaxRunes)
		if _, err := newBundle(goodJSONL, m).inspect(t, InspectOptions{}); err != nil {
			t.Fatalf("a name exactly at the limit must be accepted: %v", err)
		}
	})
}

// TestInspect_BoundsSourceProps covers the size of the allowlisted source props. Their shape was validated
// but not their serialized size, so a bundle that satisfies the contract could still exceed the JSONB and
// page-props limits — surfacing as a server error during staging rather than as a refused bundle.
func TestInspect_BoundsSourceProps(t *testing.T) {
	pageWithLabels := func(labels []any) string {
		props := map[string]any{
			"import_source_id":             "100",
			"import_source":                "confluence",
			"confluence_space_key":         "DOCS",
			"confluence_author_account_id": "aaid-100",
			"import_labels":                labels,
		}
		page := map[string]any{
			"type": "page",
			"page": map[string]any{
				"space_import_source_id": "DOCS",
				"user":                   "jdoe",
				"title":                  "Root",
				"content":                docString("hi"),
				"create_at":              1704106800000,
				"props":                  props,
			},
		}
		raw, err := json.Marshal(page)
		if err != nil {
			t.Fatalf("marshal page: %v", err)
		}
		return string(raw)
	}
	bundleWith := func(labels []any) *bundleBuilder {
		jsonl := strings.Join([]string{versionLine(), spaceLine(), pageWithLabels(labels), resolveLine()}, "\n")
		return newBundle(jsonl, baseManifest(1, 0, 0))
	}

	t.Run("props at the limit are accepted", func(t *testing.T) {
		// One label just under the bound, leaving room for the JSON scaffolding around it.
		labels := []any{strings.Repeat("l", MaxSourcePropsBytes-64)}
		if _, err := bundleWith(labels).inspect(t, InspectOptions{}); err != nil {
			t.Fatalf("props within the limit must be accepted: %v", err)
		}
	})

	t.Run("oversized props are refused, not deferred to a column limit", func(t *testing.T) {
		labels := make([]any, 0, 512)
		for range 512 {
			labels = append(labels, strings.Repeat("l", 128))
		}
		_, err := bundleWith(labels).inspect(t, InspectOptions{})
		ie, ok := err.(*InspectError)
		if !ok || ie.Code != InspectErrSourcePropsTooLarge {
			t.Fatalf("err = %v, want %s", err, InspectErrSourcePropsTooLarge)
		}
	})
}

// TestEffectiveAuthorProposal pins the single definition both hashing and resolution use. Two definitions
// let a real author change leave the source-content hash untouched, so the page reads as unchanged and the
// new attribution is never applied.
func TestEffectiveAuthorProposal(t *testing.T) {
	if got := EffectiveAuthorProposal("from-manifest", "from-page"); got != "from-manifest" {
		t.Errorf("manifest proposal must win, got %q", got)
	}
	if got := EffectiveAuthorProposal("", "from-page"); got != "from-page" {
		t.Errorf("page proposal must be the fallback, got %q", got)
	}
	if got := EffectiveAuthorProposal("", ""); got != "" {
		t.Errorf("no proposal must stay empty, got %q", got)
	}
}
