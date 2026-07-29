// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package importer

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/mattermost/mattermost-plugin-docs/server/model"
)

// TipTapError describes a rejected TipTap document with a stable code.
type TipTapError struct {
	Code    string
	Message string
}

func (e *TipTapError) Error() string { return e.Message }

func tiptapErr(code, format string, args ...any) *TipTapError {
	return &TipTapError{Code: code, Message: fmt.Sprintf(format, args...)}
}

// Stable TipTap rejection codes.
const (
	TipTapErrInvalidJSON    = "tiptap_invalid_json"
	TipTapErrNotDoc         = "tiptap_not_doc"
	TipTapErrMissingType    = "tiptap_missing_type"
	TipTapErrBadContent     = "tiptap_bad_content"
	TipTapErrBadMarks       = "tiptap_bad_marks"
	TipTapErrBadText        = "tiptap_bad_text"
	TipTapErrTooManyNodes   = "tiptap_too_many_nodes"
	TipTapErrTooDeep        = "tiptap_too_deep"
	TipTapErrBodyTooLarge   = "tiptap_body_too_large"
	TipTapErrSearchTooLarge = "tiptap_search_text_too_large"
)

// Node/mark type names with SearchText significance.
const (
	nodeTypeDoc       = "doc"
	nodeTypeText      = "text"
	nodeTypeHardBreak = "hardBreak"
	nodeTypeImage     = "image"
	markTypeLink      = "link"
)

// blockSeparatorTypes are node types after which SearchText emits a newline separator so distinct
// blocks do not run together.
var blockSeparatorTypes = map[string]struct{}{
	"paragraph":   {},
	"heading":     {},
	"listItem":    {},
	"codeBlock":   {},
	"blockquote":  {},
	"tableCell":   {},
	"tableHeader": {},
	"tableRow":    {},
}

// CanonicalizeAndExtractSearchText validates a TipTap document (a JSON string), returning its
// compact canonical re-marshaling, the derived plain-text SearchText, and any placeholder links
// discovered in approved attributes. Unknown node/mark types and attributes are preserved.
//
// The canonical body is a deterministic compact re-marshaling (Go sorts object keys), so it is
// stable for hashing regardless of the producer's original key order.
func CanonicalizeAndExtractSearchText(body string) (canonicalBody string, searchText string, links []DiscoveredLink, err error) {
	dec := json.NewDecoder(strings.NewReader(body))
	dec.UseNumber()

	var root any
	if decErr := dec.Decode(&root); decErr != nil {
		return "", "", nil, tiptapErr(TipTapErrInvalidJSON, "content is not valid JSON: %v", decErr)
	}
	// Reject trailing data after the first JSON value. json.Decoder.More() cannot be used here: it
	// returns false before a closing "]"/"}" delimiter, so "{...}]" would slip through. Decoding a
	// second value and requiring io.EOF rejects any trailing token while still tolerating trailing
	// whitespace.
	if decErr := dec.Decode(&struct{}{}); !errors.Is(decErr, io.EOF) {
		return "", "", nil, tiptapErr(TipTapErrInvalidJSON, "content has trailing data after the root JSON value")
	}

	rootObj, ok := root.(map[string]any)
	if !ok {
		return "", "", nil, tiptapErr(TipTapErrNotDoc, "content root is not a JSON object")
	}
	if t, _ := rootObj["type"].(string); t != nodeTypeDoc {
		return "", "", nil, tiptapErr(TipTapErrNotDoc, "content root type is not %q", nodeTypeDoc)
	}

	w := &tiptapWalker{}
	if walkErr := w.walkNode(rootObj, 0); walkErr != nil {
		return "", "", nil, walkErr
	}

	compact, marshalErr := json.Marshal(rootObj)
	if marshalErr != nil {
		return "", "", nil, tiptapErr(TipTapErrInvalidJSON, "failed to re-marshal content: %v", marshalErr)
	}
	if len(compact) > model.PageBodyMaxBytes {
		return "", "", nil, tiptapErr(TipTapErrBodyTooLarge, "canonical body is %d bytes, limit is %d", len(compact), model.PageBodyMaxBytes)
	}

	st := normalizeSearchText(w.search.String())
	if len(st) > model.PageSearchTextMaxBytes {
		return "", "", nil, tiptapErr(TipTapErrSearchTooLarge, "search text is %d bytes, limit is %d", len(st), model.PageSearchTextMaxBytes)
	}

	return string(compact), st, w.links, nil
}

// tiptapWalker accumulates SearchText and discovered links across a depth-first traversal, while
// enforcing node-count and depth limits.
type tiptapWalker struct {
	search    strings.Builder
	links     []DiscoveredLink
	nodeCount int
}

// walkNode processes one node object at the given nesting depth (root doc is depth 0).
func (w *tiptapWalker) walkNode(node map[string]any, depth int) error {
	w.nodeCount++
	if w.nodeCount > MaxTipTapNodes {
		return tiptapErr(TipTapErrTooManyNodes, "document has more than %d nodes", MaxTipTapNodes)
	}
	if depth > MaxTipTapDepth {
		return tiptapErr(TipTapErrTooDeep, "document nesting exceeds depth %d", MaxTipTapDepth)
	}

	// Every node must carry a non-empty string type. A missing or empty type is not an "unknown
	// type" (which is preserved) but a structurally invalid node ProseMirror/TipTap clients cannot
	// deserialize.
	rawType, hasType := node["type"]
	nodeType, typeIsString := rawType.(string)
	if !hasType || !typeIsString || nodeType == "" {
		return tiptapErr(TipTapErrMissingType, "node is missing a non-empty string type")
	}

	// Marks (if present) must be an array; scan link marks for placeholder hrefs.
	if rawMarks, present := node["marks"]; present {
		marks, ok := rawMarks.([]any)
		if !ok {
			return tiptapErr(TipTapErrBadMarks, "node %q has a non-array marks field", nodeType)
		}
		for _, rm := range marks {
			mark, ok := rm.(map[string]any)
			if !ok {
				return tiptapErr(TipTapErrBadMarks, "node %q has a non-object mark", nodeType)
			}
			w.scanMark(mark)
		}
	}

	// Image nodes: scan attrs.src.
	if nodeType == nodeTypeImage {
		if src := attrString(node, "src"); src != "" {
			if link, ok := classifyPlaceholder(src, true); ok {
				w.links = append(w.links, link)
			}
		}
	}

	switch nodeType {
	case nodeTypeText:
		rawText, present := node["text"]
		if !present {
			return tiptapErr(TipTapErrBadText, "text node is missing its text field")
		}
		text, ok := rawText.(string)
		if !ok {
			return tiptapErr(TipTapErrBadText, "text node has a non-string text field")
		}
		w.search.WriteString(text)
		if containsPlaceholderToken(text) {
			w.links = append(w.links, DiscoveredLink{Raw: text, InText: true})
		}
	case nodeTypeHardBreak:
		w.search.WriteByte('\n')
	}

	// Recurse into content (if present, must be an array).
	if rawContent, present := node["content"]; present {
		content, ok := rawContent.([]any)
		if !ok {
			return tiptapErr(TipTapErrBadContent, "node %q has a non-array content field", nodeType)
		}
		for _, rc := range content {
			child, ok := rc.(map[string]any)
			if !ok {
				return tiptapErr(TipTapErrBadContent, "node %q has a non-object content child", nodeType)
			}
			if err := w.walkNode(child, depth+1); err != nil {
				return err
			}
		}
	}

	// Emit a block separator after block-level nodes so their text does not merge with the next.
	if _, isBlock := blockSeparatorTypes[nodeType]; isBlock {
		w.search.WriteByte('\n')
	}

	return nil
}

// scanMark records a placeholder discovered in a link mark's href attribute.
func (w *tiptapWalker) scanMark(mark map[string]any) {
	if t, _ := mark["type"].(string); t != markTypeLink {
		return
	}
	href := attrString(mark, "href")
	if href == "" {
		return
	}
	if link, ok := classifyPlaceholder(href, false); ok {
		w.links = append(w.links, link)
	}
}

// attrString reads a string attribute from an object's "attrs" map, returning "" if absent or of a
// non-string type.
func attrString(obj map[string]any, key string) string {
	attrs, ok := obj["attrs"].(map[string]any)
	if !ok {
		return ""
	}
	v, _ := attrs[key].(string)
	return v
}

var (
	// horizontalWhitespace matches runs of spaces/tabs to collapse to a single space.
	horizontalWhitespace = regexp.MustCompile(`[ \t]+`)
	// threePlusNewlines matches runs of 3+ newlines (allowing surrounding horizontal space) to
	// collapse to exactly two.
	threePlusNewlines = regexp.MustCompile(`\n[ \t]*(\n[ \t]*){2,}`)
)

// normalizeSearchText collapses horizontal whitespace and excess blank lines, then trims.
func normalizeSearchText(s string) string {
	// Drop NUL bytes first: a TipTap text node may decode an escaped NUL to a literal NUL byte,
	// which PostgreSQL cannot store in the SearchText TEXT column and would reject at staging insert.
	s = stripNUL(s)
	// Normalize CRLF/CR to LF first so newline collapsing is uniform.
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = horizontalWhitespace.ReplaceAllString(s, " ")
	// Trim trailing horizontal space on each line so " \n" does not defeat newline collapsing.
	s = trimLineTrailingSpaces(s)
	s = threePlusNewlines.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}

// trimLineTrailingSpaces removes trailing spaces/tabs before each newline.
func trimLineTrailingSpaces(s string) string {
	var b bytes.Buffer
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		b.WriteString(strings.TrimRight(line, " \t"))
		if i < len(lines)-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}
