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
	// MaxBundleUploadBytes bounds the compressed archive a client may upload. It is enforced twice:
	// while streaming the upload to disk, and again from the resulting file size before the ZIP
	// central directory is parsed.
	MaxBundleUploadBytes = 250 * 1024 * 1024
	// MaxMultipartOverheadBytes is the slack added to MaxBundleUploadBytes when capping the whole
	// multipart request body, covering part headers, boundaries, and the request JSON part.
	MaxMultipartOverheadBytes = 1024 * 1024
	// MaxRequestPartBytes bounds the JSON `request` part of the multipart upload.
	MaxRequestPartBytes = 64 * 1024
	// MaxArchiveEntries bounds the number of entries in the ZIP central directory.
	MaxArchiveEntries = 25_000
	// MaxManifestBytes bounds the decompressed import-manifest.json.
	MaxManifestBytes = 2 * 1024 * 1024
	// MaxJSONLBytes bounds the decompressed import.jsonl. It is enforced while streaming, so an
	// over-limit entry is rejected without ever being fully decompressed.
	MaxJSONLBytes = 128 * 1024 * 1024
	// MaxJSONLLineBytes bounds a single JSONL line.
	MaxJSONLLineBytes = 8 * 1024 * 1024
	// MaxPages bounds the number of page lines. It also fixes the result-ordinal ranges: page
	// ordinals occupy 0..MaxPages-1 and stale results start at MaxPages.
	MaxPages = 5_000
	// MaxTipTapNodes bounds the number of nodes in a single page's TipTap document.
	MaxTipTapNodes = 250_000
	// MaxTipTapDepth bounds TipTap nesting depth.
	MaxTipTapDepth = 100
	// MaxManifestUsers bounds the manifest user list, which is persisted per job.
	MaxManifestUsers = 50_000
)

// Fixed entry names required at the archive root.
const (
	entryJSONL    = "import.jsonl"
	entryManifest = "import-manifest.json"
	entryDataDir  = "data/"
)

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
	ArchiveErrTooLarge          = "archive_too_large"
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

// Archive is a structurally validated bundle. It holds only entry handles, never decompressed
// bodies, so the JSONL can be streamed more than once (first to verify its checksum, then to parse
// it inside the staging transaction) without ever materializing it in memory.
type Archive struct {
	jsonl      *zip.File
	manifest   *zip.File
	hasDataDir bool
}

// HasDataDir reports whether any entry existed under data/. Those entries are validated but never
// opened: attachment bytes are out of scope in this release.
func (a *Archive) HasDataDir() bool { return a.hasDataDir }

// OpenArchive validates the structure and safety of a ZIP bundle read from r (of size n) and returns
// a handle for streaming its two root entries. It never extracts or opens data/ files.
//
// Structural rules (before opening any body): reject an over-limit compressed size, duplicate raw or
// normalized names, backslash separators, empty names, NUL bytes, absolute paths, Windows drive
// prefixes, "."/".." segments, symlinks and other non-regular entries, encrypted entries, and
// unsupported compression methods. Require exactly one root import.jsonl and one root
// import-manifest.json; permit other files only below data/.
func OpenArchive(r io.ReaderAt, n int64) (*Archive, error) {
	// Reject an oversized archive from its size alone, before the ZIP central directory is parsed.
	if n > MaxBundleUploadBytes {
		return nil, archiveErr(ArchiveErrTooLarge, "archive is %d bytes, limit is %d", n, MaxBundleUploadBytes)
	}
	zr, err := zip.NewReader(r, n)
	if err != nil {
		return nil, archiveErr(ArchiveErrUnreadable, "failed to read zip archive: %v", err)
	}
	if len(zr.File) > MaxArchiveEntries {
		return nil, archiveErr(ArchiveErrTooManyEntries, "archive has %d entries, limit is %d", len(zr.File), MaxArchiveEntries)
	}

	rawSeen := make(map[string]struct{}, len(zr.File))
	normSeen := make(map[string]struct{}, len(zr.File))

	archive := &Archive{}

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
			// non-conformant producer or an evasion attempt. The authoritative producer emits "/" on
			// every OS, so this is a producer bug to fix rather than a case to tolerate.
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
		// e.g. "data//x" and "data/x" are recognized as the same entry. The trailing slash is
		// preserved so a directory entry stays distinct from a same-named file.
		name := normalizeEntryName(raw)
		if _, dup := normSeen[name]; dup {
			return nil, archiveErr(ArchiveErrDuplicateEntry, "archive contains duplicate normalized entry %q", name)
		}
		normSeen[name] = struct{}{}

		if modeErr := checkEntrySafety(f, name); modeErr != nil {
			return nil, modeErr
		}

		switch {
		case name == entryJSONL:
			archive.jsonl = f
		case name == entryManifest:
			archive.manifest = f
		case name == entryDataDir || strings.HasPrefix(name, entryDataDir):
			// data/ files are permitted but never opened.
			archive.hasDataDir = true
		default:
			return nil, archiveErr(ArchiveErrUnexpectedEntry, "unexpected archive entry %q; only import.jsonl, import-manifest.json, and data/ are allowed", name)
		}
	}

	if archive.jsonl == nil {
		return nil, archiveErr(ArchiveErrMissingJSONL, "archive is missing required import.jsonl")
	}
	if archive.manifest == nil {
		return nil, archiveErr(ArchiveErrMissingManifest, "archive is missing required import-manifest.json")
	}
	return archive, nil
}

// ReadManifest returns the decompressed manifest, enforcing MaxManifestBytes while reading. The
// manifest is deliberately the only entry read whole: it is small and bounded.
func (a *Archive) ReadManifest() ([]byte, error) {
	body, err := readLimited(a.manifest, MaxManifestBytes)
	if err != nil {
		if err == errTooLarge {
			return nil, archiveErr(ArchiveErrManifestTooLarge, "import-manifest.json exceeds %d bytes", MaxManifestBytes)
		}
		return nil, archiveErr(ArchiveErrUnreadable, "failed to read import-manifest.json: %v", err)
	}
	return body, nil
}

// JSONLSha256 streams import.jsonl once and returns the lowercase-hex SHA-256 of its exact
// decompressed bytes, without retaining them. Reading to EOF also makes archive/zip verify the
// entry's CRC. The decompressed-size limit is enforced while reading rather than trusted from ZIP
// metadata.
func (a *Archive) JSONLSha256() (string, error) {
	rc, err := a.jsonl.Open()
	if err != nil {
		return "", archiveErr(ArchiveErrUnreadable, "failed to open import.jsonl: %v", err)
	}
	// Closed explicitly below: archive/zip verifies the entry CRC on Close, so its error matters.
	defer func() { _ = rc.Close() }()

	hasher := sha256.New()
	// Copy one byte past the limit so an exactly-oversized entry is still detected.
	written, err := io.Copy(hasher, io.LimitReader(rc, MaxJSONLBytes+1))
	if err != nil {
		return "", archiveErr(ArchiveErrUnreadable, "failed to read import.jsonl: %v", err)
	}
	if written > MaxJSONLBytes {
		return "", archiveErr(ArchiveErrJSONLTooLarge, "import.jsonl exceeds %d bytes", MaxJSONLBytes)
	}
	if err := rc.Close(); err != nil {
		return "", archiveErr(ArchiveErrUnreadable, "failed to verify import.jsonl: %v", err)
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

// OpenJSONL reopens import.jsonl for line-by-line parsing. The returned reader is bounded to
// MaxJSONLBytes+1 bytes so the caller can detect an over-limit entry, and the caller must close it.
func (a *Archive) OpenJSONL() (io.ReadCloser, error) {
	rc, err := a.jsonl.Open()
	if err != nil {
		return nil, archiveErr(ArchiveErrUnreadable, "failed to open import.jsonl: %v", err)
	}
	return &limitedReadCloser{r: io.LimitReader(rc, MaxJSONLBytes+1), c: rc}, nil
}

// limitedReadCloser applies a read limit while preserving the underlying entry's Close, which is what
// makes archive/zip verify the CRC.
type limitedReadCloser struct {
	r io.Reader
	c io.Closer
}

func (l *limitedReadCloser) Read(p []byte) (int, error) { return l.r.Read(p) }
func (l *limitedReadCloser) Close() error               { return l.c.Close() }

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

// checkEntrySafety validates mode, encryption, and compression for one entry, uniformly for every
// entry including the data/ payloads that are never opened.
//
// A name ending in "/" is accepted only when the entry's mode is genuinely a directory with no other
// special-file bits: otherwise a symlink or device node named "data/" would slip past the file
// checks by merely looking like a directory.
func checkEntrySafety(f *zip.File, name string) error {
	mode := f.Mode()
	if mode&fsModeSymlink != 0 {
		return archiveErr(ArchiveErrUnsafeEntry, "archive entry %q is a symlink", name)
	}
	if strings.HasSuffix(name, "/") {
		if !mode.IsDir() {
			return archiveErr(ArchiveErrUnsafeEntry, "archive entry %q is named as a directory but is not one", name)
		}
		// A genuine directory may carry only the directory bit among the special-file bits.
		if mode&(fs.ModeType&^fs.ModeDir) != 0 {
			return archiveErr(ArchiveErrUnsafeEntry, "archive directory entry %q has special-file bits", name)
		}
		return nil
	}
	if !mode.IsRegular() {
		return archiveErr(ArchiveErrUnsafeEntry, "archive entry %q is not a regular file", name)
	}
	// Bit 0 of the general-purpose flag marks an encrypted entry.
	if f.Flags&0x1 != 0 {
		return archiveErr(ArchiveErrEncryptedEntry, "archive entry %q is encrypted", name)
	}
	if f.Method != zip.Store && f.Method != zip.Deflate {
		return archiveErr(ArchiveErrUnsupportedMethod, "archive entry %q uses unsupported compression method %d", name, f.Method)
	}
	return nil
}

// errTooLarge is a sentinel used internally by readLimited so callers can distinguish an
// over-limit body from a genuine read failure.
var errTooLarge = fmt.Errorf("entry exceeds decompressed size limit")

// readLimited decompresses f, enforcing the decompressed byte limit while reading (not trusting
// declared ZIP metadata). It reads one byte past the limit to detect an exact overflow: an entry
// within the limit is read to EOF, which makes archive/zip verify the entry CRC; an over-limit entry
// is rejected before it can be fully decompressed, so there is no unbounded decompression.
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
