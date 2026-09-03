// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package importer

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mattermost/mattermost-plugin-docs/server/model"
)

func TestCanonicalize_RejectsNonDoc(t *testing.T) {
	for _, body := range []string{`{"type":"paragraph"}`, `[]`, `"text"`, `{`, `{"type":"doc"} trailing`} {
		if _, _, _, err := CanonicalizeAndExtractSearchText(body); err == nil {
			t.Errorf("expected rejection for %q", body)
		}
	}
}

func TestCanonicalize_RejectsTrailingDelimiter(t *testing.T) {
	// json.Decoder.More() returns false before a closing "]"/"}", so these must be caught by the
	// decode-second-value/io.EOF check rather than More().
	for _, body := range []string{`{"type":"doc","content":[]}]`, `{"type":"doc","content":[]}}`, `{"type":"doc"}{}`} {
		if _, _, _, err := CanonicalizeAndExtractSearchText(body); err == nil {
			t.Errorf("expected trailing-data rejection for %q", body)
		}
	}
	// Trailing whitespace remains acceptable.
	if _, _, _, err := CanonicalizeAndExtractSearchText(`{"type":"doc","content":[]}` + "  \n"); err != nil {
		t.Errorf("trailing whitespace should be accepted: %v", err)
	}
}

// TestCanonicalize_RejectsUnknownNodeTypes pins the security-relevant reversal: an unknown node type
// is rejected rather than preserved, because preserving it is exactly how unsanitized content would
// reach storage. Imported content goes through the same allowlist as browser-supplied content.
func TestCanonicalize_RejectsUnknownNodeTypes(t *testing.T) {
	body := `{"type":"doc","content":[{"type":"customWidget","attrs":{"foo":"bar"},"content":[{"type":"text","text":"hi"}]}]}`
	_, _, _, err := CanonicalizeAndExtractSearchText(body)
	if te, ok := err.(*TipTapError); !ok || te.Code != TipTapErrSanitizerRejected {
		t.Fatalf("err = %v, want %s", err, TipTapErrSanitizerRejected)
	}
}

// TestCanonicalize_RunsSharedSanitizer proves the shared sanitizer is actually applied to imported
// content: a script-bearing URL is neutralized and an event-handler attribute is stripped, rather
// than being canonicalized through into staging.
func TestCanonicalize_RunsSharedSanitizer(t *testing.T) {
	doc := map[string]any{
		"type": "doc",
		"content": []any{
			map[string]any{
				"type": "paragraph",
				"content": []any{
					map[string]any{
						"type": "text",
						"text": "click me",
						"marks": []any{map[string]any{
							"type":  "link",
							"attrs": map[string]any{"href": "javascript:alert(1)", "onclick": "steal()"},
						}},
					},
				},
			},
			map[string]any{"type": "image", "attrs": map[string]any{
				"src": "vbscript:evil", "style": "position:fixed",
			}},
		},
	}
	canon, _, _, err := CanonicalizeAndExtractSearchText(marshal(doc))
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	for _, forbidden := range []string{"javascript:", "vbscript:", "onclick", "position:fixed"} {
		if strings.Contains(canon, forbidden) {
			t.Errorf("canonical body still contains %q: %s", forbidden, canon)
		}
	}
}

// TestCanonicalize_KeepsPlaceholdersThroughSanitizer confirms the Confluence placeholders survive
// URL sanitization: they carry no scheme, so the allowlist treats them as relative references.
func TestCanonicalize_KeepsPlaceholdersThroughSanitizer(t *testing.T) {
	doc := map[string]any{
		"type": "doc",
		"content": []any{
			map[string]any{
				"type": "paragraph",
				"content": []any{
					map[string]any{
						"type": "text",
						"text": "see page",
						"marks": []any{map[string]any{
							"type":  "link",
							"attrs": map[string]any{"href": "{{CONF_PAGE_ID:101}}"},
						}},
					},
				},
			},
		},
	}
	canon, _, links, err := CanonicalizeAndExtractSearchText(marshal(doc))
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if !strings.Contains(canon, "{{CONF_PAGE_ID:101}}") {
		t.Errorf("placeholder did not survive sanitization: %s", canon)
	}
	if len(links) != 1 || links[0].Kind != LinkKindPageID || links[0].Target != "101" {
		t.Errorf("placeholder not discovered after sanitization: %+v", links)
	}
}

func TestCanonicalize_RejectsNonArrayContent(t *testing.T) {
	if _, _, _, err := CanonicalizeAndExtractSearchText(`{"type":"doc","content":{}}`); err == nil {
		t.Errorf("expected bad-content rejection")
	}
	if _, _, _, err := CanonicalizeAndExtractSearchText(`{"type":"doc","content":[{"type":"text","text":5}]}`); err == nil {
		t.Errorf("expected bad-text rejection")
	}
}

func TestCanonicalize_RejectsMissingNodeType(t *testing.T) {
	// A child node with no type is structurally invalid; the shared sanitizer rejects it first.
	body := `{"type":"doc","content":[{"attrs":{"x":1},"content":[{"type":"text","text":"hi"}]}]}`
	_, _, _, err := CanonicalizeAndExtractSearchText(body)
	if te, ok := err.(*TipTapError); !ok || te.Code != TipTapErrSanitizerRejected {
		t.Fatalf("err = %v, want %s", err, TipTapErrSanitizerRejected)
	}
}

func TestCanonicalize_RejectsTextNodeWithoutText(t *testing.T) {
	body := `{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text"}]}]}`
	_, _, _, err := CanonicalizeAndExtractSearchText(body)
	if te, ok := err.(*TipTapError); !ok || te.Code != TipTapErrBadText {
		t.Fatalf("err = %v, want %s", err, TipTapErrBadText)
	}
}

func TestSearchText_ParagraphsHeadingsHardBreak(t *testing.T) {
	doc := map[string]any{
		"type": "doc",
		"content": []any{
			block("heading", text("Title")),
			block("paragraph", text("First line"), map[string]any{"type": "hardBreak"}, text("second line")),
			block("paragraph", text("Another paragraph")),
		},
	}
	_, search, _, err := CanonicalizeAndExtractSearchText(marshal(doc))
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	want := "Title\nFirst line\nsecond line\nAnother paragraph"
	if search != want {
		t.Errorf("search = %q, want %q", search, want)
	}
}

func TestSearchText_ListsAndTables(t *testing.T) {
	doc := map[string]any{
		"type": "doc",
		"content": []any{
			block("bulletList",
				block("listItem", block("paragraph", text("one"))),
				block("listItem", block("paragraph", text("two"))),
			),
			block("table",
				block("tableRow",
					block("tableHeader", block("paragraph", text("H1"))),
					block("tableHeader", block("paragraph", text("H2"))),
				),
				block("tableRow",
					block("tableCell", block("paragraph", text("a"))),
					block("tableCell", block("paragraph", text("b"))),
				),
			),
		},
	}
	_, search, _, err := CanonicalizeAndExtractSearchText(marshal(doc))
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	for _, token := range []string{"one", "two", "H1", "H2", "a", "b"} {
		if !strings.Contains(search, token) {
			t.Errorf("search %q missing %q", search, token)
		}
	}
	// No triple newlines survive normalization.
	if strings.Contains(search, "\n\n\n") {
		t.Errorf("search has 3+ consecutive newlines: %q", search)
	}
}

func TestSearchText_CodeBlock(t *testing.T) {
	doc := map[string]any{
		"type":    "doc",
		"content": []any{block("codeBlock", text("go build ./..."))},
	}
	_, search, _, err := CanonicalizeAndExtractSearchText(marshal(doc))
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if search != "go build ./..." {
		t.Errorf("search = %q", search)
	}
}

func TestLinkDiscovery_OnlyApprovedAttrs(t *testing.T) {
	doc := map[string]any{
		"type": "doc",
		"content": []any{
			map[string]any{
				"type": "paragraph",
				"content": []any{
					// link mark with a page-id placeholder href (producer emits braced form)
					map[string]any{
						"type":  "text",
						"text":  "see other page",
						"marks": []any{map[string]any{"type": "link", "attrs": map[string]any{"href": "{{CONF_PAGE_ID:101}}"}}},
					},
					// ordinary text mentioning a placeholder token (must NOT be an approved link)
					map[string]any{"type": "text", "text": "the token {{CONF_PAGE_ID:999}} appears here"},
				},
			},
			// image node with attachment placeholder src
			map[string]any{"type": "image", "attrs": map[string]any{"src": "{{CONF_ATTACHMENT:300}}"}},
		},
	}
	_, _, links, err := CanonicalizeAndExtractSearchText(marshal(doc))
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	var approved, inText int
	var sawPageID, sawAttachment bool
	for _, l := range links {
		if l.InText {
			inText++
			continue
		}
		approved++
		switch l.Kind {
		case LinkKindPageID:
			sawPageID = true
			if l.Target != "101" {
				t.Errorf("page-id target = %q, want 101", l.Target)
			}
		case LinkKindAttachment:
			sawAttachment = true
			if !l.InImageSrc {
				t.Errorf("attachment link should be flagged InImageSrc")
			}
		}
	}
	if !sawPageID || !sawAttachment {
		t.Errorf("expected page-id and attachment approved links; got %+v", links)
	}
	if inText == 0 {
		t.Errorf("expected an in-text placeholder to be flagged separately")
	}
}

func TestLinkDiscovery_EscapedBracesInTarget(t *testing.T) {
	// The producer escapes literal braces in a placeholder target ("{"->"\{", "}"->"\}"). A title
	// containing braces must still be discovered, and its target unescaped back to literal form.
	doc := map[string]any{
		"type": "doc",
		"content": []any{
			map[string]any{
				"type": "paragraph",
				"content": []any{
					map[string]any{
						"type": "text",
						"text": "linky",
						"marks": []any{map[string]any{"type": "link", "attrs": map[string]any{
							"href": `{{CONF_PAGE_TITLE:A\{B\}C}}`,
						}}},
					},
				},
			},
		},
	}
	_, _, links, err := CanonicalizeAndExtractSearchText(marshal(doc))
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	var found *DiscoveredLink
	for i := range links {
		if links[i].Kind == LinkKindPageTitle {
			found = &links[i]
		}
	}
	if found == nil {
		t.Fatalf("expected a page-title placeholder to be discovered; got %+v", links)
	}
	if found.Target != "A{B}C" {
		t.Errorf("target = %q, want unescaped %q", found.Target, "A{B}C")
	}
}

// --- helpers ---

func block(kind string, children ...any) map[string]any {
	m := map[string]any{"type": kind}
	if len(children) > 0 {
		m["content"] = children
	}
	return m
}

func text(s string) map[string]any {
	return map[string]any{"type": "text", "text": s}
}

func marshal(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// nestedBlockquotes returns a doc whose content nests `levels` blockquote nodes, the innermost holding a
// paragraph. levels counts the document's direct children as level 1.
func nestedBlockquotes(levels int) string {
	inner := map[string]any{"type": "paragraph", "content": []any{text("deep")}}
	node := inner
	for range levels - 1 {
		node = map[string]any{"type": "blockquote", "content": []any{node}}
	}
	return marshal(map[string]any{"type": "doc", "content": []any{node}})
}

// TestCanonicalize_DepthAgreesWithSharedSanitizer pins that the importer's walk rejects at exactly the
// nesting the shared sanitizer does, never one level earlier. Starting the walk at depth 1 while the
// sanitizer starts at 0 made the importer stricter than the sanitizer that had just accepted the
// document, so content the browser path stores could not be imported.
func TestCanonicalize_DepthAgreesWithSharedSanitizer(t *testing.T) {
	// Find the first depth the shared sanitizer refuses, then check the importer agrees on both sides of
	// that boundary rather than assuming a particular number.
	firstRejected := 0
	for levels := model.MaxTipTapDepth - 2; levels <= model.MaxTipTapDepth+4; levels++ {
		if _, err := model.ParseTipTapDocument(nestedBlockquotes(levels)); err != nil {
			firstRejected = levels
			break
		}
	}
	if firstRejected == 0 {
		t.Fatalf("the sanitizer accepted every depth probed; the test cannot locate the boundary")
	}

	// One level below the boundary must pass the importer too.
	if _, _, _, err := CanonicalizeAndExtractSearchText(nestedBlockquotes(firstRejected - 1)); err != nil {
		t.Errorf("depth %d is accepted by the sanitizer but rejected by the importer: %v", firstRejected-1, err)
	}
	// At the boundary both must refuse, and the importer must report it as a depth limit rather than as
	// generic malformation, so the HTTP contract can map it to "not processable".
	_, _, _, err := CanonicalizeAndExtractSearchText(nestedBlockquotes(firstRejected))
	te, ok := err.(*TipTapError)
	if !ok || te.Code != TipTapErrTooDeep {
		t.Errorf("depth %d: err = %v, want %s", firstRejected, err, TipTapErrTooDeep)
	}
}

// TestCanonicalize_SanitizerLimitsKeepTheirOwnCodes covers the other half of that mapping: the sanitizer
// reports size, depth, and node-count breaches through one error type, and collapsing them into
// "rejected by the sanitizer" made the size-limit codes unreachable for imported content.
func TestCanonicalize_SanitizerLimitsKeepTheirOwnCodes(t *testing.T) {
	// A body past PageBodyMaxBytes is refused before parsing, by the sanitizer rather than by the
	// importer's own post-marshal check.
	huge := `{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"` +
		strings.Repeat("a", model.PageBodyMaxBytes) + `"}]}]}`
	_, _, _, err := CanonicalizeAndExtractSearchText(huge)
	te, ok := err.(*TipTapError)
	if !ok || te.Code != TipTapErrBodyTooLarge {
		t.Errorf("oversized body: err = %v, want %s", err, TipTapErrBodyTooLarge)
	}

	// Genuine malformation keeps the sanitizer-rejected code, which maps to a client error rather than
	// to "not processable".
	_, _, _, err = CanonicalizeAndExtractSearchText(`{"type":"doc","content":[{"type":"customWidget"}]}`)
	te, ok = err.(*TipTapError)
	if !ok || te.Code != TipTapErrSanitizerRejected {
		t.Errorf("unknown node: err = %v, want %s", err, TipTapErrSanitizerRejected)
	}
}
