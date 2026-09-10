// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package importer

import (
	"regexp"
	"strings"
)

// Confluence link placeholder names the mmetl producer emits inside TipTap link mark hrefs and
// image src attributes. The producer wraps them in double braces, e.g. "{{CONF_PAGE_ID:101}}"
// (see mmetl services/confluence/links.go). V1 discovers these structurally but never rewrites
// them: there is no canonical Docs reader URL in this repository yet.
const (
	PlaceholderPageID     = "CONF_PAGE_ID"
	PlaceholderPageTitle  = "CONF_PAGE_TITLE"
	PlaceholderFile       = "CONF_FILE"
	PlaceholderAttachment = "CONF_ATTACHMENT"
)

// LinkKind classifies a discovered placeholder.
type LinkKind string

const (
	LinkKindPageID     LinkKind = "page_id"
	LinkKindPageTitle  LinkKind = "page_title"
	LinkKindFile       LinkKind = "file"
	LinkKindAttachment LinkKind = "attachment"
)

// DiscoveredLink is one placeholder found in an approved attribute (link mark href or image src).
// Raw is the exact attribute value; Target is the placeholder's argument (e.g. the page ID or
// title) with the braces and prefix removed.
type DiscoveredLink struct {
	Kind   LinkKind `json:"kind"`
	Raw    string   `json:"raw"`
	Target string   `json:"target"`
	// InImageSrc is true when the placeholder was found in an image node's src rather than a link
	// mark's href.
	InImageSrc bool `json:"in_image_src"`
	// InText is true when a placeholder token appeared in ordinary text rather than an approved
	// attribute. V1 never rewrites these; the caller reports them as placeholder_in_text_not_rewritten.
	InText bool `json:"in_text"`
}

// placeholderKinds maps a recognized link/attachment placeholder name to its kind.
var placeholderKinds = map[string]LinkKind{
	PlaceholderPageID:     LinkKindPageID,
	PlaceholderPageTitle:  LinkKindPageTitle,
	PlaceholderFile:       LinkKindFile,
	PlaceholderAttachment: LinkKindAttachment,
}

// placeholderTarget matches a placeholder's target argument: any run of escaped braces ("\{" or
// "\}") or characters that are neither brace. The producer escapes literal braces in the target
// (escapeForPlaceholder: "{"->"\{", "}"->"\}"), so a naive "[^}]*" would stop at the first escaped
// "}" and fail to match a title/filename containing braces. RE2 has no lookbehind, so the "stop at
// the first unescaped }}" rule is expressed through this alternation instead.
const placeholderTarget = `((?:\\[{}]|[^{}])*)`

// linkPlaceholderRe matches a producer link/attachment placeholder of the form
// "{{CONF_PAGE_ID:target}}", capturing the placeholder name and its (still-escaped) target.
var linkPlaceholderRe = regexp.MustCompile(`\{\{(CONF_PAGE_ID|CONF_PAGE_TITLE|CONF_FILE|CONF_ATTACHMENT):` + placeholderTarget + `\}\}`)

// anyPlaceholderRe matches any Confluence placeholder token (including e.g. CONF_USER) so a
// placeholder left in ordinary text can be flagged even when it is not one of the link kinds.
var anyPlaceholderRe = regexp.MustCompile(`\{\{CONF_[A-Z_]+:` + placeholderTarget + `\}\}`)

// placeholderUnescaper reverses escapeForPlaceholder, turning the producer's "\{"/"\}" back into
// literal braces in a discovered target.
var placeholderUnescaper = strings.NewReplacer(`\{`, `{`, `\}`, `}`)

// classifyPlaceholder inspects an approved attribute value (a link href or image src) and returns a
// DiscoveredLink when it contains a recognized "{{CONF_...:target}}" placeholder, plus ok=true. The
// producer sets the whole attribute to the placeholder, but this tolerates surrounding text by
// matching the first placeholder anywhere in the value. The captured target is unescaped back to
// its literal form.
func classifyPlaceholder(value string, inImageSrc bool) (DiscoveredLink, bool) {
	m := linkPlaceholderRe.FindStringSubmatch(value)
	if m == nil {
		return DiscoveredLink{}, false
	}
	return DiscoveredLink{
		Kind:       placeholderKinds[m[1]],
		Raw:        value,
		Target:     placeholderUnescaper.Replace(m[2]),
		InImageSrc: inImageSrc,
	}, true
}

// containsPlaceholderToken reports whether s contains any Confluence "{{CONF_...:...}}" placeholder
// token. Used to flag placeholders that appear in ordinary text (which V1 never rewrites).
func containsPlaceholderToken(s string) bool {
	return anyPlaceholderRe.MatchString(s)
}
