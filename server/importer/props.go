// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package importer

import (
	"maps"
	"slices"

	"github.com/mattermost/mattermost-plugin-docs/server/model"
)

// DocsImportPropsKey is the single top-level page prop the importer owns. Everything the importer
// records about a page's origin lives under it, so a reimport can replace its own bookkeeping wholesale
// without touching props that belong to interactive editing or to other features.
const DocsImportPropsKey = "docs_import"

// Keys inside the docs_import namespace. They are named constants because both the props builder and
// the applied-content hash's canonical subset must agree on them exactly: a typo in either would make
// every reimport look like a local edit.
const (
	DocsImportKeySourceType            = "source_type"
	DocsImportKeyImportSourceID        = "import_source_id"
	DocsImportKeyOrganizationID        = "organization_id"
	DocsImportKeySpaceKey              = "space_key"
	DocsImportKeyExternalPageID        = "external_page_id"
	DocsImportKeySourceAuthorAccountID = "source_author_account_id"
	DocsImportKeySourceAuthorUsername  = "source_author_username"
	DocsImportKeyResolvedAuthorUserID  = "resolved_author_user_id"
	DocsImportKeyAuthorFallback        = "author_fallback"
	DocsImportKeyAuthorFallbackReason  = "author_fallback_reason"
	DocsImportKeySourceCreateAt        = "source_create_at"
	DocsImportKeySourceUpdateAt        = "source_update_at"
	DocsImportKeyLastJobID             = "last_job_id"
	DocsImportKeySourceProps           = "source_props"
)

// docsImportSourceFieldKeys is the canonical subset of the docs_import namespace that mirrors *source*
// content and therefore participates in the applied-content hash.
//
// The exclusions are the point of this list. last_job_id changes on every import, and the resolved
// author id and fallback reason legitimately change when a previously unmatched Confluence account is
// later created as a Mattermost user — recomputing those must not read as a local edit, or a harmless
// re-resolution would turn every page into a conflict.
var docsImportSourceFieldKeys = []string{
	DocsImportKeySourceType,
	DocsImportKeyImportSourceID,
	DocsImportKeyOrganizationID,
	DocsImportKeySpaceKey,
	DocsImportKeyExternalPageID,
	DocsImportKeySourceAuthorAccountID,
	DocsImportKeySourceAuthorUsername,
	DocsImportKeySourceCreateAt,
	DocsImportKeySourceUpdateAt,
	DocsImportKeySourceProps,
}

// DocsImportInput is everything needed to build a page's docs_import namespace.
type DocsImportInput struct {
	ImportSourceID   string
	OrganizationID   string
	SpaceKey         string
	ExternalPageID   string
	SourceAccountID  string
	SourceUsername   string
	ResolvedUserID   string
	FallbackReason   string
	SourceCreateAt   int64
	SourceUpdateAt   int64
	LastJobID        string
	SourceProps      map[string]any
	AllowlistedProps []string
}

// BuildDocsImportProps returns the docs_import namespace for one page.
//
// Only allowlisted source props are copied. Blindly copying whatever the producer emitted would put
// unreviewed future fields into a hashed, user-visible prop, so a new producer field is a deliberate
// addition to AllowlistedSourceProps rather than something that arrives by itself.
func BuildDocsImportProps(in DocsImportInput) map[string]any {
	allow := in.AllowlistedProps
	if allow == nil {
		allow = AllowlistedSourceProps
	}
	sourceProps := map[string]any{}
	for _, key := range allow {
		if v, ok := in.SourceProps[key]; ok {
			sourceProps[key] = v
		}
	}

	out := map[string]any{
		DocsImportKeySourceType:            model.ImportSourceTypeConfluence,
		DocsImportKeyImportSourceID:        in.ImportSourceID,
		DocsImportKeySpaceKey:              in.SpaceKey,
		DocsImportKeyExternalPageID:        in.ExternalPageID,
		DocsImportKeySourceAuthorAccountID: in.SourceAccountID,
		DocsImportKeySourceAuthorUsername:  in.SourceUsername,
		DocsImportKeyResolvedAuthorUserID:  in.ResolvedUserID,
		DocsImportKeyAuthorFallback:        in.FallbackReason != "",
		DocsImportKeySourceCreateAt:        in.SourceCreateAt,
		DocsImportKeySourceUpdateAt:        in.SourceUpdateAt,
		DocsImportKeyLastJobID:             in.LastJobID,
		DocsImportKeySourceProps:           sourceProps,
	}
	// Optional fields are omitted rather than written empty, so the canonical hash subset does not
	// change shape depending on whether an organization id happened to be present.
	if in.OrganizationID != "" {
		out[DocsImportKeyOrganizationID] = in.OrganizationID
	}
	if in.FallbackReason != "" {
		out[DocsImportKeyAuthorFallbackReason] = in.FallbackReason
	}
	return out
}

// DocsImportSourceFields extracts the canonical source-mirroring subset of a docs_import namespace, for
// the applied-content hash. A page with no docs_import namespace yields an empty map, which is what makes
// a page the importer has never touched hash differently from one it has.
func DocsImportSourceFields(docsImport map[string]any) map[string]any {
	out := make(map[string]any, len(docsImportSourceFieldKeys))
	for _, key := range docsImportSourceFieldKeys {
		if v, ok := docsImport[key]; ok {
			out[key] = v
		}
	}
	return out
}

// DocsImportNamespace reads the docs_import namespace out of a page's top-level props, returning nil when
// absent or not an object.
func DocsImportNamespace(props map[string]any) map[string]any {
	if props == nil {
		return nil
	}
	ns, ok := props[DocsImportPropsKey].(map[string]any)
	if !ok {
		return nil
	}
	return ns
}

// MergeDocsImportProps returns existing top-level props with the docs_import namespace replaced.
//
// Unrelated top-level props are preserved: a reimport owns its own namespace and nothing else, so
// whatever interactive editing or another feature stored on the page survives. The input map is not
// mutated, so a caller can compare before and after.
func MergeDocsImportProps(existing map[string]any, docsImport map[string]any) map[string]any {
	merged := make(map[string]any, len(existing)+1)
	maps.Copy(merged, existing)
	merged[DocsImportPropsKey] = docsImport
	return merged
}

// IsAllowlistedSourceProp reports whether a producer page prop is copied into docs_import.source_props.
func IsAllowlistedSourceProp(key string) bool {
	return slices.Contains(AllowlistedSourceProps, key)
}
