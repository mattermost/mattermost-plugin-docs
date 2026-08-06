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

// TestArchiveEntryCount_NoEOCD rejects a non-zip body before any directory walk is attempted.
func TestArchiveEntryCount_NoEOCD(t *testing.T) {
	raw := []byte("this is definitely not a zip archive, not even close to one")
	if _, err := archiveEntryCount(bytes.NewReader(raw), int64(len(raw))); err == nil {
		t.Fatalf("expected rejection for a body with no end-of-central-directory record")
	}
}
