// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package importer

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"fmt"
	"testing"
)

// buildZipWithEntries returns a valid stored-method archive with the given number of small entries.
func buildZipWithEntries(t *testing.T, entries int) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for i := range entries {
		// Store rather than Deflate: this test cares about record count, not compression, and Store
		// keeps a 25 000-entry archive fast to build.
		w, err := zw.CreateHeader(&zip.FileHeader{Name: fmt.Sprintf("data/f%d", i), Method: zip.Store})
		if err != nil {
			t.Fatalf("CreateHeader: %v", err)
		}
		if _, err := w.Write([]byte("x")); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return buf.Bytes()
}

// forgeEOCDEntryCount rewrites the declared entry counts in the end-of-central-directory record,
// simulating an attacker who understates how many records the archive really carries.
func forgeEOCDEntryCount(t *testing.T, raw []byte, declared uint16) []byte {
	t.Helper()
	out := bytes.Clone(raw)
	for i := len(out) - eocdLen; i >= 0; i-- {
		if binary.LittleEndian.Uint32(out[i:]) != eocdSignature {
			continue
		}
		commentLen := int(binary.LittleEndian.Uint16(out[i+20:]))
		if i+eocdLen+commentLen != len(out) {
			continue
		}
		binary.LittleEndian.PutUint16(out[i+8:], declared)  // entries on this disk
		binary.LittleEndian.PutUint16(out[i+10:], declared) // total entries
		return out
	}
	t.Fatalf("no end-of-central-directory record found")
	return nil
}

// TestArchiveEntryCount_IgnoresForgedTrailerCount is the regression test for the bypass: the trailer's
// declared count is attacker-controlled and archive/zip does not enforce it (it reads records until one
// fails to parse, then compares only the low 16 bits), so a precheck that reads that field bounds
// nothing. The count must come from walking actual records.
func TestArchiveEntryCount_IgnoresForgedTrailerCount(t *testing.T) {
	const realEntries = 40
	raw := buildZipWithEntries(t, realEntries)

	honest, err := archiveEntryCount(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if honest != realEntries {
		t.Fatalf("honest count = %d, want %d", honest, realEntries)
	}

	// Understating the count must not change what the precheck sees.
	for _, declared := range []uint16{0, 1, 7} {
		forged := forgeEOCDEntryCount(t, raw, declared)
		got, countErr := archiveEntryCount(bytes.NewReader(forged), int64(len(forged)))
		if countErr != nil {
			t.Fatalf("declared %d: unexpected error: %v", declared, countErr)
		}
		if got != realEntries {
			t.Errorf("declared %d: count = %d, want the %d records actually present", declared, got, realEntries)
		}
	}
}

// TestOpenArchive_RejectsTooManyEntriesBeforeReaderConstruction covers the limit itself. The count is
// short-circuited once it passes the cap, so the walk never runs to completion on a hostile archive.
func TestOpenArchive_RejectsTooManyEntriesBeforeReaderConstruction(t *testing.T) {
	raw := buildZipWithEntries(t, MaxArchiveEntries+1)
	// Forge the trailer too, so the rejection cannot come from the declared value.
	raw = forgeEOCDEntryCount(t, raw, 2)

	_, err := OpenArchive(bytes.NewReader(raw), int64(len(raw)))
	ae, ok := err.(*ArchiveError)
	if !ok || ae.Code != ArchiveErrTooManyEntries {
		t.Fatalf("err = %v, want %s", err, ArchiveErrTooManyEntries)
	}
}

// TestArchiveEntryCount_HonestArchiveAtLimit pins that a legitimate archive at the boundary is still
// accepted, so the bound is off-by-one correct rather than merely safe.
func TestArchiveEntryCount_HonestArchiveAtLimit(t *testing.T) {
	raw := buildZipWithEntries(t, 3)
	got, err := archiveEntryCount(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got > MaxArchiveEntries {
		t.Fatalf("count = %d, must not exceed the limit for a 3-entry archive", got)
	}
}

// findEOCD returns the index of the archive's end-of-central-directory record.
func findEOCD(t *testing.T, raw []byte) int {
	t.Helper()
	for i := len(raw) - eocdLen; i >= 0; i-- {
		if binary.LittleEndian.Uint32(raw[i:]) != eocdSignature {
			continue
		}
		if i+eocdLen+int(binary.LittleEndian.Uint16(raw[i+20:])) == len(raw) {
			return i
		}
	}
	t.Fatalf("no end-of-central-directory record found")
	return 0
}

// makePrefixedZip64 rewrites an archive's trailer into ZIP64 form and prepends prefixLen bytes of
// unrelated data, without adjusting the offsets inside the ZIP64 record.
//
// This is the shape a self-extracting archive has — a stub ahead of the zip, with declared offsets still
// relative to the zip itself — and archive/zip reads it happily, deriving a base offset to compensate. It
// is hand-built rather than produced by zip.Writer because the writer only emits ZIP64 above 4 GiB or
// 65 535 entries, and because the point is precisely an archive no honest producer would make: the locator
// offset is real (so the reader finds the record) while the directory offset inside it is not (so the
// reader has a prefix to compensate for).
func makePrefixedZip64(t *testing.T, raw []byte, prefixLen int) []byte {
	return makePrefixedZip64WithComment(t, raw, prefixLen, 0)
}

// makePrefixedZip64WithComment is makePrefixedZip64 with a trailing archive comment of commentLen bytes.
func makePrefixedZip64WithComment(t *testing.T, raw []byte, prefixLen int, commentLen uint16) []byte {
	t.Helper()
	eocd := findEOCD(t, raw)
	entries := binary.LittleEndian.Uint16(raw[eocd+10:])
	dirSize := binary.LittleEndian.Uint32(raw[eocd+12:])
	dirOffset := binary.LittleEndian.Uint32(raw[eocd+16:])

	// 0xFE is arbitrary but must not look like a record signature to either the walk or the reader's
	// "maybe the base offset is really zero" heuristic.
	out := bytes.Repeat([]byte{0xFE}, prefixLen)
	out = append(out, raw[:eocd]...)

	recordAt := uint64(len(out))
	record := make([]byte, zip64EOCDMinLen)
	binary.LittleEndian.PutUint32(record[0:], zip64EOCDSig)
	binary.LittleEndian.PutUint64(record[4:], zip64EOCDMinLen-12) // size of the record after this field
	binary.LittleEndian.PutUint16(record[12:], 45)                // version made by
	binary.LittleEndian.PutUint16(record[14:], 45)                // version needed
	binary.LittleEndian.PutUint64(record[24:], uint64(entries))   // entries on this disk
	binary.LittleEndian.PutUint64(record[32:], uint64(entries))   // total entries
	binary.LittleEndian.PutUint64(record[40:], uint64(dirSize))
	// Deliberately not shifted by the prefix: this is what the reader compensates for, and what makes the
	// raw declared offset useless as a place to look for the directory.
	binary.LittleEndian.PutUint64(record[48:], uint64(dirOffset))
	out = append(out, record...)

	locator := make([]byte, zip64LocatorLen)
	binary.LittleEndian.PutUint32(locator[0:], zip64LocatorSig)
	binary.LittleEndian.PutUint64(locator[8:], recordAt) // real, so the reader resolves the record
	binary.LittleEndian.PutUint32(locator[16:], 1)       // total number of disks
	out = append(out, locator...)

	end := bytes.Clone(raw[eocd : eocd+eocdLen])
	binary.LittleEndian.PutUint16(end[8:], zip16BitEntrySignal)
	binary.LittleEndian.PutUint16(end[10:], zip16BitEntrySignal)
	binary.LittleEndian.PutUint32(end[12:], zip32SizeSignal)
	binary.LittleEndian.PutUint32(end[16:], zip32SizeSignal)
	binary.LittleEndian.PutUint16(end[20:], commentLen)
	out = append(out, end...)

	// 'c' is arbitrary; what matters is the comment's length, which is what pushes the EOCD away from the end
	// of the file and the locator out of any window measured from it.
	return append(out, bytes.Repeat([]byte{'c'}, int(commentLen))...)
}

// TestArchiveEntryCount_PrefixedZip64 is the regression test for a ZIP64 blind spot: the directory of a
// ZIP64 archive ends at the ZIP64 record, 76 bytes before the ordinary end-of-directory record it is found
// through, so measuring from the latter starts the walk *inside* the first record and counts nothing. On a
// prefixed archive the raw declared offset is wrong too, so both candidates counted zero and any number of
// entries passed the precheck.
//
// zip.NewReader is asserted alongside it, because a count is only meaningful for an archive the reader
// really walks: if the shape were one it rejected, the precheck would have nothing to protect.
func TestArchiveEntryCount_PrefixedZip64(t *testing.T) {
	const realEntries = 6
	plain := buildZipWithEntries(t, realEntries)

	for _, prefixLen := range []int{0, 512} {
		raw := makePrefixedZip64(t, plain, prefixLen)

		zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
		if err != nil {
			t.Fatalf("prefix %d: archive/zip rejected the archive, so the count proves nothing: %v", prefixLen, err)
		}
		if len(zr.File) != realEntries {
			t.Fatalf("prefix %d: reader parsed %d entries, want %d", prefixLen, len(zr.File), realEntries)
		}

		got, err := archiveEntryCount(bytes.NewReader(raw), int64(len(raw)))
		if err != nil {
			t.Fatalf("prefix %d: unexpected error: %v", prefixLen, err)
		}
		if got != realEntries {
			t.Errorf("prefix %d: count = %d, want the %d records the reader allocates", prefixLen, got, realEntries)
		}
	}
}

// TestArchiveEntryCount_Zip64BehindAMaximumLengthComment covers where the ZIP64 locator is *read from*.
//
// The trailer window is exactly large enough for an end-of-directory record carrying the longest legal comment,
// so with that comment the record sits at the window's first byte — and the locator, which precedes it, is not
// in the window at all. A precheck that looks for the locator inside the buffer concludes the archive is not
// ZIP64 and counts nothing, while the reader, which reaches the locator through ReaderAt, resolves the
// directory and allocates every record in it.
func TestArchiveEntryCount_Zip64BehindAMaximumLengthComment(t *testing.T) {
	const realEntries = 6
	plain := buildZipWithEntries(t, realEntries)
	raw := makePrefixedZip64WithComment(t, plain, 512, maxZipCommentLen)

	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatalf("archive/zip rejected the archive, so the count proves nothing: %v", err)
	}
	if len(zr.File) != realEntries {
		t.Fatalf("reader parsed %d entries, want %d", len(zr.File), realEntries)
	}

	got, err := archiveEntryCount(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != realEntries {
		t.Errorf("count = %d, want the %d records the reader allocates", got, realEntries)
	}
}

// TestOpenArchive_RejectsTooManyEntriesInPrefixedZip64 closes the loop end to end for the ZIP64 shape.
//
// The count is asserted first and is the load-bearing half. OpenArchive re-checks len(zr.File) after
// constructing the reader and reports the same code, so the rejection alone cannot tell you which check
// fired — and the one that matters is the one that runs *before* a struct is allocated per record.
func TestOpenArchive_RejectsTooManyEntriesInPrefixedZip64(t *testing.T) {
	raw := makePrefixedZip64(t, buildZipWithEntries(t, MaxArchiveEntries+1), 512)

	count, err := archiveEntryCount(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count <= MaxArchiveEntries {
		t.Fatalf("precheck counted %d records, so the cap would only be reached after the reader allocated them all", count)
	}

	_, err = OpenArchive(bytes.NewReader(raw), int64(len(raw)))
	ae, ok := err.(*ArchiveError)
	if !ok || ae.Code != ArchiveErrTooManyEntries {
		t.Fatalf("err = %v, want %s", err, ArchiveErrTooManyEntries)
	}
}

// TestArchiveEntryCount_NoEOCD rejects a non-zip body before any directory walk is attempted.
func TestArchiveEntryCount_NoEOCD(t *testing.T) {
	raw := []byte("this is definitely not a zip archive, not even close to one")
	if _, err := archiveEntryCount(bytes.NewReader(raw), int64(len(raw))); err == nil {
		t.Fatalf("expected rejection for a body with no end-of-central-directory record")
	}
}
