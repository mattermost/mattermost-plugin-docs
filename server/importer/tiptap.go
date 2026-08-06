// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package importer

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
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
	TipTapErrInvalidJSON       = "tiptap_invalid_json"
	TipTapErrNotDoc            = "tiptap_not_doc"
	TipTapErrMissingType       = "tiptap_missing_type"
	TipTapErrBadContent        = "tiptap_bad_content"
	TipTapErrBadMarks          = "tiptap_bad_marks"
	TipTapErrBadText           = "tiptap_bad_text"
	TipTapErrUnstorableText    = "tiptap_unstorable_text"
	TipTapErrSanitizerRejected = "tiptap_sanitizer_rejected"
	TipTapErrTooManyNodes      = "tiptap_too_many_nodes"
	TipTapErrTooDeep           = "tiptap_too_deep"
	TipTapErrBodyTooLarge      = "tiptap_body_too_large"
	TipTapErrSearchTooLarge    = "tiptap_search_text_too_large"
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

// CanonicalizeAndExtractSearchText sanitizes a TipTap document (a JSON string) through the
// repository's shared sanitizer, then returns its compact canonical re-marshaling, the derived
// plain-text SearchText, and any placeholder links discovered in approved attributes.
//
// Sanitization is not optional and is not reimplemented here: model.ParseTipTapDocument is the
// declared invariant for anything stored as page content (see model/page_content.go), and the import
// path writes pages through a dedicated store path that bypasses the interactive one. Running the
// same sanitizer means imported content cannot carry a javascript: href, an event-handler attribute,
// or a node/mark type the editor schema does not define — a bundle is external, untrusted input and
// gets exactly the same treatment as a browser payload.
//
// Note that this deliberately overrides the plan's earlier "preserve unknown node/mark types"
// instruction: preserving an unknown type is precisely what would let unsanitized content through.
// Every node and mark the authoritative producer emits is on the sanitizer's allowlist.
//
// The canonical body is a deterministic compact re-marshaling of the *sanitized* document (Go sorts
// object keys), so it is stable for hashing and always reflects what will actually be stored.
func CanonicalizeAndExtractSearchText(body string) (canonicalBody string, searchText string, links []DiscoveredLink, err error) {
	// An absent body is a producer error rather than an empty page: ParseTipTapDocument would
	// helpfully turn "" into an empty doc, which would silently import a blank page.
	if strings.TrimSpace(body) == "" {
		return "", "", nil, tiptapErr(TipTapErrInvalidJSON, "content is empty")
	}

	doc, parseErr := model.ParseTipTapDocument(body)
	if parseErr != nil {
		// The sanitizer returns plain errors (its failures are parse-level, with no per-field i18n
		// key), so they are wrapped in one stable code here. Its message is safe to surface: it names
		// node/mark types and limits, never content.
		// Keep the sanitizer's limit rejections distinct from its structural ones. Both come back as one
		// error type, but a document that breaches a size or nesting bound is well-formed and merely
		// unprocessable, while one the allowlist rejects is malformed — a different HTTP contract, and the
		// only reason the size-limit codes below are reachable at all.
		switch {
		case errors.Is(parseErr, model.ErrTipTapBodyTooLarge):
			return "", "", nil, tiptapErr(TipTapErrBodyTooLarge, "content exceeds the maximum body size of %d bytes", model.PageBodyMaxBytes)
		case errors.Is(parseErr, model.ErrTipTapTooDeep):
			return "", "", nil, tiptapErr(TipTapErrTooDeep, "document nesting exceeds depth %d", model.MaxTipTapDepth)
		case errors.Is(parseErr, model.ErrTipTapTooManyNodes):
			return "", "", nil, tiptapErr(TipTapErrTooManyNodes, "document has more than %d nodes", model.MaxTipTapNodes)
		default:
			return "", "", nil, tiptapErr(TipTapErrSanitizerRejected, "content rejected by the page sanitizer: %v", parseErr)
		}
	}

	compact, marshalErr := json.Marshal(doc)
	if marshalErr != nil {
		return "", "", nil, tiptapErr(TipTapErrInvalidJSON, "failed to re-marshal sanitized content: %v", marshalErr)
	}
	if len(compact) > model.PageBodyMaxBytes {
		return "", "", nil, tiptapErr(TipTapErrBodyTooLarge, "canonical body is %d bytes, limit is %d", len(compact), model.PageBodyMaxBytes)
	}

	// Walk the sanitized document for SearchText and placeholder links. The traversal also enforces
	// the two text-shape rules the sanitizer does not check (a text node must carry a string "text"),
	// and rejects NUL/invalid UTF-8, which PostgreSQL cannot store.
	w := &tiptapWalker{}
	for _, node := range doc.Content {
		if node == nil {
			return "", "", nil, tiptapErr(TipTapErrBadContent, "sanitized content contains a null node")
		}
		// Depth 0 for the document's direct children, matching how the shared sanitizer numbers them
		// (sanitizeTipTapDocument enters sanitizeTipTapNode at 0). Starting at 1 here made this walk one
		// level stricter than the sanitizer that just accepted the document, so imported content could be
		// rejected at a nesting the browser path allows.
		if walkErr := w.walkNode(node, 0); walkErr != nil {
			return "", "", nil, walkErr
		}
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

// walkNode processes one node object at the given nesting depth. The document's direct children are
// depth 0, exactly as the shared sanitizer counts them, so both enforce model.MaxTipTapDepth at the same
// nesting. The node has already been sanitized; these checks are the text-shape and storability rules the
// sanitizer does not cover, plus defence-in-depth bounds.
func (w *tiptapWalker) walkNode(node map[string]any, depth int) error {
	w.nodeCount++
	if w.nodeCount > model.MaxTipTapNodes {
		return tiptapErr(TipTapErrTooManyNodes, "document has more than %d nodes", model.MaxTipTapNodes)
	}
	if depth > model.MaxTipTapDepth {
		return tiptapErr(TipTapErrTooDeep, "document nesting exceeds depth %d", model.MaxTipTapDepth)
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
		// Reject rather than sanitize: a NUL cannot be stored in the body/SearchText columns and
		// invalid UTF-8 would be silently mutated, so a document carrying either is refused outright
		// instead of being altered behind the user's back.
		if !IsStorableText(text) {
			return tiptapErr(TipTapErrUnstorableText, "text node contains invalid UTF-8 or a NUL character")
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
