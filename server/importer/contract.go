// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Package importer contains the pure, side-effect-free logic that consumes an mmetl
// Confluence v2 bundle: secure archive inspection, strict JSONL parsing, TipTap
// canonicalization, SearchText extraction, placeholder link discovery, and canonical
// hashing. It has no HTTP, database, or plugin-API dependencies so it can be unit-tested
// in isolation; the app/store layers orchestrate it.
package importer

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// ContractVersion is the only JSONL contract version this importer accepts.
const ContractVersion = 2

// ManifestVersion is the string value the manifest's version field must carry for v2.
const ManifestVersion = "2"

// Line type discriminators emitted by the producer, one per JSONL line.
const (
	LineTypeVersion                  = "version"
	LineTypeSpace                    = "space"
	LineTypePage                     = "page"
	LineTypePageComment              = "page_comment"
	LineTypeResolveSpacePlaceholders = "resolve_space_placeholders"
)

// Line mirrors the producer's LineImportData. Every JSONL line decodes into this shape; the
// payload matching Type is non-nil and the others are nil. Pointer payload fields let the parser
// verify that a line carries exactly the payload its Type declares. Unknown object fields are
// tolerated (forward-compatible v2 additions) because json.Unmarshal ignores them by default.
type Line struct {
	Type                     string                   `json:"type"`
	Version                  *int                     `json:"version,omitempty"`
	Source                   *SourceData              `json:"source,omitempty"`
	Space                    *SpaceData               `json:"space,omitempty"`
	Page                     *PageData                `json:"page,omitempty"`
	PageComment              *PageCommentData         `json:"page_comment,omitempty"`
	ResolveSpacePlaceholders *ResolvePlaceholdersData `json:"resolve_space_placeholders,omitempty"`
}

// SourceData is the bundle's source namespace, carried once on the version line and mirrored in
// the manifest. OrganizationID is optional metadata; SpaceKey scopes bare source IDs.
type SourceData struct {
	OrganizationID *string `json:"organization_id,omitempty"`
	SpaceKey       *string `json:"space_key,omitempty"`
}

// SpaceData is the single space line. Team is advisory; Props carries import_source_id.
type SpaceData struct {
	Team        *string         `json:"team"`
	Title       *string         `json:"title,omitempty"`
	Description *string         `json:"description,omitempty"`
	Props       *map[string]any `json:"props,omitempty"`
}

// PageData is one page line. Content is a JSON string holding TipTap JSON that must be decoded and
// validated independently of the line. The page's bare external ID is derived only from
// Props["import_source_id"], never from any other field.
type PageData struct {
	Team                 *string           `json:"team"`
	SpaceImportSourceID  *string           `json:"space_import_source_id"`
	User                 *string           `json:"user"`
	Title                *string           `json:"title"`
	Content              *string           `json:"content"`
	ParentImportSourceID *string           `json:"parent_import_source_id,omitempty"`
	CreateAt             *int64            `json:"create_at,omitempty"`
	UpdateAt             *int64            `json:"update_at,omitempty"`
	Props                *map[string]any   `json:"props,omitempty"`
	Attachments          *[]AttachmentData `json:"attachments,omitempty"`
}

// PageCommentData is one comment line. Comments are parsed and counted but never staged as
// individual entities in this release.
type PageCommentData struct {
	PageImportSourceID          *string         `json:"page_import_source_id"`
	ParentCommentImportSourceID *string         `json:"parent_comment_import_source_id,omitempty"`
	User                        *string         `json:"user"`
	Content                     *string         `json:"content"`
	CreateAt                    *int64          `json:"create_at,omitempty"`
	UpdateAt                    *int64          `json:"update_at,omitempty"`
	IsResolved                  *bool           `json:"is_resolved,omitempty"`
	Props                       *map[string]any `json:"props,omitempty"`
}

// AttachmentData is one attachment metadata entry. Its bytes are never opened in this release; only
// its path is validated and it is counted.
type AttachmentData struct {
	Path  *string         `json:"path"`
	Props *map[string]any `json:"props,omitempty"`
}

// ResolvePlaceholdersData is the trailing resolve_space_placeholders line.
type ResolvePlaceholdersData struct {
	Team                *string `json:"team"`
	SpaceImportSourceID *string `json:"space_import_source_id"`
}

// Well-known page prop keys emitted by the producer inside PageData.Props.
const (
	PropImportSourceID            = "import_source_id"
	PropImportSource              = "import_source"
	PropConfluenceSpaceKey        = "confluence_space_key"
	PropConfluenceAuthorAccountID = "confluence_author_account_id"
	PropImportLabels              = "import_labels"
)

// AllowlistedSourceProps names the page source props copied verbatim into the docs_import
// namespace on execution. Arbitrary future producer fields are deliberately not copied.
var AllowlistedSourceProps = []string{PropImportLabels}

// stringOrEmpty dereferences a *string, returning "" for nil.
func stringOrEmpty(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// int64OrZero dereferences a *int64, returning 0 for nil.
func int64OrZero(v *int64) int64 {
	if v == nil {
		return 0
	}
	return *v
}

// propString reads a string-valued prop from an untyped props map, returning "" when absent or of
// a non-string type. Using json.Number-safe handling is unnecessary here: string props decode as
// Go strings regardless of UseNumber.
func propString(props map[string]any, key string) string {
	if props == nil {
		return ""
	}
	if v, ok := props[key].(string); ok {
		return v
	}
	return ""
}

// derefProps returns the props map behind a *map pointer, or nil.
func derefProps(p *map[string]any) map[string]any {
	if p == nil {
		return nil
	}
	return *p
}

// IsStorableText reports whether s is safe to persist: valid UTF-8 with no NUL (U+0000). PostgreSQL
// cannot store a NUL in a TEXT/VARCHAR value and rejects the escaped-NUL code point inside a JSONB
// string, and invalid UTF-8 would be silently replaced or mutated. Both are rejected during
// inspection rather than sanitized away, so the bundle the user uploaded is either imported exactly
// or refused with a clear reason — a silently altered page body is worse than a rejected bundle.
func IsStorableText(s string) bool {
	return utf8.ValidString(s) && !strings.ContainsRune(s, 0)
}

// findUnstorableValue walks a decoded JSON value and returns the first string it finds that is not
// storable (see IsStorableText), plus false. It is used to reject a source-props map before it can
// reach a JSONB column.
func findUnstorableValue(v any) (string, bool) {
	switch t := v.(type) {
	case string:
		if !IsStorableText(t) {
			return t, false
		}
	case []any:
		for _, item := range t {
			if bad, ok := findUnstorableValue(item); !ok {
				return bad, false
			}
		}
	case map[string]any:
		for k, val := range t {
			if !IsStorableText(k) {
				return k, false
			}
			if bad, ok := findUnstorableValue(val); !ok {
				return bad, false
			}
		}
	}
	return "", true
}

// Bounded-identifier limits for values that participate in a B-tree index. They mirror the migration's
// column widths; identifiers are additionally required to match IdentifierPattern.
const (
	// ExternalIDMaxBytes bounds Confluence page IDs and Atlassian account IDs.
	ExternalIDMaxBytes = 512
	// SpaceKeyMaxBytes bounds source organization IDs and Space keys.
	SpaceKeyMaxBytes = 255
)

// IdentifierPattern is the ASCII contract every non-empty external identifier must match. Every
// identifier the authoritative producer emits fits this set, so bounding it keeps index sizing
// deterministic and rejects incompatible future producer identifiers until the contract is
// deliberately revised. '~' is included because Confluence keys personal spaces as
// "~username"/"~accountid"; omitting it would reject every personal-space bundle outright.
var IdentifierPattern = regexp.MustCompile(`^[A-Za-z0-9._:@~-]+$`)

// IsValidIdentifier reports whether id is non-empty, no longer than maxBytes, and matches the
// contract pattern. Callers that allow absence check for "" themselves.
func IsValidIdentifier(id string, maxBytes int) bool {
	if id == "" || len(id) > maxBytes {
		return false
	}
	return IdentifierPattern.MatchString(id)
}
