// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package importer

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
)

// HashFormatVersion versions the hash input shapes so a later deliberate change can migrate
// baselines rather than silently invalidating every mapping. Version 2 split content from structure:
// parent and sibling ordinal left the content hashes and became separate baseline columns, so a
// preserved local move no longer reads as a content conflict.
const HashFormatVersion = 2

// Body format markers recorded in the applied-content hash. Interactive Docs permits opaque page
// bodies, so a stored body that cannot be canonicalized as TipTap is still hashed deterministically —
// tagged as opaque so it compares unequal to the importer's last canonical baseline and is therefore
// treated as a definite local edit rather than a fatal preflight error.
const (
	BodyFormatCanonicalTipTap = "canonical_tiptap"
	BodyFormatOpaqueRaw       = "opaque_raw"
)

// SourceContentHashInput is the canonical shape hashed to detect whether a page's source *content*
// changed between imports. It deliberately excludes the parent external ID, source ordinal, bundle
// team, job ID, target IDs, and attachment/comment counts: parent and order are structural metadata
// compared through their own baselines.
type SourceContentHashInput struct {
	Version         int            `json:"version"`
	Title           string         `json:"title"`
	CanonicalBody   string         `json:"canonical_body"`
	AuthorAccountID string         `json:"author_account_id"`
	AuthorProposal  string         `json:"author_proposal"`
	SourceCreateAt  int64          `json:"source_create_at"`
	SourceUpdateAt  int64          `json:"source_update_at"`
	SourceProps     map[string]any `json:"source_props"`
}

// AppliedContentHashInput is the canonical shape hashed to detect whether the local page's *content*
// was edited after the last import. It excludes ParentId (structural), SearchText (derived from the
// body), numeric SortOrder, all local timestamps, modifier identity, operational props, and unrelated
// top-level props — every one of which changes through normal editing or legitimate recomputation.
type AppliedContentHashInput struct {
	Version    int    `json:"version"`
	Title      string `json:"title"`
	BodyFormat string `json:"body_format"`
	Body       string `json:"body"`
	// DocsImportSourceFields is the canonical subset of importer-owned props mirroring source
	// content. It must exclude last_job_id, resolved/fallback user IDs, and other operational fields.
	DocsImportSourceFields map[string]any `json:"docs_import_source_fields"`
}

// HashSourceContent returns the lowercase-hex SHA-256 of the canonical source-content input.
func HashSourceContent(in SourceContentHashInput) (string, error) {
	in.Version = HashFormatVersion
	return canonicalHashHex(in)
}

// HashAppliedContent returns the lowercase-hex SHA-256 of the canonical applied-content input. The
// caller sets BodyFormat: BodyFormatCanonicalTipTap for a body it canonicalized, or
// BodyFormatOpaqueRaw with the exact stored bytes when canonicalization failed.
func HashAppliedContent(in AppliedContentHashInput) (string, error) {
	in.Version = HashFormatVersion
	return canonicalHashHex(in)
}

// canonicalHashHex marshals v to canonical JSON (Go sorts object keys, including nested map keys,
// so key order in the source props is irrelevant) and returns the SHA-256 as lowercase hex.
func canonicalHashHex(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// hexSHA256Pattern matches exactly 64 lowercase hexadecimal characters.
var hexSHA256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// IsValidSHA256Hex reports whether s is exactly 64 lowercase hexadecimal characters. Enforced at
// the model/application boundary for every non-empty hash so a CHAR-padded or malformed value
// never enters a comparison.
func IsValidSHA256Hex(s string) bool {
	return hexSHA256Pattern.MatchString(s)
}
