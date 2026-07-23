// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package importer

import "strings"

// Confluence link placeholder prefixes the producer emits inside TipTap link mark hrefs and image
// src attributes. V1 discovers these structurally but never rewrites them: there is no canonical
// Docs reader URL in this repository yet.
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
// Raw is the exact attribute value; Target is the portion after the placeholder prefix and colon
// (e.g. the page ID or title), when present.
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

// classifyPlaceholder inspects an attribute value and returns a DiscoveredLink when it begins with
// a recognized placeholder prefix, plus ok=true. A value that merely contains a placeholder token
// somewhere other than the start is not treated as a placeholder here (ordinary text is reported
// separately by the caller).
func classifyPlaceholder(value string, inImageSrc bool) (DiscoveredLink, bool) {
	for prefix, kind := range placeholderKinds {
		if value == prefix || strings.HasPrefix(value, prefix+":") {
			target := ""
			if len(value) > len(prefix)+1 {
				target = value[len(prefix)+1:]
			}
			return DiscoveredLink{Kind: kind, Raw: value, Target: target, InImageSrc: inImageSrc}, true
		}
	}
	return DiscoveredLink{}, false
}

var placeholderKinds = map[string]LinkKind{
	PlaceholderPageID:     LinkKindPageID,
	PlaceholderPageTitle:  LinkKindPageTitle,
	PlaceholderFile:       LinkKindFile,
	PlaceholderAttachment: LinkKindAttachment,
}

// containsPlaceholderToken reports whether s contains any placeholder prefix token anywhere. Used
// to flag placeholders that appear in ordinary text (which V1 never rewrites) so the report can
// note them.
func containsPlaceholderToken(s string) bool {
	for prefix := range placeholderKinds {
		if strings.Contains(s, prefix) {
			return true
		}
	}
	return false
}
