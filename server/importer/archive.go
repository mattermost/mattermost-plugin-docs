// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package importer

import (
	"archive/zip"
	"bufio"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"math"
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
	// Bound the entry count before constructing the reader: zip.NewReader eagerly materializes a
	// struct per entry, so checking len(zr.File) afterwards is too late — an archive whose 250 MiB is
	// spent on millions of tiny central-directory records would already have consumed gigabytes.
	//
	// The count must come from *counting actual records*, never from the trailer's declared total. Go's
	// reader does not treat that total as a bound: it reads headers until one fails to parse and then
	// compares only the low 16 bits ("only compare 16 bits here", archive/zip/reader.go), so an archive
	// declaring 0 entries while shipping 65 536 of them passes its check. Reading a number the parser
	// itself ignores would bound nothing.
	entries, err := archiveEntryCount(r, n)
	if err != nil {
		return nil, err
	}
	if entries > MaxArchiveEntries {
		return nil, archiveErr(ArchiveErrTooManyEntries, "archive has more than %d central-directory entries", MaxArchiveEntries)
	}

	zr, err := zip.NewReader(r, n)
	if err != nil {
		return nil, archiveErr(ArchiveErrUnreadable, "failed to read zip archive: %v", err)
	}
	// Defensive: the header is attacker-controlled, so re-check what the reader actually parsed.
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

// ZIP structural constants used to locate and walk the central directory.
const (
	eocdSignature       = 0x06054b50 // "PK\x05\x06" end of central directory
	eocdLen             = 22
	zip64LocatorSig     = 0x07064b50 // "PK\x06\x07" ZIP64 EOCD locator
	zip64LocatorLen     = 20
	zip64EOCDSig        = 0x06064b50 // "PK\x06\x06" ZIP64 end of central directory
	zip64EOCDMinLen     = 56
	maxZipCommentLen    = 65535
	zip16BitEntrySignal = 0xFFFF // total-entries value meaning "see the ZIP64 record"
	zip32SizeSignal     = 0xFFFFFFFF
	cdHeaderSignature   = 0x02014b50 // "PK\x01\x02" central-directory file header
	cdHeaderLen         = 46         // fixed part, before name/extra/comment
	cdScanBufferBytes   = 64 * 1024
)

// archiveEntryCount returns the number of central-directory records actually present, stopping as soon
// as the count exceeds MaxArchiveEntries. Nothing in the trailer is trusted as a count: only the
// directory's *location* is read from it, and the records are then walked.
//
// The walk is cheap and bounded — it stops after MaxArchiveEntries+1 records, and reads only each
// record's 46-byte fixed header (skipping name, extra, and comment), so at most a couple of megabytes
// are touched regardless of how the archive is shaped.
func archiveEntryCount(r io.ReaderAt, n int64) (uint64, error) {
	starts, err := directoryStartCandidates(r, n)
	if err != nil {
		return 0, err
	}
	// Go picks the directory start from the trailer with a fallback heuristic, so more than one offset
	// can be the one it walks. Take the largest count across the candidates: over-counting a malformed
	// archive only costs a rejection, whereas under-counting would reopen the allocation hole.
	var most uint64
	for _, start := range starts {
		count, countErr := countDirectoryRecords(r, start, n)
		if countErr != nil {
			return 0, countErr
		}
		most = max(most, count)
		if most > MaxArchiveEntries {
			return most, nil
		}
	}
	return most, nil
}

// countDirectoryRecords walks contiguous central-directory records from start, returning how many it
// found. It stops at the first non-record byte sequence, at end of file, or once the count passes
// MaxArchiveEntries — whichever comes first.
func countDirectoryRecords(r io.ReaderAt, start, n int64) (uint64, error) {
	if start < 0 || start >= n {
		return 0, nil
	}
	section := io.NewSectionReader(r, start, n-start)
	buffered := bufio.NewReaderSize(section, cdScanBufferBytes)
	header := make([]byte, cdHeaderLen)

	var count uint64
	for {
		if _, err := io.ReadFull(buffered, header); err != nil {
			// A short or absent record simply ends the directory; that is a shape zip.NewReader will
			// diagnose itself, and it is not this function's job to classify it.
			return count, nil
		}
		if binary.LittleEndian.Uint32(header) != cdHeaderSignature {
			return count, nil
		}
		count++
		if count > MaxArchiveEntries {
			// Stop immediately: the caller rejects, and walking further would be the very work this
			// precheck exists to avoid.
			return count, nil
		}
		skip := int64(binary.LittleEndian.Uint16(header[28:])) + // file name length
			int64(binary.LittleEndian.Uint16(header[30:])) + // extra field length
			int64(binary.LittleEndian.Uint16(header[32:])) // file comment length
		if _, err := buffered.Discard(int(skip)); err != nil {
			return count, nil
		}
	}
}

// directoryStartCandidates returns the offsets at which the central directory may begin, read from the
// archive trailer. It reads at most ~64 KiB plus one ZIP64 record.
//
// Two candidates exist because archive/zip derives the directory start from a computed base offset
// (end-of-directory offset minus the declared directory size) but falls back to the raw declared offset
// when that base looks wrong — a concession to self-extracting archives with prepended data. Both are
// returned so the caller can bound whichever one the reader ends up walking.
func directoryStartCandidates(r io.ReaderAt, n int64) ([]int64, error) {
	if n < eocdLen {
		return nil, archiveErr(ArchiveErrUnreadable, "archive is too small to be a zip file")
	}
	tailLen := min(int64(maxZipCommentLen+eocdLen), n)
	tail := make([]byte, tailLen)
	if _, err := r.ReadAt(tail, n-tailLen); err != nil {
		return nil, archiveErr(ArchiveErrUnreadable, "failed to read zip trailer: %v", err)
	}

	// Scan backwards for the last EOCD whose declared comment length matches the remaining bytes; a
	// later false positive inside a comment would otherwise win.
	eocd := -1
	for i := len(tail) - eocdLen; i >= 0; i-- {
		if binary.LittleEndian.Uint32(tail[i:]) != eocdSignature {
			continue
		}
		commentLen := int(binary.LittleEndian.Uint16(tail[i+20:]))
		if i+eocdLen+commentLen == len(tail) {
			eocd = i
			break
		}
	}
	if eocd < 0 {
		return nil, archiveErr(ArchiveErrUnreadable, "archive has no end-of-central-directory record")
	}
	eocdOffset := n - tailLen + int64(eocd)

	entries := uint64(binary.LittleEndian.Uint16(tail[eocd+10:]))
	dirSize := uint64(binary.LittleEndian.Uint32(tail[eocd+12:]))
	dirOffset := uint64(binary.LittleEndian.Uint32(tail[eocd+16:]))

	// The directory ends where the end-of-directory record begins, and archive/zip derives the directory
	// start by subtracting the declared size from there. Every candidate below is that subtraction, or the
	// raw declared offset the reader falls back to.
	candidates := directoryStarts(eocdOffset, dirSize, dirOffset)

	// Any saturated 32/16-bit field means the real values live in the ZIP64 record. The 0xFFFF form of the
	// directory *size* is included because archive/zip tests for it too ("d.directorySize == 0xffff"),
	// which is almost certainly a typo for the 32-bit signal — but a precheck has to consult the record
	// whenever the parser might, or the two disagree about where the directory is.
	if entries == zip16BitEntrySignal || dirSize == zip16BitEntrySignal ||
		dirSize == zip32SizeSignal || dirOffset == zip32SizeSignal {
		zip64, err := readZip64Directory(r, n, eocdOffset)
		if err != nil {
			return nil, err
		}
		if zip64 != nil {
			// Which record counts as "the end" is the whole subtlety: having consulted the ZIP64 record,
			// the reader measures the directory's end from *that* record ("directoryEndOffset = p"), 76
			// bytes before the ordinary EOCD it was found through. Reading the size from ZIP64 while still
			// measuring from the ordinary EOCD lands 76 bytes past the directory's first record, which in a
			// prefixed archive — where the raw declared offset is wrong too — leaves nothing counted at all.
			candidates = append(candidates, directoryStarts(zip64.recordOffset, zip64.dirSize, zip64.dirOffset)...)

			// The 32-bit candidates stay. The 0xFFFF size condition above can send us to the ZIP64 record in
			// a case the reader resolves from the 32-bit fields, and the reader also ignores a record whose
			// disk numbers it dislikes, so dropping them would move this blind spot rather than close it.
			// Over-counting a malformed archive costs a rejection; under-counting reopens the allocation
			// hole the precheck exists to close.
			candidates = append(candidates, directoryStarts(eocdOffset, zip64.dirSize, zip64.dirOffset)...)
		}
	}
	return candidates, nil
}

// directoryStarts returns the offsets at which a directory of dirSize, declared at dirOffset, may begin
// when its end-of-directory record starts at end.
//
// Values too large to be a file offset are dropped rather than rejected: the archive is bounded to
// MaxBundleUploadBytes, so a saturated field is a signal to look elsewhere, not a fatal shape. Whether
// anything is left to walk is settled by walking it.
func directoryStarts(end int64, dirSize, dirOffset uint64) []int64 {
	starts := make([]int64, 0, 2)
	if dirSize <= math.MaxInt64 {
		starts = append(starts, end-int64(dirSize))
	}
	if dirOffset <= math.MaxInt64 {
		starts = append(starts, int64(dirOffset))
	}
	return starts
}

// zip64Directory is the central-directory location read from a ZIP64 end-of-central-directory record,
// together with where that record itself begins — which is the offset archive/zip measures the directory's
// end from once it has consulted the record.
type zip64Directory struct {
	dirSize      uint64
	dirOffset    uint64
	recordOffset int64
}

// readZip64Directory reads the ZIP64 end-of-central-directory record through its locator, returning where
// the directory is and where the record itself begins. A nil result means no usable locator is present, in
// which case the caller keeps the 32-bit values.
//
// The locator is read from its absolute offset rather than from the trailer buffer the EOCD was found in. With
// a maximum-length comment the EOCD sits at the very start of that buffer and the locator — which precedes it —
// falls outside entirely, so a buffer-relative read finds nothing and concludes the archive is not ZIP64. The
// reader reaches it through ReaderAt regardless, so it would find the record, resolve the directory, and
// allocate every entry in it while the precheck had counted none.
func readZip64Directory(r io.ReaderAt, n int64, eocdOffset int64) (*zip64Directory, error) {
	locatorOffset := eocdOffset - zip64LocatorLen
	if locatorOffset < 0 {
		return nil, nil
	}
	locator := make([]byte, zip64LocatorLen)
	if _, err := r.ReadAt(locator, locatorOffset); err != nil {
		// Not readable is not malformed: there simply is no locator there.
		return nil, nil
	}
	if binary.LittleEndian.Uint32(locator) != zip64LocatorSig {
		return nil, nil
	}
	recordAt := binary.LittleEndian.Uint64(locator[8:])
	if recordAt > math.MaxInt64 {
		return nil, archiveErr(ArchiveErrUnreadable, "archive zip64 directory offset is out of range")
	}
	recordOffset := int64(recordAt)
	if n < zip64EOCDMinLen || recordOffset > n-zip64EOCDMinLen {
		return nil, archiveErr(ArchiveErrUnreadable, "archive zip64 directory offset is out of range")
	}
	record := make([]byte, zip64EOCDMinLen)
	if _, readErr := r.ReadAt(record, recordOffset); readErr != nil {
		return nil, archiveErr(ArchiveErrUnreadable, "failed to read zip64 directory: %v", readErr)
	}
	if binary.LittleEndian.Uint32(record) != zip64EOCDSig {
		return nil, archiveErr(ArchiveErrUnreadable, "archive zip64 directory record is malformed")
	}
	return &zip64Directory{
		dirSize:      binary.LittleEndian.Uint64(record[40:]),
		dirOffset:    binary.LittleEndian.Uint64(record[48:]),
		recordOffset: recordOffset,
	}, nil
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
