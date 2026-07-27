// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package importer

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"path"
	"strings"
)

// fsModeSymlink is the file-mode bit marking a symlink entry.
const fsModeSymlink = fs.ModeSymlink

// First-release archive/content limits. Kept as named constants (not magic numbers in handlers)
// so the upload handler and the archive inspector share one source of truth.
const (
	// MaxArchiveEntries bounds the number of entries in the ZIP central directory.
	MaxArchiveEntries = 25_000
	// MaxManifestBytes bounds the decompressed import-manifest.json.
	MaxManifestBytes = 2 * 1024 * 1024
	// MaxJSONLBytes bounds the decompressed import.jsonl.
	MaxJSONLBytes = 128 * 1024 * 1024
	// MaxJSONLLineBytes bounds a single JSONL line.
	MaxJSONLLineBytes = 8 * 1024 * 1024
	// MaxPages bounds the number of page lines.
	MaxPages = 5_000
	// MaxTipTapNodes bounds the number of nodes in a single page's TipTap document.
	MaxTipTapNodes = 250_000
	// MaxTipTapDepth bounds TipTap nesting depth.
	MaxTipTapDepth = 100
)

// Fixed entry names required at the archive root.
const (
	entryJSONL    = "import.jsonl"
	entryManifest = "import-manifest.json"
	entryDataDir  = "data/"
)

// ArchiveContents holds the decompressed bytes of the two required root entries plus the
// SHA-256 the caller computed over the archive as a whole. Attachment bytes under data/ are never
// read, so they are absent here.
type ArchiveContents struct {
	ManifestBytes []byte
	JSONLBytes    []byte
	// JSONLSha256 is the lowercase hex SHA-256 of the exact decompressed import.jsonl bytes.
	JSONLSha256 string
	// HasDataDir reports whether any entry under data/ existed (metadata only; never extracted).
	HasDataDir bool
}

// ArchiveError describes a rejected archive with a stable code so callers can map it to a
// user-facing message and HTTP status.
type ArchiveError struct {
	Code    string
	Message string
}

func (e *ArchiveError) Error() string { return e.Message }

func archiveErr(code, format string, args ...any) *ArchiveError {
	return &ArchiveError{Code: code, Message: fmt.Sprintf(format, args...)}
}

// Stable archive rejection codes.
const (
	ArchiveErrUnreadable        = "archive_unreadable"
	ArchiveErrTooManyEntries    = "archive_too_many_entries"
	ArchiveErrBadEntryName      = "archive_bad_entry_name"
	ArchiveErrDuplicateEntry    = "archive_duplicate_entry"
	ArchiveErrUnsafeEntry       = "archive_unsafe_entry"
	ArchiveErrEncryptedEntry    = "archive_encrypted_entry"
	ArchiveErrUnsupportedMethod = "archive_unsupported_method"
	ArchiveErrMissingJSONL      = "archive_missing_jsonl"
	ArchiveErrMissingManifest   = "archive_missing_manifest"
	ArchiveErrUnexpectedEntry   = "archive_unexpected_entry"
	ArchiveErrManifestTooLarge  = "archive_manifest_too_large"
	ArchiveErrJSONLTooLarge     = "archive_jsonl_too_large"
)

// InspectArchive validates the structure and safety of a ZIP bundle read from r (of size n) and
// returns the decompressed manifest and JSONL bytes. It never extracts or opens data/ files.
//
// Structural rules (before opening any body): reject duplicate raw or normalized names,
// backslash separators, empty names, NUL bytes, absolute paths, Windows drive prefixes, "."/".."
// segments, symlinks/non-regular entries, encrypted entries, and unsupported compression methods.
// Require exactly one root import.jsonl and one root import-manifest.json; permit other files only
// below data/.
func InspectArchive(r io.ReaderAt, n int64) (*ArchiveContents, error) {
	zr, err := zip.NewReader(r, n)
	if err != nil {
		return nil, archiveErr(ArchiveErrUnreadable, "failed to read zip archive: %v", err)
	}
	if len(zr.File) > MaxArchiveEntries {
		return nil, archiveErr(ArchiveErrTooManyEntries, "archive has %d entries, limit is %d", len(zr.File), MaxArchiveEntries)
	}

	rawSeen := make(map[string]struct{}, len(zr.File))
	normSeen := make(map[string]struct{}, len(zr.File))

	var jsonlFile, manifestFile *zip.File
	hasDataDir := false

	for _, f := range zr.File {
		raw := f.Name
		if raw == "" {
			return nil, archiveErr(ArchiveErrBadEntryName, "archive contains an empty entry name")
		}
		if strings.ContainsRune(raw, 0) {
			return nil, archiveErr(ArchiveErrBadEntryName, "archive entry name contains a NUL byte")
		}
		if strings.Contains(raw, "\\") {
			// Reject rather than silently converting: a backslash in a ZIP name is either a
			// non-conformant producer or an evasion attempt.
			return nil, archiveErr(ArchiveErrBadEntryName, "archive entry %q contains a backslash", raw)
		}
		if _, dup := rawSeen[raw]; dup {
			return nil, archiveErr(ArchiveErrDuplicateEntry, "archive contains duplicate entry %q", raw)
		}
		rawSeen[raw] = struct{}{}

		// Reject traversal/absolute/drive names on the raw form first, so path.Clean below cannot
		// mask a ".." segment by collapsing it before it is detected.
		if unsafeErr := checkUnsafeName(raw); unsafeErr != nil {
			return nil, unsafeErr
		}

		// Genuinely normalize before duplicate detection: collapse "." and repeated "/" segments so
		// e.g. "data//x" and "data/x" are recognized as the same entry. Only "/" separators are
		// present (backslashes are rejected above). The trailing slash is preserved so a directory
		// entry stays distinct from a same-named file.
		name := normalizeEntryName(raw)
		if _, dup := normSeen[name]; dup {
			return nil, archiveErr(ArchiveErrDuplicateEntry, "archive contains duplicate normalized entry %q", name)
		}
		normSeen[name] = struct{}{}

		// Validate mode, encryption, and compression method for every file entry — including the
		// data/ payloads we never open — so an unsafe entry is rejected uniformly rather than only
		// for the two entries whose bytes are read.
		isDir := strings.HasSuffix(name, "/")
		if !isDir {
			if modeErr := checkEntryMode(f); modeErr != nil {
				return nil, modeErr
			}
			if methodErr := checkSupportedMethod(f); methodErr != nil {
				return nil, methodErr
			}
		}

		switch {
		case name == entryJSONL:
			jsonlFile = f
		case name == entryManifest:
			manifestFile = f
		case name == entryDataDir || strings.HasPrefix(name, entryDataDir):
			// data/ files are permitted but never opened.
			hasDataDir = true
		default:
			return nil, archiveErr(ArchiveErrUnexpectedEntry, "unexpected archive entry %q; only import.jsonl, import-manifest.json, and data/ are allowed", name)
		}
	}

	if jsonlFile == nil {
		return nil, archiveErr(ArchiveErrMissingJSONL, "archive is missing required import.jsonl")
	}
	if manifestFile == nil {
		return nil, archiveErr(ArchiveErrMissingManifest, "archive is missing required import-manifest.json")
	}

	manifestBytes, err := readLimited(manifestFile, MaxManifestBytes)
	if err != nil {
		if err == errTooLarge {
			return nil, archiveErr(ArchiveErrManifestTooLarge, "import-manifest.json exceeds %d bytes", MaxManifestBytes)
		}
		return nil, archiveErr(ArchiveErrUnreadable, "failed to read import-manifest.json: %v", err)
	}

	jsonlBytes, err := readLimited(jsonlFile, MaxJSONLBytes)
	if err != nil {
		if err == errTooLarge {
			return nil, archiveErr(ArchiveErrJSONLTooLarge, "import.jsonl exceeds %d bytes", MaxJSONLBytes)
		}
		return nil, archiveErr(ArchiveErrUnreadable, "failed to read import.jsonl: %v", err)
	}

	sum := sha256.Sum256(jsonlBytes)

	return &ArchiveContents{
		ManifestBytes: manifestBytes,
		JSONLBytes:    jsonlBytes,
		JSONLSha256:   hex.EncodeToString(sum[:]),
		HasDataDir:    hasDataDir,
	}, nil
}

// normalizeEntryName canonicalizes a "/"-separated ZIP entry name for duplicate detection by
// collapsing "." and repeated-slash segments via path.Clean, preserving a trailing slash so a
// directory entry does not alias a same-named file. It must be called only after checkUnsafeName
// has rejected ".." segments, since path.Clean would otherwise resolve them away.
func normalizeEntryName(name string) string {
	isDir := strings.HasSuffix(name, "/")
	cleaned := path.Clean(name)
	if isDir && cleaned != "/" && cleaned != "." {
		cleaned += "/"
	}
	return cleaned
}

// checkUnsafeName rejects path traversal and platform-specific unsafe names on a "/"-separated
// entry name.
func checkUnsafeName(name string) error {
	if strings.HasPrefix(name, "/") {
		return archiveErr(ArchiveErrUnsafeEntry, "archive entry %q is an absolute path", name)
	}
	// Windows drive prefix such as "C:".
	if len(name) >= 2 && name[1] == ':' {
		return archiveErr(ArchiveErrUnsafeEntry, "archive entry %q has a drive prefix", name)
	}
	for seg := range strings.SplitSeq(name, "/") {
		if seg == "." || seg == ".." {
			return archiveErr(ArchiveErrUnsafeEntry, "archive entry %q contains a %q segment", name, seg)
		}
	}
	return nil
}

// checkEntryMode rejects symlinks and other non-regular file entries. Directory entries are
// handled by the caller (trailing "/").
func checkEntryMode(f *zip.File) error {
	mode := f.Mode()
	if mode&fsModeSymlink != 0 {
		return archiveErr(ArchiveErrUnsafeEntry, "archive entry %q is a symlink", f.Name)
	}
	if !mode.IsRegular() {
		return archiveErr(ArchiveErrUnsafeEntry, "archive entry %q is not a regular file", f.Name)
	}
	return nil
}

// checkSupportedMethod rejects encrypted entries and unsupported compression methods for entries
// whose bodies will be read (manifest/JSONL).
func checkSupportedMethod(f *zip.File) error {
	// Bit 0 of the general-purpose flag marks an encrypted entry.
	if f.Flags&0x1 != 0 {
		return archiveErr(ArchiveErrEncryptedEntry, "archive entry %q is encrypted", f.Name)
	}
	if f.Method != zip.Store && f.Method != zip.Deflate {
		return archiveErr(ArchiveErrUnsupportedMethod, "archive entry %q uses unsupported compression method %d", f.Name, f.Method)
	}
	return nil
}

// errTooLarge is a sentinel used internally by readLimited so callers can distinguish an
// over-limit body from a genuine read failure.
var errTooLarge = fmt.Errorf("entry exceeds decompressed size limit")

// readLimited decompresses f, enforcing the decompressed byte limit while reading (not trusting
// declared ZIP metadata) rather than the compressed metadata. It reads one byte past the limit to
// detect an exact overflow: an entry within the limit is read to EOF, which makes archive/zip
// verify the entry CRC; an over-limit entry is rejected before it can be fully decompressed, so
// there is no unbounded decompression.
func readLimited(f *zip.File, limit int64) (_ []byte, err error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer func() {
		if cerr := rc.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()

	buf, err := io.ReadAll(io.LimitReader(rc, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(buf)) > limit {
		return nil, errTooLarge
	}
	return buf, nil
}
