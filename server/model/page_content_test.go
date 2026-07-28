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

// TestParseTipTapDocumentRejectsForbiddenTypes pins the node/mark allowlist — the strongest
// defense in the sanitizer — by asserting that a document carrying an unlisted type is rejected
// outright rather than stripped and accepted.
func TestParseTipTapDocumentRejectsForbiddenTypes(t *testing.T) {
	// Types a lenient renderer could execute, plus a couple of allowed ones as a control.
	nodeCases := []struct {
		nodeType string
		rejected bool
	}{
		{"script", true},
		{"iframe", true},
		{"embed", true},
		{"object", true},
		{"noscript", true},
		{"template", true},
		{"style", true},
		{"link", true},
		{"svg", true},
		{"math", true},
		{"animate", true},
		{"animatetransform", true},
		{"foreignobject", true},
		{"maction", true},
		{"SCRIPT", true}, // the allowlist is case-sensitive, so a cased variant is not listed
		{"IFrame", true},
		{"paragraph", false}, // control: allowed
	}
	for _, tc := range nodeCases {
		t.Run("node type "+tc.nodeType, func(t *testing.T) {
			raw := map[string]any{
				"type":    "doc",
				"content": []any{map[string]any{"type": tc.nodeType}},
			}
			b, err := json.Marshal(raw)
			require.NoError(t, err)
			_, err = model.ParseTipTapDocument(string(b))
			if tc.rejected {
				require.Error(t, err, "forbidden node type %q must be rejected", tc.nodeType)
			} else {
				require.NoError(t, err, "allowed node type %q must parse", tc.nodeType)
			}
		})
	}

	// Unlisted mark types on an otherwise-valid text node. "link" is intentionally absent — it is a
	// valid mark whose href is sanitized rather than blocked (see TestParseTipTapDocumentSanitizesURLs).
	markCases := []struct {
		markType string
		rejected bool
	}{
		{"script", true},
		{"iframe", true},
		{"style", true},
		{"svg", true},
		{"foreignobject", true},
		{"MAction", true}, // cased variant of an unlisted type is still unlisted
		{"link", false},   // control: allowed as a mark
		{"bold", false},   // control: ordinary formatting mark
		{"", true},        // a mark with an empty type is rejected, matching node-type strictness
	}
	for _, tc := range markCases {
		t.Run("mark type "+tc.markType, func(t *testing.T) {
			raw := map[string]any{
				"type": "doc",
				"content": []any{map[string]any{
					"type":  "text",
					"text":  "x",
					"marks": []any{map[string]any{"type": tc.markType}},
				}},
			}
			b, err := json.Marshal(raw)
			require.NoError(t, err)
			_, err = model.ParseTipTapDocument(string(b))
			if tc.rejected {
				require.Error(t, err, "forbidden mark type %q must be rejected", tc.markType)
			} else {
				require.NoError(t, err, "allowed mark type %q must parse", tc.markType)
			}
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

func TestParseTipTapDocumentNeutralizesBareArrayURLStrings(t *testing.T) {
	// A dangerous scheme carried as a bare string inside an array attribute value — under a key an
	// editor extension may define that is not one of the designated URL keys — has no attribute key
	// to mark it a URL, so stripDangerousKeys does not reach it. The array walk must still neutralize
	// the executable scheme while leaving legitimate colon-bearing strings (a timestamp, a ratio) and
	// safe URLs untouched.
	raw := map[string]any{
		"type": "doc",
		"content": []any{
			map[string]any{
				"type": "fileAttachment",
				"attrs": map[string]any{
					"sources": []any{"javascript:alert(1)", "vbscript:msgbox(1)", "http://ok.example", "12:30", "16:9"},
					"nested":  []any{[]any{"javascript:alert(2)"}},
				},
			},
		},
	}
	b, err := json.Marshal(raw)
	require.NoError(t, err)
	doc, err := model.ParseTipTapDocument(string(b))
	require.NoError(t, err)

	out := marshal(t, doc)
	require.NotContains(t, out, "javascript:alert", "bare javascript: array element must be neutralized")
	require.NotContains(t, out, "vbscript:", "bare vbscript: array element must be neutralized")
	require.Contains(t, out, "http://ok.example", "a safe URL in the array must be preserved")
	require.Contains(t, out, "12:30", "a non-URL colon-bearing string must be preserved")
	require.Contains(t, out, "16:9", "a non-URL ratio string must be preserved")

	var parsed map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &parsed))
	attrs := parsed["content"].([]any)[0].(map[string]any)["attrs"].(map[string]any)
	sources := attrs["sources"].([]any)
	require.Equal(t, "", sources[0], "javascript: element blanked in place, preserving array length")
	require.Equal(t, "", sources[1], "vbscript: element blanked in place")
	require.Equal(t, "http://ok.example", sources[2])
}

func TestParseTipTapDocumentSanitizesDataAttrs(t *testing.T) {
	// A data-* attribute is not necessarily a URL: an extension may store a ratio or timestamp
	// under a data-* key. Such colon-bearing values must be preserved (the strict URL allowlist
	// would parse the leading token as a scheme and blank them), while a dangerous scheme carried
	// under a data-* key is still neutralized, and a non-string data-* value is left untouched.
	raw := map[string]any{
		"type": "doc",
		"content": []any{
			map[string]any{
				"type": "fileAttachment",
				"attrs": map[string]any{
					"data-ratio":   "16:9",
					"data-time":    "12:30",
					"data-src":     "https://ok.example/img.png",
					"data-onnav":   "javascript:alert(1)",
					"data-vb":      "vbscript:msgbox(1)",
					"data-index":   float64(5),
					"data-payload": "data:text/html,<script>alert(1)</script>",
					// Mixed-case key: the data- prefix is matched case-insensitively (the key is
					// lowercased before the prefix test), so a dangerous scheme here is still blanked
					// and a colon-bearing non-URL value is still preserved.
					"DATA-Nav": "javascript:alert(2)",
					"Data-Ok":  "21:9",
					// Non-string container under a data-* key: stripDangerousKeys skips the non-string
					// value, and the recursive attr walk sanitizes the nested map's designated URL key.
					"data-config": map[string]any{"href": "javascript:alert(3)", "keep": "plain"},
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

	require.Equal(t, "16:9", attrs["data-ratio"], "a non-URL ratio under a data-* key must be preserved")
	require.Equal(t, "12:30", attrs["data-time"], "a non-URL timestamp under a data-* key must be preserved")
	require.Equal(t, "https://ok.example/img.png", attrs["data-src"], "a safe URL under a data-* key must be preserved")
	require.Equal(t, "", attrs["data-onnav"], "a javascript: scheme under a data-* key must be neutralized")
	require.Equal(t, "", attrs["data-vb"], "a vbscript: scheme under a data-* key must be neutralized")
	require.Equal(t, float64(5), attrs["data-index"], "a non-string data-* value must be left untouched")
	require.Equal(t, "", attrs["data-payload"], "a non-image data: URI under a data-* key must be neutralized")
	require.Equal(t, "", attrs["DATA-Nav"], "a dangerous scheme under a mixed-case data-* key must be neutralized")
	require.Equal(t, "21:9", attrs["Data-Ok"], "a non-URL value under a mixed-case data-* key must be preserved")

	config, ok := attrs["data-config"].(map[string]any)
	require.True(t, ok, "a nested map under a data-* key must be preserved as a map")
	require.Equal(t, "", config["href"], "a dangerous URL nested under a data-* map must be sanitized recursively")
	require.Equal(t, "plain", config["keep"], "a non-URL sibling in the nested map must be preserved")
}

func TestParseTipTapDocumentRejectsTooDeep(t *testing.T) {
	// A pathologically deep document is rejected rather than walked.
	depth := 200
	deep := `{"type":"doc","content":` + strings.Repeat(`[{"type":"blockquote","content":`, depth) + `[]` + strings.Repeat(`}]`, depth) + `}`
	_, err := model.ParseTipTapDocument(deep)
	require.Error(t, err, "content nested beyond the limit must be rejected")
}

func TestParseTipTapDocumentNodeDepthBoundary(t *testing.T) {
	// Pins the exact node-nesting cutoff, mirroring maxTipTapDepth in the sanitize walk
	// (server/model/page_content.go); a drifted or off-by-one cap fails these loudly, unlike the
	// far-past-the-limit rejection test above.
	const depthLimit = 100

	// nestedTo builds a document whose deepest node sits at the given depth (the top-level
	// content node is depth 0).
	nestedTo := func(depth int) string {
		n := depth + 1
		return `{"type":"doc","content":` + strings.Repeat(`[{"type":"blockquote","content":`, n) + `[]` + strings.Repeat(`}]`, n) + `}`
	}

	t.Run("nesting at the limit is accepted", func(t *testing.T) {
		_, err := model.ParseTipTapDocument(nestedTo(depthLimit))
		require.NoError(t, err)
	})

	t.Run("nesting one past the limit is rejected", func(t *testing.T) {
		_, err := model.ParseTipTapDocument(nestedTo(depthLimit + 1))
		require.Error(t, err)
	})
}

func TestParseTipTapDocumentAttrsDepthBoundary(t *testing.T) {
	// Pins the exact attrs-nesting cutoff: the attrs walk shares maxTipTapDepth with the node walk
	// but counts depth independently, so it gets its own boundary pin.
	const depthLimit = 100

	// attrsNestedTo builds a node whose attrs contain a map chain whose deepest map sits at the
	// given depth (the attrs object itself is depth 0).
	attrsNestedTo := func(depth int) string {
		return `{"type":"doc","content":[{"type":"paragraph","attrs":` +
			strings.Repeat(`{"a":`, depth) + `{"b":1}` + strings.Repeat(`}`, depth) + `}]}`
	}

	t.Run("attrs nesting at the limit is accepted", func(t *testing.T) {
		_, err := model.ParseTipTapDocument(attrsNestedTo(depthLimit))
		require.NoError(t, err)
	})

	t.Run("attrs nesting one past the limit is rejected", func(t *testing.T) {
		_, err := model.ParseTipTapDocument(attrsNestedTo(depthLimit + 1))
		require.Error(t, err)
	})
}

func TestParseTipTapDocumentRejectsOffSchemaNodeType(t *testing.T) {
	// A node type outside the allowlist is rejected outright, so a client node type the server does
	// not know about surfaces as a loud failure rather than passing through unrecognized.
	_, err := model.ParseTipTapDocument(`{"type":"doc","content":[{"type":"widget","content":[]}]}`)
	require.Error(t, err, "an off-schema node type must be rejected")
}

func TestParseTipTapDocumentRejectsOffSchemaMarkType(t *testing.T) {
	// Mark types are allowlisted the same as node types: a mark outside the schema is rejected.
	_, err := model.ParseTipTapDocument(`{"type":"doc","content":[{"type":"text","text":"x","marks":[{"type":"blink"}]}]}`)
	require.Error(t, err, "an off-schema mark type must be rejected")
}

func TestParseTipTapDocumentRejectsAllowlistedTypeInWrongCase(t *testing.T) {
	// The allowlist matches case-sensitively (TipTap schema names are camelCase). A case variant of an
	// allowed type is rejected — this also guards the denylist->allowlist switch, since the old
	// case-insensitive denylist would have lowercased "Paragraph" and let it through.
	_, err := model.ParseTipTapDocument(`{"type":"doc","content":[{"type":"Paragraph"}]}`)
	require.Error(t, err, "a wrong-case node type must be rejected under the case-sensitive allowlist")
}

func TestParseTipTapDocumentAllowsSchemaNodeTypes(t *testing.T) {
	// Representative core-schema and extension node types must pass sanitization unchanged.
	for _, nodeType := range []string{"heading", "table", "callout", "taskList", "taskItem", "channelMention", "video", "fileAttachment", "imagePlaceholder", "imageResize"} {
		doc := `{"type":"doc","content":[{"type":"` + nodeType + `","content":[]}]}`
		_, err := model.ParseTipTapDocument(doc)
		require.NoError(t, err, "schema node type %q must be allowed", nodeType)
	}
}

func TestParseTipTapDocumentStripsAdditionalDangerousAttrs(t *testing.T) {
	// formaction, dynsrc, and lowsrc are in the denylist but not covered by the main dangerous-attrs test.
	raw := map[string]any{
		"type": "doc",
		"content": []any{
			map[string]any{
				"type": "image",
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

// buildTipTapContentJSON returns a TipTap document JSON string whose top-level content array holds
// n flat sibling paragraph nodes, built without any nesting so the node count equals n exactly.
func buildTipTapContentJSON(n int) string {
	var b strings.Builder
	b.WriteString(`{"type":"doc","content":[`)
	for i := range n {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(`{"type":"paragraph"}`)
	}
	b.WriteString(`]}`)
	return b.String()
}

func TestParseTipTapDocumentRejectsTooManyNodes(t *testing.T) {
	// Mirrors the maxTipTapNodes guard in sanitizeTipTapDocument (server/model/page_content.go).
	const nodeLimit = 50_000

	t.Run("node count at the limit is accepted", func(t *testing.T) {
		doc, err := model.ParseTipTapDocument(buildTipTapContentJSON(nodeLimit))
		require.NoError(t, err)
		require.Len(t, doc.Content, nodeLimit)
	})

	t.Run("node count exceeding the limit is rejected", func(t *testing.T) {
		_, err := model.ParseTipTapDocument(buildTipTapContentJSON(nodeLimit + 1))
		require.Error(t, err)
		require.Contains(t, err.Error(), "exceeds the maximum of 50000 nodes")
	})
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

func TestParseTipTapDocumentSanitizesWhitespacePrefixedAttrKeys(t *testing.T) {
	// An HTML tokenizer treats "\tonclick" as the onclick attribute, so keys carrying leading or
	// trailing whitespace/control characters must be recognized the same as their clean forms. No
	// editor schema defines a padded name, so every one of them is dropped rather than kept with a
	// sanitized value — a renderer resolving the trimmed name must not find two entries for it.
	raw := map[string]any{
		"type": "doc",
		"content": []any{
			map[string]any{
				"type": "image",
				"attrs": map[string]any{
					" onclick":  "alert(document.cookie)",
					"\tonerror": "steal()",
					" href":     "javascript:alert(1)",
					"alt":       "a cat",
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

	require.NotContains(t, attrs, " onclick", "whitespace-prefixed event handler must be stripped")
	require.NotContains(t, attrs, "\tonerror", "control-char-prefixed event handler must be stripped")
	require.NotContains(t, attrs, " href", "whitespace-prefixed URL key must be dropped, not kept sanitized")
	require.NotContains(t, attrs, "href", "dropping the padded key must not introduce a canonical one")
	require.Equal(t, "a cat", attrs["alt"], "clean attr must survive")
}
