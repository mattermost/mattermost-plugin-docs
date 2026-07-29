// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package importer

import (
	"encoding/json"
	"strings"
	"testing"
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

func TestCanonicalize_PreservesUnknownTypes(t *testing.T) {
	body := `{"type":"doc","content":[{"type":"customWidget","attrs":{"foo":"bar"},"content":[{"type":"text","text":"hi"}]}]}`
	canon, search, _, err := CanonicalizeAndExtractSearchText(body)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if !strings.Contains(canon, "customWidget") || !strings.Contains(canon, "bar") {
		t.Errorf("unknown node/attr not preserved: %s", canon)
	}
	if search != "hi" {
		t.Errorf("search = %q, want hi", search)
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
	// A child node with no type is structurally invalid, not an unknown type.
	body := `{"type":"doc","content":[{"attrs":{"x":1},"content":[{"type":"text","text":"hi"}]}]}`
	_, _, _, err := CanonicalizeAndExtractSearchText(body)
	if te, ok := err.(*TipTapError); !ok || te.Code != TipTapErrMissingType {
		t.Fatalf("err = %v, want %s", err, TipTapErrMissingType)
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
