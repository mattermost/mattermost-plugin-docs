// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package model

import (
	"encoding/base64"
	"encoding/json"
	"html"
	"net/http"
	"slices"
	"strings"

	"github.com/pkg/errors"
)

const (
	TipTapDocType   = "doc"
	EmptyTipTapJSON = `{"type":"doc","content":[]}`
)

// TipTapDocument is the parsed form of a TipTap editor document. Content is left as an untyped node
// tree because node attributes and nesting vary per editor extension; the permitted node and mark
// type names are constrained by allowedNodeTypes/allowedMarkTypes during sanitization. Client-supplied
// content must be produced by ParseTipTapDocument for the sanitization invariant to hold — a value
// decoded from client JSON any other way has not passed sanitizeTipTapDocument and must not be
// stored or rendered. The one exception is a document assembled internally from trusted parts (e.g.
// convertPlainTextToTipTap, which emits only paragraph and text nodes with no attrs or marks): it is
// safe by construction and needs no sanitization pass.
type TipTapDocument struct {
	Type    string           `json:"type"`
	Content []map[string]any `json:"content"`
}

// BuildSearchText extracts searchable plain text from a TipTap document.
func BuildSearchText(doc TipTapDocument) string {
	var b strings.Builder
	for _, node := range doc.Content {
		appendNodeText(&b, node, 0)
	}
	return b.String()
}

// ParseTipTapDocument parses and sanitizes a TipTap JSON string into a TipTapDocument. Unlike the
// model's field validators, it returns a plain error (not an *mmmodel.AppError): its failures are
// parse-level, not per-field, so there is no single i18n key to attach per reason.
func ParseTipTapDocument(contentJSON string) (TipTapDocument, error) {
	if contentJSON == "" {
		return TipTapDocument{
			Type:    TipTapDocType,
			Content: []map[string]any{},
		}, nil
	}

	// Reject over-limit content before parsing: json.Unmarshal materializes every node as a
	// map[string]any at a large multiple of its encoded size, and the per-node budget inside the
	// sanitize walk only applies after that allocation.
	if len(contentJSON) > PageBodyMaxBytes {
		return TipTapDocument{}, errors.New("content exceeds the maximum body size")
	}

	var doc TipTapDocument
	if err := json.Unmarshal([]byte(contentJSON), &doc); err != nil {
		return TipTapDocument{}, err
	}

	if doc.Type != TipTapDocType {
		return TipTapDocument{}, errors.New("content must be valid TipTap JSON with type: doc")
	}

	// A document with no "content" key (or an explicit null) decodes to a nil slice, which would
	// re-marshal to "content":null. Empty content is [] everywhere else, and a client walking the
	// array would fault on null, so normalize to the one empty representation.
	if doc.Content == nil {
		doc.Content = []map[string]any{}
	}

	if err := sanitizeTipTapDocument(&doc); err != nil {
		return TipTapDocument{}, err
	}
	return doc, nil
}

// appendNodeText walks a node subtree once, appending each text leaf and mention label to b. A
// single shared builder keeps extraction O(total text) instead of re-joining every subtree at each
// ancestor, which would copy a leaf once per level of nesting.
func appendNodeText(b *strings.Builder, node map[string]any, depth int) {
	if depth > maxTipTapDepth {
		return
	}

	if textVal, ok := node["text"]; ok {
		if text, ok := textVal.(string); ok && text != "" {
			if normalized := strings.Join(strings.Fields(text), " "); normalized != "" {
				writeSearchTextPart(b, normalized)
			}
		}
	}

	if nodeType, ok := node["type"].(string); ok && (nodeType == "mention" || nodeType == "channelMention") {
		if attrs, ok := node["attrs"].(map[string]any); ok {
			if label, ok := attrs["label"].(string); ok && label != "" {
				writeSearchTextPart(b, "@"+label)
			} else if id, ok := attrs["id"].(string); ok && id != "" {
				writeSearchTextPart(b, "@"+id)
			}
		}
	}

	if contentVal, ok := node["content"]; ok {
		if children, ok := contentVal.([]any); ok {
			for _, child := range children {
				if childNode, ok := child.(map[string]any); ok {
					appendNodeText(b, childNode, depth+1)
				}
			}
		}
	}
}

// writeSearchTextPart appends part to b, separated from any prior content by a single space.
func writeSearchTextPart(b *strings.Builder, part string) {
	if b.Len() > 0 {
		b.WriteByte(' ')
	}
	b.WriteString(part)
}

// maxTipTapDepth bounds recursion over client-supplied content. encoding/json already caps nesting,
// but this rejects a pathologically deep document before the recursive walk and keeps stored content
// within a sane depth.
const maxTipTapDepth = 100

// maxTipTapNodes caps the total number of content nodes in a TipTap document, bounding the
// sanitize walk and the stored node count. It applies after json.Unmarshal has materialized the
// document, so it does not bound the parse itself — the pre-parse body-size check at the top of
// ParseTipTapDocument does that. The plain-text path is capped at maxPlainTextParagraphs
// (10 000 paragraphs → ~20 000 nodes, since each non-empty line is a paragraph plus a text child);
// rich documents with ~5 inline nodes per paragraph stay well under 50 000 for any sane document.
const maxTipTapNodes = 50_000

var errAttrDepthExceeded = errors.New("content attribute nesting exceeds the maximum depth")

func sanitizeTipTapDocument(doc *TipTapDocument) error {
	// count is a running node budget shared across the walk: sanitizeTipTapNode increments it per
	// node and bails once it crosses maxTipTapNodes, so a crafted payload is rejected before the
	// walk allocates without bound — without a separate counting pass over the whole tree.
	count := 0
	for i := range doc.Content {
		if doc.Content[i] == nil {
			return errors.New("content document nodes must be objects")
		}
		if err := sanitizeTipTapNode(doc.Content[i], 0, &count); err != nil {
			return err
		}
	}
	return nil
}

// urlAttrKeys are the attribute keys (matched case-insensitively) whose values are URLs and must
// pass through sanitizeURL.
var urlAttrKeys = map[string]struct{}{
	"href":       {},
	"src":        {},
	"poster":     {},
	"xlink:href": {},
	"xlinkhref":  {},
}

// allowedNodeTypes is the allowlist of TipTap node type values permitted in stored content, keyed to
// the core WysiwygEditor schema plus the extensions the page editor augments it with. A node type not
// listed here is rejected outright: this pins the server's accepted node set to the client's schema so
// the two cannot drift silently, and it fails closed for any script- or embed-bearing type the client
// never emits. Matched case-sensitively, since TipTap schema names are case-sensitive camelCase.
//
// This is a hand-maintained mirror of the editor schema: when the page editor gains a node type — a
// core WysiwygEditor/StarterKit change or a Docs-specific extension — add it here in the same change,
// or the new content is rejected on save.
var allowedNodeTypes = map[string]struct{}{
	// Core WysiwygEditor schema (StarterKit + Link + CodeBlockLowlight + Table).
	"doc":            {},
	"paragraph":      {},
	"text":           {},
	"heading":        {},
	"hardBreak":      {},
	"horizontalRule": {},
	"blockquote":     {},
	"codeBlock":      {},
	"bulletList":     {},
	"orderedList":    {},
	"listItem":       {},
	"table":          {},
	"tableRow":       {},
	"tableCell":      {},
	"tableHeader":    {},
	// Page editor extensions layered on the core schema.
	"taskList":         {},
	"taskItem":         {},
	"mention":          {},
	"channelMention":   {},
	"callout":          {},
	"image":            {},
	"imageResize":      {},
	"imagePlaceholder": {},
	"video":            {},
	"fileAttachment":   {},
}

// allowedMarkTypes is the allowlist of TipTap mark type values, keyed to the core WysiwygEditor
// schema plus the page editor's extensions. Like allowedNodeTypes, a mark type not listed here is
// rejected rather than passed through. "link" is a mark (TipTap's inline hyperlink); its href is
// sanitized by sanitizeURL.
var allowedMarkTypes = map[string]struct{}{
	// Core WysiwygEditor marks.
	"bold":      {},
	"italic":    {},
	"strike":    {},
	"code":      {},
	"underline": {},
	"link":      {},
	// Page editor extensions.
	"textStyle":     {},
	"commentAnchor": {},
}

// dangerousAttrKeys are attribute keys (matched case-insensitively) stripped outright regardless of
// value: they can execute script or embed foreign markup, and no supported node needs them. This
// denylist is layered on top of the URL-scheme allowlist.
var dangerousAttrKeys = map[string]struct{}{
	"style":      {},
	"formaction": {},
	"action":     {},
	"srcdoc":     {},
	"srcset":     {},
	"background": {},
	"dynsrc":     {},
	"lowsrc":     {},
	"ping":       {},
	"data":       {},
}

// trimBrowserIgnoredChars trims leading/trailing ASCII space and control characters (r <= ' ') —
// the characters an HTML tokenizer ignores around attribute names and a browser strips from the
// ends of a URL — so a sanitizer match cannot be defeated by padding with them.
func trimBrowserIgnoredChars(s string) string {
	return strings.TrimFunc(s, func(r rune) bool { return r <= ' ' })
}

// stripDangerousKeys strips script-bearing keys (event handlers plus the dangerousAttrKeys set) and
// neutralizes dangerous URL schemes on any URL-valued key, at the top level of m only. Key names are
// matched case-insensitively, since HTML attribute names are case-insensitive.
//
// It is applied to attribute maps and also to the node and mark objects themselves, because a
// lenient client renderer may read a dangerous or URL-valued key placed directly on the object
// rather than nested under its "attrs".
func stripDangerousKeys(m map[string]any) {
	for key, val := range m {
		// Trim leading/trailing whitespace and control characters before matching: an HTML
		// tokenizer treats "\tonclick" as the onclick attribute, so a key that fails to match
		// here because of such a prefix would carry its payload through every check below.
		lower := strings.ToLower(trimBrowserIgnoredChars(key))
		if _, dangerous := dangerousAttrKeys[lower]; strings.HasPrefix(lower, "on") || dangerous {
			delete(m, key)
			continue
		}
		// A data-* attribute may carry a URL a lenient client renderer treats as a navigation
		// target, but it is not necessarily one: an editor extension may store a ratio ("16:9"), a
		// timestamp ("12:30"), or any other colon-bearing value under a data-* key. The strict URL
		// allowlist would read the leading token as a scheme and blank every such value, so
		// neutralize only the unambiguously dangerous schemes here (javascript:/vbscript:/non-image
		// data:), matching the bare-array-string path. A non-string value (number, nested map/array)
		// carries no scheme to strip and is left for the recursive attr walk to handle.
		if strings.HasPrefix(lower, "data-") {
			if v, ok := val.(string); ok {
				m[key] = neutralizeAmbiguousURLScheme(v)
			}
			continue
		}
		// A designated URL key (href, src, ...) genuinely holds a URL, so apply the strict scheme
		// allowlist. A non-string value (e.g. a JSON array) can be coerced back into a dangerous
		// string by a client renderer, so drop it rather than leave it untouched.
		if _, isURL := urlAttrKeys[lower]; isURL {
			v, ok := val.(string)
			if !ok {
				delete(m, key)
				continue
			}
			m[key] = sanitizeURL(v)
		}
	}
}

// sanitizeAttrs strips dangerous keys from an attribute map and descends into any nested maps and
// arrays it holds, so a URL or handler buried under a sub-object (e.g. attrs.config.href, a shape
// an extension's schema may define) is neutralized rather than passed through untouched. It fails
// closed: attribute nesting past maxTipTapDepth returns an error (rejecting the whole document)
// rather than silently leaving the over-deep subtree unsanitized.
func sanitizeAttrs(attrs map[string]any, depth int) error {
	if depth > maxTipTapDepth {
		return errAttrDepthExceeded
	}
	stripDangerousKeys(attrs)
	for _, val := range attrs {
		if err := sanitizeAttrValue(val, depth); err != nil {
			return err
		}
	}
	return nil
}

// sanitizeAttrValue recurses into the containers an attribute value may hold, neutralizing dangerous
// URL schemes on any bare string reached inside an array. A scalar string held directly under a map
// key is left to stripDangerousKeys, which sanitizes it only under a URL-designated key; but a string
// inside an array has no key to mark it, so a dangerous scheme there — e.g. ["javascript:alert(1)"]
// under a non-URL-designated key an editor extension may define — would otherwise pass through
// untouched. Like sanitizeAttrs, it fails closed past maxTipTapDepth.
func sanitizeAttrValue(val any, depth int) error {
	if depth > maxTipTapDepth {
		return errAttrDepthExceeded
	}
	switch v := val.(type) {
	case map[string]any:
		return sanitizeAttrs(v, depth+1)
	case []any:
		for i, item := range v {
			if s, ok := item.(string); ok {
				v[i] = neutralizeAmbiguousURLScheme(s)
				continue
			}
			if err := sanitizeAttrValue(item, depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}

// nodeSkipKeys are the top-level keys excluded from the flat-key sanitization pass in
// sanitizeTipTapNode: "attrs" is handled separately, and "content"/"marks" get their own recursion.
var nodeSkipKeys = map[string]struct{}{"attrs": {}, "content": {}, "marks": {}}

// markSkipKeys are the top-level keys excluded from the flat-key sanitization pass for mark objects.
var markSkipKeys = map[string]struct{}{"attrs": {}}

// sanitizeObjAttrsAndFlatKeys sanitizes the "attrs" sub-object of a node or mark, and any flat keys
// not in skipKeys. stripDangerousKeys must be called on obj before this.
func sanitizeObjAttrsAndFlatKeys(obj map[string]any, attrsErrMsg string, skipKeys map[string]struct{}, depth int) error {
	if attrsVal, ok := obj["attrs"]; ok && attrsVal != nil {
		attrs, ok := attrsVal.(map[string]any)
		if !ok {
			return errors.New(attrsErrMsg)
		}
		if err := sanitizeAttrs(attrs, 0); err != nil {
			return err
		}
	}
	for key, val := range obj {
		if _, skip := skipKeys[key]; skip {
			continue
		}
		if err := sanitizeAttrValue(val, depth); err != nil {
			return err
		}
	}
	return nil
}

func sanitizeTipTapNode(node map[string]any, depth int, count *int) error {
	if node == nil {
		return errors.New("content node must not be null")
	}
	if depth > maxTipTapDepth {
		return errors.New("content nesting exceeds the maximum depth")
	}
	*count++
	if *count > maxTipTapNodes {
		return errors.Errorf("content exceeds the maximum of %d nodes", maxTipTapNodes)
	}

	// Strip dangerous/URL keys placed directly on the node object, then sanitize its attrs. The
	// node's own keys need the same treatment as a mark's: "content" and "marks" are walked below,
	// and no supported node key collides with the handler/URL sets.
	stripDangerousKeys(node)

	nodeType, ok := node["type"].(string)
	if !ok || nodeType == "" {
		return errors.New("content node must have a non-empty type field")
	}
	if _, allowed := allowedNodeTypes[nodeType]; !allowed {
		return errors.Errorf("content node type %q is not allowed", nodeType)
	}

	if err := sanitizeObjAttrsAndFlatKeys(node, "content node attrs must be an object", nodeSkipKeys, depth); err != nil {
		return err
	}

	if marksVal, ok := node["marks"]; ok && marksVal != nil {
		marksArray, ok := marksVal.([]any)
		if !ok {
			return errors.New("content node marks must be an array")
		}
		for _, mark := range marksArray {
			markNode, ok := mark.(map[string]any)
			if !ok {
				return errors.New("content mark must be an object")
			}
			// Sanitize both the mark's nested attrs and any dangerous/URL keys placed directly on
			// the mark object (a non-standard shape a lenient renderer may read).
			stripDangerousKeys(markNode)
			markType, ok := markNode["type"].(string)
			if !ok || markType == "" {
				return errors.New("content mark must have a non-empty type field")
			}
			if _, allowed := allowedMarkTypes[markType]; !allowed {
				return errors.Errorf("content mark type %q is not allowed", markType)
			}
			if err := sanitizeObjAttrsAndFlatKeys(markNode, "content mark attrs must be an object", markSkipKeys, depth); err != nil {
				return err
			}
		}
	}

	if contentVal, ok := node["content"]; ok && contentVal != nil {
		contentArray, ok := contentVal.([]any)
		if !ok {
			return errors.New("content node content must be an array")
		}
		for _, child := range contentArray {
			childNode, ok := child.(map[string]any)
			if !ok {
				return errors.New("content child must be an object")
			}
			if err := sanitizeTipTapNode(childNode, depth+1, count); err != nil {
				return err
			}
		}
	}
	return nil
}

// safeImageMIMETypes are the only image MIME types allowed through data: URIs. SVG is excluded
// because it can carry script. safeImageDataPrefixes is derived from this slice so adding a new
// type here keeps both in sync automatically.
var safeImageMIMETypes = []string{
	"image/png",
	"image/jpeg",
	"image/gif",
	"image/webp",
	"image/bmp",
}

var safeImageDataPrefixes = func() []string {
	out := make([]string, len(safeImageMIMETypes))
	for i, mt := range safeImageMIMETypes {
		out[i] = "data:" + mt
	}
	return out
}()

// sniffBase64Chars is how many leading base64 characters are decoded for content sniffing. 24 chars
// decode to 18 bytes, covering the longest signature http.DetectContentType matches on for the types
// above (WebP, whose RIFF container marker runs to byte 14). 24 is a multiple of 4, so a truncated
// (unpadded) prefix stays valid StdEncoding; a short payload that already carries "=" padding within
// those 24 chars decodes too (unlike RawStdEncoding, which rejects padding).
const sniffBase64Chars = 24

// isBase64ImagePayload confirms a data:image/* URI actually carries base64-encoded bytes that sniff
// as one of the allowed raster images, so a script-bearing payload cannot ride in under an allowed
// MIME label (e.g. an SVG declared as image/png).
func isBase64ImagePayload(url string) bool {
	meta, payload, ok := strings.Cut(url, ",")
	if !ok {
		return false
	}
	if !strings.Contains(strings.ToLower(meta), ";base64") {
		return false
	}
	trimmed := strings.TrimSpace(payload)
	if len(trimmed) > sniffBase64Chars {
		trimmed = trimmed[:sniffBase64Chars]
	}
	data, err := base64.StdEncoding.DecodeString(trimmed)
	if err != nil {
		return false
	}
	contentType, _, _ := strings.Cut(http.DetectContentType(data), ";")
	return slices.Contains(safeImageMIMETypes, contentType)
}

// urlScheme returns the scheme of a URL and whether one is present. lower must already be
// lowercased (decodeURLScheme passes its lowered form): the character check below accepts only a-z,
// so an uppercase scheme would be reported as absent. A scheme must sit at the very start and
// precede any '/', '?', or '#', matching how browsers parse schemes; a relative reference (no
// scheme) returns ("", false).
func urlScheme(lower string) (string, bool) {
	for i, r := range lower {
		if r == ':' {
			if i == 0 {
				return "", false
			}
			return lower[:i], true
		}
		if r == '/' || r == '?' || r == '#' {
			return "", false
		}
		valid := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '+' || r == '-' || r == '.'
		if !valid {
			return "", false
		}
	}
	return "", false
}

// urlStripChars removes the ASCII tab/newline/CR that browsers strip from a URL before resolving
// its scheme, so an obfuscated "java\tscript:" cannot slip past the scheme check.
var urlStripChars = strings.NewReplacer("\t", "", "\n", "", "\r", "", "\x00", "")

// decodeURLScheme extracts the scheme a browser would resolve from url, defeating the obfuscation a
// dangerous scheme can hide behind. It decodes HTML entities and strips the tab/newline/CR/null
// browsers ignore, so an entity-encoded ":" or an embedded control char is detected. Two strip
// passes: the first removes literal control chars, the second removes any html.UnescapeString
// re-introduces (e.g. "&Tab;" → "\t"). Percent-encoded characters (%09, %0A, etc.) are intentionally
// NOT stripped: browsers do not strip them from scheme names, so "java%09script:" never parses as
// "javascript:". Returns the lowercased scheme, the lowercased cleaned string (for data: prefix
// matching), and whether a scheme is present (false for a relative reference). The decode is used
// only for detection; callers return the original string when they allow it.
func decodeURLScheme(url string) (scheme, lower string, hasScheme bool) {
	cleaned := urlStripChars.Replace(url)
	cleaned = html.UnescapeString(cleaned)
	cleaned = urlStripChars.Replace(cleaned)
	cleaned = trimBrowserIgnoredChars(cleaned)
	lower = strings.ToLower(cleaned)
	scheme, hasScheme = urlScheme(lower)
	return scheme, lower, hasScheme
}

// isSafeImageDataURL reports whether a data: URL carries a base64 payload sniffing as an allowed
// raster image. lower is the lowercased, obfuscation-decoded form of url from decodeURLScheme.
func isSafeImageDataURL(url, lower string) bool {
	for _, prefix := range safeImageDataPrefixes {
		if strings.HasPrefix(lower, prefix) && isBase64ImagePayload(url) {
			return true
		}
	}
	return false
}

// sanitizeURL returns the URL unchanged if its scheme is on the allowlist (or it is a relative
// reference), and "" otherwise. It defends against control-character, leading-whitespace, and
// HTML-entity obfuscation of dangerous schemes (e.g. "java&Tab;script&colon;alert(1)"). Applied to a
// value under a URL-designated attribute key (href, src, poster, xlink:href). data-* keys are not
// necessarily URLs and take the lenient neutralizeAmbiguousURLScheme path instead.
func sanitizeURL(url string) string {
	scheme, lower, hasScheme := decodeURLScheme(url)
	if !hasScheme {
		return url
	}
	switch scheme {
	case "http", "https", "mailto", "tel":
		return url
	case "data":
		if isSafeImageDataURL(url, lower) {
			return url
		}
		return ""
	default:
		return ""
	}
}

// neutralizeAmbiguousURLScheme blanks a string that carries a script-executing or foreign-content
// URL scheme, for values that are not unambiguously URLs. It is reached two ways: a bare element of
// an array attribute value (no key marks it a URL), and a data-* attribute value (whose key may mark
// a URL but need not — an extension may store a "12:30" timestamp or "16:9" ratio there). Unlike
// sanitizeURL (a strict allowlist applied where an attribute key designates the value a URL), only
// the unambiguously dangerous schemes are neutralized: a plain colon-bearing string is preserved,
// while a javascript:/vbscript:/non-image data: payload is dropped.
func neutralizeAmbiguousURLScheme(s string) string {
	scheme, lower, hasScheme := decodeURLScheme(s)
	if !hasScheme {
		return s
	}
	switch scheme {
	case "javascript", "vbscript":
		return ""
	case "data":
		if isSafeImageDataURL(s, lower) {
			return s
		}
		return ""
	default:
		return s
	}
}
