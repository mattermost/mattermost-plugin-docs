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
// tree because the TipTap schema is open (editor extensions add node/mark types). Instances must be
// produced by ParseTipTapDocument for the sanitization invariant to hold — a value built any other
// way has not passed sanitizeTipTapDocument and must not be stored or rendered.
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
// parse-level and are always collapsed by the app layer into one generic content app-error, so they
// are not meant to be addressable per-reason by an i18n key.
func ParseTipTapDocument(contentJSON string) (TipTapDocument, error) {
	if contentJSON == "" {
		return TipTapDocument{
			Type:    TipTapDocType,
			Content: []map[string]any{},
		}, nil
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

// maxTipTapNodes caps the total number of content nodes in a TipTap document. A 2 MiB JSON payload
// can contain hundreds of thousands of tiny nodes; unmarshaling them before sanitization causes
// significant allocation and CPU amplification. The plain-text path is capped at maxPlainTextParagraphs
// (10 000 paragraphs → ~10 000 nodes); rich documents with ~5 inline nodes per paragraph stay well
// under 50 000 for any sane document.
const maxTipTapNodes = 50_000

var errAttrDepthExceeded = errors.New("content attribute nesting exceeds the maximum depth")

// countTipTapNodes returns the total number of nodes in the subtree rooted at node, bounding
// recursion at maxTipTapDepth. It stops counting once the running total exceeds the limit so that
// a document with millions of nodes does not incur a full traversal just to fail the check.
func countTipTapNodes(node map[string]any, depth, runningTotal, limit int) int {
	if depth > maxTipTapDepth || runningTotal > limit {
		return runningTotal
	}
	runningTotal++
	if contentVal, ok := node["content"]; ok {
		if children, ok := contentVal.([]any); ok {
			for _, child := range children {
				if childNode, ok := child.(map[string]any); ok {
					runningTotal = countTipTapNodes(childNode, depth+1, runningTotal, limit)
					if runningTotal > limit {
						return runningTotal
					}
				}
			}
		}
	}
	return runningTotal
}

func sanitizeTipTapDocument(doc *TipTapDocument) error {
	// Reject documents with pathologically many nodes before the recursive sanitization walk,
	// which would otherwise allocate without bound on a crafted payload.
	total := 0
	for _, node := range doc.Content {
		if node != nil {
			total = countTipTapNodes(node, 0, total, maxTipTapNodes)
		}
		if total > maxTipTapNodes {
			return errors.Errorf("content exceeds the maximum of %d nodes", maxTipTapNodes)
		}
	}

	for i := range doc.Content {
		if doc.Content[i] == nil {
			return errors.New("content document nodes must be objects")
		}
		if err := sanitizeTipTapNode(doc.Content[i], 0); err != nil {
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

// forbiddenNodeTypes are TipTap node type values rejected outright because they map to HTML
// elements that can execute script or embed foreign content. A full allowlist keyed to the editor
// schema would be the stronger posture; this denylist stops the most dangerous types now.
var forbiddenNodeTypes = map[string]struct{}{
	"script":           {},
	"iframe":           {},
	"embed":            {},
	"object":           {},
	"noscript":         {},
	"template":         {},
	"style":            {},
	"link":             {},
	"svg":              {},
	"math":             {},
	"animate":          {},
	"animatetransform": {},
	"foreignobject":    {},
	"maction":          {},
}

// forbiddenMarkTypes are TipTap mark type values rejected outright. This mirrors forbiddenNodeTypes
// except that "link" is valid as a mark (TipTap's inline hyperlink) — its href is sanitized by
// sanitizeURL rather than being blocked outright.
var forbiddenMarkTypes = map[string]struct{}{
	"script":           {},
	"iframe":           {},
	"embed":            {},
	"object":           {},
	"noscript":         {},
	"template":         {},
	"style":            {},
	"svg":              {},
	"math":             {},
	"animate":          {},
	"animatetransform": {},
	"foreignobject":    {},
	"maction":          {},
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

// stripDangerousKeys strips script-bearing keys (event handlers plus the dangerousAttrKeys set) and
// neutralizes dangerous URL schemes on any URL-valued key, at the top level of m only. Key names are
// matched case-insensitively, since HTML attribute names are case-insensitive.
//
// It is applied to attribute maps and also to the node and mark objects themselves, because a
// lenient client renderer may read a dangerous or URL-valued key placed directly on the object
// rather than nested under its "attrs".
func stripDangerousKeys(m map[string]any) {
	for key, val := range m {
		lower := strings.ToLower(key)
		if _, dangerous := dangerousAttrKeys[lower]; strings.HasPrefix(lower, "on") || dangerous {
			delete(m, key)
			continue
		}
		// data-* attributes may carry URL values (data-href, data-src, data-url, etc.) that a
		// lenient client renderer can treat as navigation targets; sanitize them as URLs.
		isURL := strings.HasPrefix(lower, "data-")
		if !isURL {
			_, isURL = urlAttrKeys[lower]
		}
		if isURL {
			// A URL-valued attribute must be a string. A non-string value (e.g. a JSON array) can
			// be coerced back into a dangerous string by a client renderer, so drop it rather than
			// leave it untouched.
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

// sanitizeAttrValue recurses into the containers an attribute value may hold. Scalars are left
// alone: only a map can carry a dangerous key. Like sanitizeAttrs, it fails closed past
// maxTipTapDepth.
func sanitizeAttrValue(val any, depth int) error {
	if depth > maxTipTapDepth {
		return errAttrDepthExceeded
	}
	switch v := val.(type) {
	case map[string]any:
		return sanitizeAttrs(v, depth+1)
	case []any:
		for _, item := range v {
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

func sanitizeTipTapNode(node map[string]any, depth int) error {
	if node == nil {
		return errors.New("content node must not be null")
	}
	if depth > maxTipTapDepth {
		return errors.New("content nesting exceeds the maximum depth")
	}

	// Strip dangerous/URL keys placed directly on the node object, then sanitize its attrs. The
	// node's own keys need the same treatment as a mark's: "content" and "marks" are walked below,
	// and no supported node key collides with the handler/URL sets.
	stripDangerousKeys(node)

	nodeType, ok := node["type"].(string)
	if !ok || nodeType == "" {
		return errors.New("content node must have a non-empty type field")
	}
	if _, forbidden := forbiddenNodeTypes[strings.ToLower(nodeType)]; forbidden {
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
			if _, forbidden := forbiddenMarkTypes[strings.ToLower(markType)]; forbidden {
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
			if err := sanitizeTipTapNode(childNode, depth+1); err != nil {
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

// urlScheme returns the lowercased scheme of a URL and whether one is present. A scheme must sit at
// the very start and precede any '/', '?', or '#', matching how browsers parse schemes; a relative
// reference (no scheme) returns ("", false).
func urlScheme(s string) (string, bool) {
	for i, r := range s {
		if r == ':' {
			if i == 0 {
				return "", false
			}
			return s[:i], true
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

// sanitizeURL returns the URL unchanged if its scheme is on the allowlist (or it is a relative
// reference), and "" otherwise. It defends against control-character, leading-whitespace, and
// HTML-entity obfuscation of dangerous schemes (e.g. "java&Tab;script&colon;alert(1)").
func sanitizeURL(url string) string {
	// Decode HTML entities and strip the tab/newline/CR browsers ignore, so an obfuscated scheme
	// (entity-encoded ":" or an embedded control char) is detected. The decode is used only for
	// scheme detection; the original url is what gets returned when allowed.
	// Two strip passes: the first removes literal control chars, the second removes any that
	// html.UnescapeString re-introduces (e.g. "&Tab;" → "\t").
	// Percent-encoded characters (%09, %0A, etc.) are intentionally NOT stripped: browsers do not
	// strip percent-encoded chars from scheme names, so "java%09script:" never parses as "javascript:".
	cleaned := urlStripChars.Replace(url)
	cleaned = html.UnescapeString(cleaned)
	cleaned = urlStripChars.Replace(cleaned)
	cleaned = strings.TrimFunc(cleaned, func(r rune) bool { return r <= ' ' })
	lower := strings.ToLower(cleaned)

	scheme, hasScheme := urlScheme(lower)
	if !hasScheme {
		return url
	}
	switch scheme {
	case "http", "https", "mailto", "tel":
		return url
	case "data":
		for _, prefix := range safeImageDataPrefixes {
			if strings.HasPrefix(lower, prefix) && isBase64ImagePayload(url) {
				return url
			}
		}
		return ""
	default:
		return ""
	}
}
