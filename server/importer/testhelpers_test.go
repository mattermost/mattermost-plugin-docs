// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package importer

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
)

// bundleBuilder assembles an in-memory ZIP bundle for tests. It computes the JSONL checksum and
// injects it into the manifest unless the manifest already carries one.
type bundleBuilder struct {
	jsonl      string
	manifest   Manifest
	extraFiles map[string]string // extra archive entries (name -> body), e.g. under data/
	// skipChecksum leaves the manifest checksum untouched (for checksum-mismatch tests).
	skipChecksum bool
}

func newBundle(jsonl string, manifest Manifest) *bundleBuilder {
	return &bundleBuilder{jsonl: jsonl, manifest: manifest, extraFiles: map[string]string{}}
}

func (b *bundleBuilder) withFile(name, body string) *bundleBuilder {
	b.extraFiles[name] = body
	return b
}

// jsonlSha returns the lowercase-hex SHA-256 of the builder's JSONL bytes.
func (b *bundleBuilder) jsonlSha() string {
	sum := sha256.Sum256([]byte(b.jsonl))
	return hex.EncodeToString(sum[:])
}

// bytes builds the ZIP archive bytes.
func (b *bundleBuilder) bytesZip(t *testing.T) []byte {
	t.Helper()
	m := b.manifest
	if !b.skipChecksum && m.Checksums.JSONLSha256 == "" {
		m.Checksums.JSONLSha256 = b.jsonlSha()
	}
	manifestBytes, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	writeEntry := func(name, body string) {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip create %q: %v", name, err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatalf("zip write %q: %v", name, err)
		}
	}
	writeEntry(entryManifest, string(manifestBytes))
	writeEntry(entryJSONL, b.jsonl)
	for name, body := range b.extraFiles {
		writeEntry(name, body)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

// bytesZipWithManifestSuffix builds the archive like bytesZip but appends suffix to the manifest
// entry's bytes after its JSON object, for exercising trailing-data rejection.
func (b *bundleBuilder) bytesZipWithManifestSuffix(t *testing.T, suffix string) []byte {
	t.Helper()
	m := b.manifest
	if !b.skipChecksum && m.Checksums.JSONLSha256 == "" {
		m.Checksums.JSONLSha256 = b.jsonlSha()
	}
	manifestBytes, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	writeEntry := func(name, body string) {
		w, cerr := zw.Create(name)
		if cerr != nil {
			t.Fatalf("zip create %q: %v", name, cerr)
		}
		if _, werr := w.Write([]byte(body)); werr != nil {
			t.Fatalf("zip write %q: %v", name, werr)
		}
	}
	writeEntry(entryManifest, string(manifestBytes)+suffix)
	writeEntry(entryJSONL, b.jsonl)
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

// collected is the streamed inspection output gathered into one value for assertions. Production
// callers persist each page as it arrives; tests accumulate them so they can inspect the whole set.
type collected struct {
	Summary *InspectionSummary
	Pages   []StagedPage
	Users   []StagedManifestUser
	Issues  []InspectionIssue
}

// Convenience accessors mirroring the summary fields tests assert on.
func (c *collected) Version() int                  { return c.Summary.Version }
func (c *collected) SpaceKey() string              { return c.Summary.SpaceKey }
func (c *collected) SpaceTitle() string            { return c.Summary.SpaceTitle }
func (c *collected) CommentCount() int             { return c.Summary.CommentCount }
func (c *collected) AttachmentCount() int          { return c.Summary.AttachmentCount }
func (c *collected) Restricted() RestrictedSummary { return c.Summary.Restricted }

// inspect opens the builder's archive and streams a full inspection into a collected result.
func (b *bundleBuilder) inspect(t *testing.T, opts InspectOptions) (*collected, error) {
	t.Helper()
	raw := b.bytesZip(t)
	archive, err := OpenArchive(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return nil, err
	}
	return collectInspect(archive, opts)
}

// collectInspect runs Inspect with a sink that accumulates everything it is handed.
func collectInspect(archive *Archive, opts InspectOptions) (*collected, error) {
	out := &collected{}
	sink := StreamSink{
		Page: func(p *StagedPage) error {
			out.Pages = append(out.Pages, *p)
			return nil
		},
		ManifestUser: func(u *StagedManifestUser) error {
			out.Users = append(out.Users, *u)
			return nil
		},
		Issue: func(i *InspectionIssue) error {
			out.Issues = append(out.Issues, *i)
			return nil
		},
	}
	summary, err := Inspect(archive, opts, sink)
	if err != nil {
		return nil, err
	}
	out.Summary = summary
	return out, nil
}

// baseManifest returns a minimal valid v2 manifest with the given counts.
func baseManifest(pages, comments, attachments int) Manifest {
	return Manifest{
		Version: "2",
		Source:  ManifestSource{Type: "confluence", SpaceKey: "DOCS", SpaceName: "Docs"},
		Target:  ManifestTarget{Team: "myteam"},
		Counts:  ManifestCounts{Pages: pages, Comments: comments, Attachments: attachments},
	}
}

// docString builds a TipTap doc JSON string with a single paragraph of the given text.
func docString(text string) string {
	doc := map[string]any{
		"type": "doc",
		"content": []any{
			map[string]any{
				"type": "paragraph",
				"content": []any{
					map[string]any{"type": "text", "text": text},
				},
			},
		},
	}
	b, _ := json.Marshal(doc)
	return string(b)
}

// versionLine, spaceLine, pageLine, commentLine, resolveLine build individual JSONL lines.
func versionLine() string {
	return `{"type":"version","version":2,"source":{"space_key":"DOCS"}}`
}

func spaceLine() string {
	return `{"type":"space","space":{"team":"myteam","title":"Docs","description":"Migrated","props":{"import_source_id":"DOCS"}}}`
}

// pageLine builds a page line. parentID "" means a root page. content is a raw TipTap JSON string.
func pageLine(t *testing.T, externalID, parentID, title, content string) string {
	t.Helper()
	page := map[string]any{
		"type": "page",
		"page": map[string]any{
			"team":                   "myteam",
			"space_import_source_id": "DOCS",
			"user":                   "jdoe",
			"title":                  title,
			"content":                content,
			"create_at":              int64(1704106800000),
			"update_at":              int64(1704193200000),
			"props": map[string]any{
				"import_source_id":             externalID,
				"import_source":                "confluence",
				"confluence_author_account_id": "aaid-" + externalID,
			},
		},
	}
	if parentID != "" {
		page["page"].(map[string]any)["parent_import_source_id"] = parentID
	}
	b, err := json.Marshal(page)
	if err != nil {
		t.Fatalf("marshal page line: %v", err)
	}
	return string(b)
}

func resolveLine() string {
	return `{"type":"resolve_space_placeholders","resolve_space_placeholders":{"team":"myteam","space_import_source_id":"DOCS"}}`
}

// jsonMarshalString JSON-encodes s as a quoted JSON string literal.
func jsonMarshalString(s string) (string, error) {
	b, err := json.Marshal(s)
	return string(b), err
}

// joinLines joins JSONL lines with newlines and a trailing newline (a normal file terminator).
func joinLines(lines ...string) string {
	var b strings.Builder
	for _, l := range lines {
		b.WriteString(l)
		b.WriteByte('\n')
	}
	return b.String()
}
