// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package importer

import (
	"archive/zip"
	"bytes"
	"errors"
	"strings"
	"testing"
)

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
		`{"type":"page_comment","page_comment":{"page_import_source_id":"100","user":"jdoe","content":"hi","create_at":1704625200000}}`,
		resolveLine(),
	)
	return newBundle(jsonl, baseManifest(2, 1, 0))
}

func TestInspect_ValidBundle(t *testing.T) {
	res, err := validBundle(t).inspect(t, InspectOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Version != 2 {
		t.Errorf("version = %d, want 2", res.Version)
	}
	if len(res.Pages) != 2 {
		t.Fatalf("pages = %d, want 2", len(res.Pages))
	}
	if res.CommentCount != 1 {
		t.Errorf("comments = %d, want 1", res.CommentCount)
	}
	if res.SpaceKey != "DOCS" {
		t.Errorf("space key = %q, want DOCS", res.SpaceKey)
	}
	if res.SpaceTitle != "Docs" {
		t.Errorf("space title = %q, want Docs", res.SpaceTitle)
	}
	// Root then child; child's parent + sibling ordinals.
	if res.Pages[0].ExternalID != "100" || res.Pages[1].ExternalID != "101" {
		t.Errorf("page order = %q, %q", res.Pages[0].ExternalID, res.Pages[1].ExternalID)
	}
	if res.Pages[1].ParentExternalID != "100" {
		t.Errorf("child parent = %q, want 100", res.Pages[1].ParentExternalID)
	}
	if res.Pages[0].IncomingSourceHash == "" || !IsValidSHA256Hex(res.Pages[0].IncomingSourceHash) {
		t.Errorf("incoming hash invalid: %q", res.Pages[0].IncomingSourceHash)
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
	if res.AttachmentCount != 2 {
		t.Errorf("attachments = %d, want 2", res.AttachmentCount)
	}
	if !hasIssue(res, IssueAttachmentNotImported) {
		t.Errorf("expected attachment_placeholder_not_imported issue")
	}
}

func TestInspect_ManifestCountMismatch(t *testing.T) {
	res, err := newBundle(
		joinLines(versionLine(), spaceLine(), pageLine(t, "100", "", "H", docString("x")), resolveLine()),
		baseManifest(5, 0, 0), // declares 5 pages, but only 1 parsed
	).inspect(t, InspectOptions{})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if !hasIssue(res, IssueManifestCountMismatch) {
		t.Errorf("expected manifest_count_mismatch issue")
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
	if res.Restricted.ManifestTotal != 2 || res.Restricted.EmittedPages != 1 || res.Restricted.ManifestOnly != 1 {
		t.Errorf("restricted summary = %+v", res.Restricted)
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
	_, err := InspectArchive(bytes.NewReader(raw), int64(len(raw)))
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
	_, err := InspectArchive(bytes.NewReader(raw), int64(len(raw)))
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
	_, err := InspectArchive(bytes.NewReader(raw), int64(len(raw)))
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
			_, err = InspectArchive(bytes.NewReader(raw), int64(len(raw)))
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

func TestInspectArchive_DuplicateEntry(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, n := range []string{entryManifest, entryJSONL, entryJSONL} {
		w, _ := zw.Create(n)
		_, _ = w.Write([]byte("x"))
	}
	_ = zw.Close()
	raw := buf.Bytes()
	_, err := InspectArchive(bytes.NewReader(raw), int64(len(raw)))
	if got := inspectErrCode(err); got != ArchiveErrDuplicateEntry {
		t.Fatalf("code = %q, want %q", got, ArchiveErrDuplicateEntry)
	}
}

// helpers

func hasIssue(res *InspectionResult, code string) bool {
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
