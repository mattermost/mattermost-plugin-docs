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
// baselines rather than silently invalidating every mapping.
const HashFormatVersion = 1

// SourceStateHashInput is the canonical shape hashed to detect whether a page's source content
// changed between imports. It deliberately excludes bundle team, job ID, target IDs,
// attachment/comment counts, and source ordinal — none of which are page content.
type SourceStateHashInput struct {
	Version          int            `json:"version"`
	Title            string         `json:"title"`
	CanonicalBody    string         `json:"canonical_body"`
	ParentExternalID string         `json:"parent_external_id"`
	AuthorAccountID  string         `json:"author_account_id"`
	AuthorProposal   string         `json:"author_proposal"`
	SourceCreateAt   int64          `json:"source_create_at"`
	SourceUpdateAt   int64          `json:"source_update_at"`
	SourceProps      map[string]any `json:"source_props"`
}

// AppliedStateHashInput is the canonical shape hashed to detect whether the local page was edited
// after the last import. It excludes SearchText (derived from Body), numeric SortOrder, timestamps,
// and modifier identity — all of which change through normal editing unrelated to content.
type AppliedStateHashInput struct {
	Version                int            `json:"version"`
	Title                  string         `json:"title"`
	CanonicalBody          string         `json:"canonical_body"`
	ParentID               string         `json:"parent_id"`
	DocsImportSourceFields map[string]any `json:"docs_import_source_fields"`
}

// HashSourceState returns the lowercase-hex SHA-256 of the canonical source-state input.
func HashSourceState(in SourceStateHashInput) (string, error) {
	in.Version = HashFormatVersion
	return canonicalHashHex(in)
}

// HashAppliedState returns the lowercase-hex SHA-256 of the canonical applied-state input.
func HashAppliedState(in AppliedStateHashInput) (string, error) {
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
