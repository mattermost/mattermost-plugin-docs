// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Package importfixture builds mmetl Confluence v2 bundle archives in memory. It is the single
// source of truth for the fixture shape, shared by the genimportbundle development command and the
// server's import tests, so the bundle a developer uploads by hand and the one the tests exercise
// cannot drift apart. It deliberately does not depend on "testing" so a command can use it too.
package importfixture

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Corruption modes: each deliberately breaks one contract rule so the importer's rejection paths can
// be exercised. An empty Corrupt value builds a fully valid bundle.
const (
	CorruptNone          = ""
	CorruptCountMismatch = "count-mismatch"
	CorruptBadChecksum   = "bad-checksum"
	CorruptMissingParent = "missing-parent"
	CorruptBadTipTap     = "bad-tiptap"
	CorruptDeepHierarchy = "deep-hierarchy"
)

// CorruptModes lists every supported corruption mode, for flag help and table-driven tests.
var CorruptModes = []string{
	CorruptCountMismatch, CorruptBadChecksum, CorruptMissingParent, CorruptBadTipTap, CorruptDeepHierarchy,
}

// RootExternalID is the external id of the first (root) page every fixture emits.
const RootExternalID = "100"

// AuthorUsername is the Mattermost username every fixture page proposes as its author, both through the
// manifest's user mapping for the root page and through each page's own user field. Tests stub a user
// lookup for it to exercise successful author resolution.
const AuthorUsername = "jdoe"

// Options controls the generated bundle.
type Options struct {
	// Pages is how many pages to emit; the first is the root and the rest are its children (or, under
	// CorruptDeepHierarchy, a single chain).
	Pages int
	// SpaceKey and SpaceName identify the source Confluence space. SpaceKey is required by the
	// importer, so it defaults to "DOCS" when empty.
	SpaceKey  string
	SpaceName string
	// OrganizationID is optional source metadata.
	OrganizationID string
	// Team is the advisory target team recorded in the bundle. It never routes an import; a value
	// differing from the request's team produces a bundle_team_mismatch warning.
	Team string
	// WithFindings also emits a comment, an attachment record (plus its data/ payload), a restricted
	// page, a manifest-only restricted entry, and a manifest warning, so inspection reports issues.
	WithFindings bool
	// Corrupt is one of the Corrupt* modes.
	Corrupt string
}

// Bundle is a generated archive plus the counts it declares, so a caller can assert against them.
type Bundle struct {
	Zip         []byte
	JSONLSha256 string
	Pages       int
	Comments    int
	Attachments int
	Restricted  int
}

// ArchiveSha256 returns the lowercase-hex digest of the archive bytes, matching what the upload
// endpoint computes while streaming.
func (b Bundle) ArchiveSha256() string {
	return sha256Hex(b.Zip)
}

// Build assembles a bundle archive. The result satisfies every rule the importer enforces — ordered
// lines, parents before children, manifest counts equal to the emitted lines, and a correct JSONL
// checksum — unless Options.Corrupt asks for a specific violation.
func Build(o Options) (Bundle, error) {
	if o.Pages < 1 {
		return Bundle{}, fmt.Errorf("importfixture: Pages must be at least 1, got %d", o.Pages)
	}
	if o.SpaceKey == "" {
		o.SpaceKey = "DOCS"
	}
	if o.SpaceName == "" {
		o.SpaceName = "Docs"
	}
	if o.Team == "" {
		o.Team = "myteam"
	}

	lines, counts, restricted, err := buildLines(o)
	if err != nil {
		return Bundle{}, err
	}

	jsonl := strings.Join(lines, "\n") + "\n"
	jsonlSHA := sha256Hex([]byte(jsonl))

	declaredPages := counts.pages
	if o.Corrupt == CorruptCountMismatch {
		declaredPages = counts.pages + 4
	}
	manifestChecksum := jsonlSHA
	if o.Corrupt == CorruptBadChecksum {
		manifestChecksum = strings.Repeat("0", 64)
	}

	manifest := map[string]any{
		"version":           "2",
		"generator":         "importfixture",
		"generator_version": "dev",
		"created_at":        "2026-01-01T00:00:00Z",
		"source": map[string]any{
			"type":            "confluence",
			"organization_id": o.OrganizationID,
			"space_key":       o.SpaceKey,
			"space_name":      o.SpaceName,
			"export_file":     "sample-export.csv",
		},
		"target": map[string]any{"team": o.Team},
		"counts": map[string]any{
			"spaces":      1,
			"pages":       declaredPages,
			"comments":    counts.comments,
			"attachments": counts.attachments,
		},
		"checksums": map[string]any{"jsonl_sha256": manifestChecksum},
		"users": []map[string]any{
			{"account_id": "aaid-" + RootExternalID, "confluence_username": AuthorUsername, "mattermost_username": AuthorUsername},
		},
	}
	if len(restricted) > 0 {
		manifest["restricted_pages"] = restricted
	}
	if o.WithFindings {
		manifest["warnings"] = []string{"sample producer warning: one macro was converted to a code block"}
	}

	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return Bundle{}, fmt.Errorf("importfixture: marshal manifest: %w", err)
	}

	zipBytes, err := writeZip(manifestBytes, jsonl, o.WithFindings)
	if err != nil {
		return Bundle{}, err
	}
	return Bundle{
		Zip:         zipBytes,
		JSONLSha256: jsonlSHA,
		Pages:       counts.pages,
		Comments:    counts.comments,
		Attachments: counts.attachments,
		Restricted:  len(restricted),
	}, nil
}

type lineCounts struct {
	pages       int
	comments    int
	attachments int
}

// buildLines emits the ordered JSONL lines: version, space, pages (parents first), comments, and the
// trailing placeholder-resolution directive.
func buildLines(o Options) ([]string, lineCounts, []map[string]any, error) {
	var lines []string
	var counts lineCounts
	restricted := []map[string]any{}

	source := map[string]any{"space_key": o.SpaceKey}
	if o.OrganizationID != "" {
		source["organization_id"] = o.OrganizationID
	}
	versionLine, err := marshalLine(map[string]any{"type": "version", "version": 2, "source": source})
	if err != nil {
		return nil, counts, nil, err
	}
	spaceLine, err := marshalLine(map[string]any{"type": "space", "space": map[string]any{
		"team":        o.Team,
		"title":       o.SpaceName,
		"description": "Migrated from Confluence space: " + o.SpaceKey,
		"props":       map[string]any{"import_source_id": o.SpaceKey},
	}})
	if err != nil {
		return nil, counts, nil, err
	}
	lines = append(lines, versionLine, spaceLine)

	chain := o.Corrupt == CorruptDeepHierarchy
	prevID := ""
	for i := 0; i < o.Pages; i++ {
		id := strconv.Itoa(100 + i)
		parent := ""
		switch {
		case i == 0:
			parent = ""
		case chain:
			parent = prevID
		default:
			parent = RootExternalID
		}
		if o.Corrupt == CorruptMissingParent && i == 1 {
			parent = "999999" // never emitted
		}

		title := fmt.Sprintf("Imported page %d", i+1)
		content, docErr := sampleDoc(title, i)
		if docErr != nil {
			return nil, counts, nil, docErr
		}
		if o.Corrupt == CorruptBadTipTap && i == 0 {
			content = `{"type":"paragraph","content":[]}` // root is not a doc node
		}

		page := map[string]any{
			"team":                   o.Team,
			"space_import_source_id": o.SpaceKey,
			"user":                   AuthorUsername,
			"title":                  title,
			"content":                content,
			"create_at":              1704106800000 + int64(i)*1000,
			"update_at":              1704193200000 + int64(i)*1000,
			"props": map[string]any{
				"import_source_id":             id,
				"import_source":                "confluence",
				"confluence_space_key":         o.SpaceKey,
				"confluence_author_account_id": "aaid-" + id,
				"import_labels":                []string{"imported", "sample"},
			},
		}
		if parent != "" {
			page["parent_import_source_id"] = parent
		}
		if o.WithFindings && i == 0 {
			page["attachments"] = []map[string]any{{
				"path":  id + "/diagram.png",
				"props": map[string]any{"import_source_id": "300"},
			}}
			counts.attachments++
			restricted = append(restricted, map[string]any{"id": id, "title": title})
		}

		pageLine, lineErr := marshalLine(map[string]any{"type": "page", "page": page})
		if lineErr != nil {
			return nil, counts, nil, lineErr
		}
		lines = append(lines, pageLine)
		counts.pages++
		prevID = id
	}

	if o.WithFindings {
		// A restricted entry for a page that was never emitted: reported, never claimed as imported.
		restricted = append(restricted, map[string]any{"id": "999000", "title": "Draft page not in export"})

		commentLine, cErr := marshalLine(map[string]any{"type": "page_comment", "page_comment": map[string]any{
			"page_import_source_id": RootExternalID,
			"user":                  "jdoe",
			"content":               "This comment is counted but not imported.",
			"create_at":             1704625200000,
			"update_at":             1704625200000,
			"props":                 map[string]any{"import_source_id": "201", "import_source": "confluence"},
		}})
		if cErr != nil {
			return nil, counts, nil, cErr
		}
		lines = append(lines, commentLine)
		counts.comments++
	}

	resolveLine, err := marshalLine(map[string]any{
		"type": "resolve_space_placeholders",
		"resolve_space_placeholders": map[string]any{
			"team":                   o.Team,
			"space_import_source_id": o.SpaceKey,
		},
	})
	if err != nil {
		return nil, counts, nil, err
	}
	lines = append(lines, resolveLine)

	return lines, counts, restricted, nil
}

// writeZip assembles the archive: the two required root entries plus, when attachments were emitted,
// one file under data/ (which the importer must accept without ever opening).
func writeZip(manifest []byte, jsonl string, withData bool) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	add := func(name string, body []byte) error {
		w, err := zw.Create(name)
		if err != nil {
			return fmt.Errorf("importfixture: zip create %s: %w", name, err)
		}
		if _, err := w.Write(body); err != nil {
			return fmt.Errorf("importfixture: zip write %s: %w", name, err)
		}
		return nil
	}
	if err := add("import-manifest.json", manifest); err != nil {
		return nil, err
	}
	if err := add("import.jsonl", []byte(jsonl)); err != nil {
		return nil, err
	}
	if withData {
		if err := add("data/"+RootExternalID+"/diagram.png", []byte("not a real png; never opened by the importer")); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("importfixture: zip close: %w", err)
	}
	return buf.Bytes(), nil
}

// sampleDoc builds a TipTap document exercising the SearchText and link-discovery paths: a heading,
// paragraphs, a hard break, a bullet list, a code block, and a table. Pages after the first also
// carry a link with a braced Confluence page placeholder plus a placeholder left in ordinary text,
// so both the discovered-link and the not-rewritten-in-text reports have something to show.
func sampleDoc(title string, index int) (string, error) {
	content := []any{
		node("heading", map[string]any{"level": 2}, text(title)),
		node("paragraph", nil,
			text("This page was generated for manual import verification."),
			map[string]any{"type": "hardBreak"},
			text("The second line follows a hard break."),
		),
		node("bulletList", nil,
			node("listItem", nil, node("paragraph", nil, text("first bullet"))),
			node("listItem", nil, node("paragraph", nil, text("second bullet"))),
		),
		node("codeBlock", nil, text("go test ./server/importer/...")),
		node("table", nil,
			node("tableRow", nil,
				node("tableHeader", nil, node("paragraph", nil, text("Key"))),
				node("tableHeader", nil, node("paragraph", nil, text("Value"))),
			),
			node("tableRow", nil,
				node("tableCell", nil, node("paragraph", nil, text("index"))),
				node("tableCell", nil, node("paragraph", nil, text(strconv.Itoa(index)))),
			),
		),
	}
	if index > 0 {
		linked := text("link to the root page")
		linked["marks"] = []any{map[string]any{
			"type":  "link",
			"attrs": map[string]any{"href": "{{CONF_PAGE_ID:" + RootExternalID + "}}"},
		}}
		content = append(content,
			node("paragraph", nil, linked),
			node("paragraph", nil, text("Unresolved reference {{CONF_PAGE_TITLE:Some Other Page}} stays as text.")),
		)
	}
	raw, err := json.Marshal(map[string]any{"type": "doc", "content": content})
	if err != nil {
		return "", fmt.Errorf("importfixture: marshal sample doc: %w", err)
	}
	return string(raw), nil
}

func node(kind string, attrs map[string]any, children ...any) map[string]any {
	n := map[string]any{"type": kind}
	if attrs != nil {
		n["attrs"] = attrs
	}
	if len(children) > 0 {
		n["content"] = children
	}
	return n
}

func text(s string) map[string]any {
	return map[string]any{"type": "text", "text": s}
}

func marshalLine(v any) (string, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("importfixture: marshal jsonl line: %w", err)
	}
	return string(raw), nil
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
