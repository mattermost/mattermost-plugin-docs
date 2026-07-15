// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package model_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-docs/server/model"
)

// marshal round-trips a parsed document to JSON so tests can assert on the sanitized output.
func marshal(t *testing.T, doc model.TipTapDocument) string {
	t.Helper()
	b, err := json.Marshal(doc)
	require.NoError(t, err)
	return string(b)
}

func TestParseTipTapDocument(t *testing.T) {
	t.Run("empty string yields an empty doc", func(t *testing.T) {
		doc, err := model.ParseTipTapDocument("")
		require.NoError(t, err)
		require.Equal(t, model.TipTapDocType, doc.Type)
		require.Empty(t, doc.Content)
	})

	t.Run("valid doc round-trips", func(t *testing.T) {
		doc, err := model.ParseTipTapDocument(`{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"hello"}]}]}`)
		require.NoError(t, err)
		require.Equal(t, model.TipTapDocType, doc.Type)
		require.Len(t, doc.Content, 1)
	})

	t.Run("non-doc top-level type is rejected", func(t *testing.T) {
		_, err := model.ParseTipTapDocument(`{"type":"bogus","content":[]}`)
		require.Error(t, err)
	})

	t.Run("malformed JSON is rejected", func(t *testing.T) {
		_, err := model.ParseTipTapDocument(`{not json`)
		require.Error(t, err)
	})

	t.Run("minimal doc without content does not panic", func(t *testing.T) {
		doc, err := model.ParseTipTapDocument(`{"type":"doc"}`)
		require.NoError(t, err)
		require.Empty(t, doc.Content)
	})
}

func TestParseTipTapDocumentSanitizesURLs(t *testing.T) {
	// Each case is a link mark href; want=="" means the scheme must be stripped.
	cases := []struct {
		name string
		href string
		want string
	}{
		{"plain javascript", "javascript:alert(1)", ""},
		{"tab-obfuscated javascript", "java\tscript:alert(1)", ""},
		{"newline-obfuscated javascript", "java\nscript:alert(1)", ""},
		{"leading-control javascript", "\x01javascript:alert(1)", ""},
		{"uppercase javascript", "JAVASCRIPT:alert(1)", ""},
		{"leading-space javascript", "  javascript:alert(1)", ""},
		{"entity-colon javascript", "javascript&colon;alert(1)", ""},
		{"entity-tab javascript", "java&Tab;script:alert(1)", ""},
		{"vbscript", "vbscript:msgbox(1)", ""},
		{"data html", "data:text/html,<script>alert(1)</script>", ""},
		{"data svg", "data:image/svg+xml,<svg onload=alert(1)>", ""},
		{"http allowed", "http://example.com/a", "http://example.com/a"},
		{"https allowed", "https://example.com/a", "https://example.com/a"},
		{"mailto allowed", "mailto:a@example.com", "mailto:a@example.com"},
		{"relative allowed", "/pages/abc", "/pages/abc"},
		{"anchor allowed", "#section", "#section"},
		{"tel allowed", "tel:+15551234567", "tel:+15551234567"},
		{"data png allowed", "data:image/png;base64,iVBORw0KGgo=", "data:image/png;base64,iVBORw0KGgo="},
		{"data png non-image payload rejected", "data:image/png;base64,AAAA", ""},
		{"data png mislabeled non-base64 rejected", "data:image/png,<svg onload=alert(1)>", ""},
		// JPEG: magic bytes FF D8 FF → base64 "/9j/"
		{"data jpeg allowed", "data:image/jpeg;base64,/9j/AAAA", "data:image/jpeg;base64,/9j/AAAA"},
		// GIF89a: magic bytes 47 49 46 38 39 61 → base64 "R0lGODlh"
		{"data gif allowed", "data:image/gif;base64,R0lGODlh", "data:image/gif;base64,R0lGODlh"},
		// BMP: magic bytes 42 4D 00 00 00 00 00 00 00 (9 bytes, no padding) → base64 "Qk0AAAAAAAAA"
		{"data bmp allowed", "data:image/bmp;base64,Qk0AAAAAAAAA", "data:image/bmp;base64,Qk0AAAAAAAAA"},
		// SVG relabeled as JPEG must be rejected (it sniffs as text, not an image).
		{"data jpeg mislabeled svg rejected", "data:image/jpeg;base64,PHN2Zy8+", ""},
		// WebP: "RIFF" + 4 size bytes + "WEBPVP8 " → base64 "UklGRgAAAABXRUJQVlA4IA=="
		{"data webp allowed", "data:image/webp;base64,UklGRgAAAABXRUJQVlA4IA==", "data:image/webp;base64,UklGRgAAAABXRUJQVlA4IA=="},
		// A WAV shares WebP's leading "RIFF" container marker and differs only at byte 8, so the
		// payload must be sniffed past that marker rather than matched on its first four bytes.
		{"data webp mislabeled wav rejected", "data:image/webp;base64,UklGRgAAAABXQVZFZm10IA==", ""},
		// An ICO is a raster image but is not one of the allowed types, so it cannot ride in under
		// an image/png label.
		{"data png mislabeled ico rejected", "data:image/png;base64,AAABAAEA", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := map[string]any{
				"type": "doc",
				"content": []any{
					map[string]any{
						"type": "text",
						"text": "link",
						"marks": []any{
							map[string]any{"type": "link", "attrs": map[string]any{"href": tc.href}},
						},
					},
				},
			}
			b, err := json.Marshal(raw)
			require.NoError(t, err)
			doc, err := model.ParseTipTapDocument(string(b))
			require.NoError(t, err)

			out := marshal(t, doc)
			var parsed map[string]any
			require.NoError(t, json.Unmarshal([]byte(out), &parsed))
			content := parsed["content"].([]any)
			node := content[0].(map[string]any)
			mark := node["marks"].([]any)[0].(map[string]any)
			gotHref := mark["attrs"].(map[string]any)["href"]
			require.Equal(t, tc.want, gotHref, "href sanitization mismatch for %q", tc.href)
		})
	}
}

func TestParseTipTapDocumentRejectsNullNodes(t *testing.T) {
	t.Run("null top-level content node", func(t *testing.T) {
		_, err := model.ParseTipTapDocument(`{"type":"doc","content":[null]}`)
		require.Error(t, err)
	})

	t.Run("null mark in node", func(t *testing.T) {
		_, err := model.ParseTipTapDocument(`{"type":"doc","content":[{"type":"text","text":"hello","marks":[null]}]}`)
		require.Error(t, err)
	})

	t.Run("null child in content", func(t *testing.T) {
		_, err := model.ParseTipTapDocument(`{"type":"doc","content":[{"type":"paragraph","content":[null]}]}`)
		require.Error(t, err)
	})

	t.Run("node with empty type field", func(t *testing.T) {
		_, err := model.ParseTipTapDocument(`{"type":"doc","content":[{"type":""}]}`)
		require.Error(t, err)
	})

	t.Run("node with no type field", func(t *testing.T) {
		_, err := model.ParseTipTapDocument(`{"type":"doc","content":[{"text":"orphan"}]}`)
		require.Error(t, err)
	})
}

func TestParseTipTapDocumentDropsScriptAttributes(t *testing.T) {
	// An image node carrying event-handler and style attributes must have them stripped while a
	// safe src survives.
	raw := map[string]any{
		"type": "doc",
		"content": []any{
			map[string]any{
				"type": "image",
				"attrs": map[string]any{
					"src":     "https://example.com/cat.png",
					"onerror": "alert(document.cookie)",
					"onload":  "steal()",
					"style":   "background:url(javascript:alert(1))",
					"alt":     "a cat",
				},
			},
		},
	}
	b, err := json.Marshal(raw)
	require.NoError(t, err)
	doc, err := model.ParseTipTapDocument(string(b))
	require.NoError(t, err)

	var parsed map[string]any
	require.NoError(t, json.Unmarshal([]byte(marshal(t, doc)), &parsed))
	attrs := parsed["content"].([]any)[0].(map[string]any)["attrs"].(map[string]any)

	require.Equal(t, "https://example.com/cat.png", attrs["src"], "safe src should survive")
	require.Equal(t, "a cat", attrs["alt"], "non-script attr should survive")
	require.NotContains(t, attrs, "onerror")
	require.NotContains(t, attrs, "onload")
	require.NotContains(t, attrs, "style")
}

func TestParseTipTapDocumentSanitizesCaseInsensitiveAttrs(t *testing.T) {
	// HTML attribute names are case-insensitive, so a mixed-case event handler or URL attribute
	// must be sanitized the same as its lowercase form.
	raw := map[string]any{
		"type": "doc",
		"content": []any{
			map[string]any{
				"type": "text",
				"text": "link",
				"marks": []any{
					map[string]any{"type": "link", "attrs": map[string]any{
						"HREF":    "javascript:alert(1)",
						"OnClick": "steal()",
					}},
				},
			},
		},
	}
	b, err := json.Marshal(raw)
	require.NoError(t, err)
	doc, err := model.ParseTipTapDocument(string(b))
	require.NoError(t, err)

	var parsed map[string]any
	require.NoError(t, json.Unmarshal([]byte(marshal(t, doc)), &parsed))
	attrs := parsed["content"].([]any)[0].(map[string]any)["marks"].([]any)[0].(map[string]any)["attrs"].(map[string]any)

	require.Equal(t, "", attrs["HREF"], "uppercase HREF with a dangerous scheme must be neutralized")
	require.NotContains(t, attrs, "OnClick", "mixed-case event handler must be stripped")
}

func TestParseTipTapDocumentStripsDangerousAttrs(t *testing.T) {
	// Attributes that can execute script or embed foreign markup must be dropped regardless of
	// value, even on a node type the sanitizer does not otherwise recognize.
	raw := map[string]any{
		"type": "doc",
		"content": []any{
			map[string]any{
				"type": "blockquote",
				"attrs": map[string]any{
					"srcdoc":     "<script>alert(1)</script>",
					"background": "javascript:alert(1)",
					"data":       "javascript:alert(1)",
					"action":     "https://evil.example",
					"ping":       "https://evil.example",
					"srcset":     "javascript:alert(1)",
					"title":      "safe",
				},
			},
		},
	}
	b, err := json.Marshal(raw)
	require.NoError(t, err)
	doc, err := model.ParseTipTapDocument(string(b))
	require.NoError(t, err)

	var parsed map[string]any
	require.NoError(t, json.Unmarshal([]byte(marshal(t, doc)), &parsed))
	attrs := parsed["content"].([]any)[0].(map[string]any)["attrs"].(map[string]any)

	for _, k := range []string{"srcdoc", "background", "data", "action", "ping", "srcset"} {
		require.NotContains(t, attrs, k, "%s must be stripped", k)
	}
	require.Equal(t, "safe", attrs["title"], "non-dangerous attr should survive")
}

func TestParseTipTapDocumentSanitizesFlatMarkHref(t *testing.T) {
	// A mark that carries href directly on the mark object (not under attrs) must still be neutralized.
	raw := map[string]any{
		"type": "doc",
		"content": []any{
			map[string]any{
				"type":  "text",
				"text":  "link",
				"marks": []any{map[string]any{"type": "link", "href": "javascript:alert(1)"}},
			},
		},
	}
	b, err := json.Marshal(raw)
	require.NoError(t, err)
	doc, err := model.ParseTipTapDocument(string(b))
	require.NoError(t, err)
	require.NotContains(t, marshal(t, doc), "javascript:alert")
}

func TestParseTipTapDocumentSanitizesFlatNodeKeys(t *testing.T) {
	// A node that carries a handler or href directly on the node object (not under attrs) must be
	// neutralized, exactly as the same shape is on a mark.
	raw := map[string]any{
		"type": "doc",
		"content": []any{
			map[string]any{
				"type":    "paragraph",
				"onclick": "alert(document.cookie)",
				"href":    "javascript:alert(1)",
				"content": []any{map[string]any{"type": "text", "text": "hi"}},
			},
		},
	}
	b, err := json.Marshal(raw)
	require.NoError(t, err)
	doc, err := model.ParseTipTapDocument(string(b))
	require.NoError(t, err)
	out := marshal(t, doc)
	require.NotContains(t, out, "onclick")
	require.NotContains(t, out, "javascript:alert")
	require.Contains(t, out, "hi", "the node's legitimate content must survive")
}

func TestParseTipTapDocumentSanitizesNestedAttrs(t *testing.T) {
	// A URL nested inside a sub-object of attrs must be neutralized, not passed through because the
	// sanitizer only looked at the top level of the attrs map.
	raw := map[string]any{
		"type": "doc",
		"content": []any{
			map[string]any{
				"type": "image",
				"attrs": map[string]any{
					"config": map[string]any{"href": "javascript:alert(1)"},
					"list":   []any{map[string]any{"src": "javascript:alert(2)"}},
				},
			},
		},
	}
	b, err := json.Marshal(raw)
	require.NoError(t, err)
	doc, err := model.ParseTipTapDocument(string(b))
	require.NoError(t, err)
	require.NotContains(t, marshal(t, doc), "javascript:alert")
}

func TestParseTipTapDocumentRejectsDeeplyNestedAttrs(t *testing.T) {
	// A dangerous URL buried under attrs nested past the depth cap must NOT slip through
	// unsanitized: the attrs walk fails closed (rejects the whole document) rather than silently
	// stopping and leaving the over-deep subtree untouched. Guards against a fail-open sanitizer
	// bypass where a single shallow node hides a deep attrs subtree.
	depth := 150
	deep := `{"type":"doc","content":[{"type":"image","attrs":` +
		strings.Repeat(`{"a":`, depth) +
		`{"href":"javascript:alert(1)"}` +
		strings.Repeat(`}`, depth) +
		`}]}`
	doc, err := model.ParseTipTapDocument(deep)
	require.Error(t, err, "attrs nested beyond the limit must be rejected, not silently passed through")
	require.NotContains(t, marshal(t, doc), "javascript:alert", "no unsanitized payload may survive")
}

func TestParseTipTapDocumentDropsNonStringURLAttr(t *testing.T) {
	// A URL attribute whose value is not a string (e.g. a JSON array) can be coerced back into a
	// dangerous string by a client renderer, so it must be dropped rather than left untouched.
	raw := map[string]any{
		"type": "doc",
		"content": []any{
			map[string]any{
				"type": "text",
				"text": "link",
				"marks": []any{
					map[string]any{"type": "link", "attrs": map[string]any{"href": []any{"javascript:alert(1)"}}},
				},
			},
		},
	}
	b, err := json.Marshal(raw)
	require.NoError(t, err)
	doc, err := model.ParseTipTapDocument(string(b))
	require.NoError(t, err)

	var parsed map[string]any
	require.NoError(t, json.Unmarshal([]byte(marshal(t, doc)), &parsed))
	attrs := parsed["content"].([]any)[0].(map[string]any)["marks"].([]any)[0].(map[string]any)["attrs"].(map[string]any)
	require.NotContains(t, attrs, "href", "non-string URL attr must be dropped")
}

func TestParseTipTapDocumentRejectsTooDeep(t *testing.T) {
	// A pathologically deep document is rejected rather than walked.
	depth := 200
	deep := `{"type":"doc","content":` + strings.Repeat(`[{"type":"x","content":`, depth) + `[]` + strings.Repeat(`}]`, depth) + `}`
	_, err := model.ParseTipTapDocument(deep)
	require.Error(t, err, "content nested beyond the limit must be rejected")
}

func TestParseTipTapDocumentStripsAdditionalDangerousAttrs(t *testing.T) {
	// formaction, dynsrc, and lowsrc are in the denylist but not covered by the main dangerous-attrs test.
	raw := map[string]any{
		"type": "doc",
		"content": []any{
			map[string]any{
				"type": "form",
				"attrs": map[string]any{
					"formaction": "https://evil.example",
					"dynsrc":     "javascript:alert(1)",
					"lowsrc":     "javascript:alert(2)",
					"title":      "safe",
				},
			},
		},
	}
	b, err := json.Marshal(raw)
	require.NoError(t, err)
	doc, err := model.ParseTipTapDocument(string(b))
	require.NoError(t, err)

	var parsed map[string]any
	require.NoError(t, json.Unmarshal([]byte(marshal(t, doc)), &parsed))
	attrs := parsed["content"].([]any)[0].(map[string]any)["attrs"].(map[string]any)
	require.NotContains(t, attrs, "formaction")
	require.NotContains(t, attrs, "dynsrc")
	require.NotContains(t, attrs, "lowsrc")
	require.Equal(t, "safe", attrs["title"])
}

func TestBuildSearchText(t *testing.T) {
	t.Run("extracts and joins text", func(t *testing.T) {
		doc, err := model.ParseTipTapDocument(`{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"hello"},{"type":"text","text":"world"}]}]}`)
		require.NoError(t, err)
		require.Equal(t, "hello world", model.BuildSearchText(doc))
	})

	t.Run("extracts mention labels", func(t *testing.T) {
		doc, err := model.ParseTipTapDocument(`{"type":"doc","content":[{"type":"mention","attrs":{"label":"alice"}}]}`)
		require.NoError(t, err)
		require.Equal(t, "@alice", model.BuildSearchText(doc))
	})

	t.Run("extracts channelMention label", func(t *testing.T) {
		doc, err := model.ParseTipTapDocument(`{"type":"doc","content":[{"type":"channelMention","attrs":{"label":"general"}}]}`)
		require.NoError(t, err)
		require.Equal(t, "@general", model.BuildSearchText(doc))
	})

	t.Run("empty document returns empty string", func(t *testing.T) {
		doc, err := model.ParseTipTapDocument(`{"type":"doc","content":[]}`)
		require.NoError(t, err)
		require.Equal(t, "", model.BuildSearchText(doc))
	})

	t.Run("collapses whitespace", func(t *testing.T) {
		doc, err := model.ParseTipTapDocument(`{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"a   b\n\nc"}]}]}`)
		require.NoError(t, err)
		require.Equal(t, "a b c", model.BuildSearchText(doc))
	})
}
