// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package model

import (
	"strings"
	"testing"

	mmmodel "github.com/mattermost/mattermost/server/public/model"
)

func TestImportJobState_Predicates(t *testing.T) {
	terminal := []ImportJobState{ImportStateCompleted, ImportStateCompletedWithIssues, ImportStateFailed, ImportStateCanceled}
	for _, s := range terminal {
		if !s.IsTerminal() {
			t.Errorf("%s should be terminal", s)
		}
		if s.IsWorkerOwned() {
			t.Errorf("%s (terminal) must not be worker-owned", s)
		}
	}
	owning := []ImportJobState{ImportStateQueuedPreflight, ImportStatePreflighting, ImportStateQueuedImport, ImportStateImporting, ImportStateTerminalizing}
	for _, s := range owning {
		if s.IsTerminal() {
			t.Errorf("%s should not be terminal", s)
		}
		if !s.IsWorkerOwned() {
			t.Errorf("%s should be worker-owned", s)
		}
	}
	// Jobs waiting on a human are deliberately not worker-owned: that is what lets a later job
	// preflight and even execute while an earlier one sits unconfirmed, with mapping-revision
	// invalidation rather than queue ownership providing safety.
	for _, s := range []ImportJobState{ImportStateAwaitingSource, ImportStateAwaitingConfirmation} {
		if s.IsWorkerOwned() {
			t.Errorf("%s must not be worker-owned", s)
		}
		if !s.AwaitsUser() {
			t.Errorf("%s should await the user", s)
		}
	}
	if ImportJobState("bogus").IsValid() {
		t.Errorf("bogus state should be invalid")
	}
	if !ImportStateAwaitingSource.IsValid() {
		t.Errorf("awaiting_source should be valid")
	}
}

func TestIsValidImportHash(t *testing.T) {
	valid := strings.Repeat("a", 64)
	if !IsValidImportHash("") || !IsValidImportHash(valid) {
		t.Errorf("empty and 64-hex should be valid")
	}
	for _, bad := range []string{strings.Repeat("A", 64), strings.Repeat("a", 63), strings.Repeat("a", 65), "xyz"} {
		if IsValidImportHash(bad) {
			t.Errorf("%q should be invalid", bad)
		}
	}
}

func validJob() *ImportJob {
	return &ImportJob{
		Id:                  mmmodel.NewId(),
		ActorId:             mmmodel.NewId(),
		TeamId:              mmmodel.NewId(),
		TargetKind:          ImportTargetNew,
		TargetSpaceId:       mmmodel.NewId(),
		SourceSelectionMode: ImportSourceModeNew,
		State:               ImportStateQueuedPreflight,
		BundleSha256:        strings.Repeat("a", 64),
		CreateAt:            1,
		UpdateAt:            1,
		RetainUntil:         2,
	}
}

func TestImportJob_IsValid(t *testing.T) {
	if err := validJob().IsValid(); err != nil {
		t.Fatalf("valid job rejected: %v", err)
	}

	tests := map[string]func(*ImportJob){
		"bad id":                   func(j *ImportJob) { j.Id = "x" },
		"bad actor":                func(j *ImportJob) { j.ActorId = "" },
		"bad target kind":          func(j *ImportJob) { j.TargetKind = "sideways" },
		"bad target space":         func(j *ImportJob) { j.TargetSpaceId = "" },
		"bad source mode":          func(j *ImportJob) { j.SourceSelectionMode = "maybe" },
		"bad state":                func(j *ImportJob) { j.State = "limbo" },
		"bad intent":               func(j *ImportJob) { j.TerminalIntent = "sideways" },
		"intentless terminalizing": func(j *ImportJob) { j.State = ImportStateTerminalizing },
		"bad bundle sha":           func(j *ImportJob) { j.BundleSha256 = "nothex" },
		"empty bundle sha":         func(j *ImportJob) { j.BundleSha256 = "" },
		"bad preflight rev":        func(j *ImportJob) { j.PreflightRevision = "short" },
		"zero timestamps":          func(j *ImportJob) { j.CreateAt = 0 },
		"long space title":         func(j *ImportJob) { j.ConfirmedSpaceTitle = strings.Repeat("x", ImportSpaceTitleMaxRunes+1) },
		"long source name":         func(j *ImportJob) { j.SelectedSourceDisplayName = strings.Repeat("x", ImportDisplayNameMaxRunes+1) },
		"long error code":          func(j *ImportJob) { j.ErrorCode = strings.Repeat("x", ImportErrorCodeMaxRunes+1) },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			j := validJob()
			mutate(j)
			if err := j.IsValid(); err == nil {
				t.Errorf("expected rejection for %q", name)
			}
		})
	}
}

func TestImportSource_IsValid(t *testing.T) {
	valid := &ImportSource{
		Id:               mmmodel.NewId(),
		SpaceId:          mmmodel.NewId(),
		SourceType:       ImportSourceTypeConfluence,
		ExternalSpaceKey: "DOCS",
		CreatedBy:        mmmodel.NewId(),
		CreateAt:         1,
		UpdateAt:         1,
	}
	if err := valid.IsValid(); err != nil {
		t.Fatalf("valid source rejected: %v", err)
	}
	bad := *valid
	bad.SourceType = "notion"
	if err := bad.IsValid(); err == nil {
		t.Errorf("non-confluence source type should be rejected")
	}
	bad2 := *valid
	bad2.ExternalSpaceKey = ""
	if err := bad2.IsValid(); err == nil {
		t.Errorf("empty space key should be rejected")
	}
	bad3 := *valid
	bad3.DisplayName = strings.Repeat("x", ImportDisplayNameMaxRunes+1)
	if err := bad3.IsValid(); err == nil {
		t.Errorf("over-long display name should be rejected")
	}
}

func TestImportIssueRecord_IsValid(t *testing.T) {
	valid := &ImportIssueRecord{Stage: ImportStagePreflight, Severity: ImportSeverityWarning, Code: "some_code", Message: "m"}
	if err := valid.IsValid(); err != nil {
		t.Fatalf("valid issue rejected: %v", err)
	}
	tests := map[string]func(*ImportIssueRecord){
		"bad stage":    func(r *ImportIssueRecord) { r.Stage = "nowhere" },
		"bad severity": func(r *ImportIssueRecord) { r.Severity = "meh" },
		"empty code":   func(r *ImportIssueRecord) { r.Code = "" },
		"long code":    func(r *ImportIssueRecord) { r.Code = strings.Repeat("c", ImportIssueCodeMaxRunes+1) },
		"empty msg":    func(r *ImportIssueRecord) { r.Message = "" },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			r := *valid
			mutate(&r)
			if err := r.IsValid(); err == nil {
				t.Errorf("expected rejection for %q", name)
			}
		})
	}
}

func TestImportConfirmation_ValueAndScan(t *testing.T) {
	// An empty confirmation persists as a JSON object, preserving the column's NOT NULL DEFAULT '{}'.
	v, err := ImportConfirmation{}.Value()
	if err != nil {
		t.Fatalf("empty Value: %v", err)
	}
	if s, _ := v.(string); s == "" || s[0] != '{' {
		t.Errorf("empty Value = %v, want a JSON object", v)
	}

	// A confirmation approving thousands of conflicts exceeds mmmodel.StringInterface's internal
	// 1 MiB valuer limit, which is exactly why these columns do not use that type.
	big := ImportConfirmation{PreflightRevision: strings.Repeat("a", 64)}
	for range 4000 {
		big.OverwriteConflicts = append(big.OverwriteConflicts, strings.Repeat("9", 300))
	}
	if _, err := big.Value(); err != nil {
		t.Fatalf("a large but valid confirmation must persist, got %v", err)
	}
	blob := make(mmmodel.StringInterface)
	blob["blob"] = strings.Repeat("x", 1_500_000)
	if _, siErr := blob.Value(); siErr == nil {
		t.Errorf("expected StringInterface to reject a >1 MiB payload (sanity check on the motivation)")
	}

	// Round-trips through Scan for both []byte and string sources.
	var c ImportConfirmation
	if err := c.Scan([]byte(`{"preflight_revision":"abc","overwrite_conflicts":["101"]}`)); err != nil {
		t.Fatalf("Scan([]byte): %v", err)
	}
	if c.PreflightRevision != "abc" || len(c.OverwriteConflicts) != 1 || c.OverwriteConflicts[0] != "101" {
		t.Errorf("Scan([]byte) produced %+v", c)
	}
	if err := c.Scan(nil); err != nil {
		t.Errorf("Scan(nil): %v", err)
	}
}

func TestImportConfirmation_IsValid(t *testing.T) {
	revision := strings.Repeat("a", 64)
	valid := &ImportConfirmation{PreflightRevision: revision, OverwriteConflicts: []string{"101", "205"}}
	if err := valid.IsValid(); err != nil {
		t.Fatalf("valid confirmation rejected: %v", err)
	}
	tests := map[string]func(*ImportConfirmation){
		"bad revision":    func(c *ImportConfirmation) { c.PreflightRevision = "short" },
		"duplicate id":    func(c *ImportConfirmation) { c.OverwriteConflicts = []string{"101", "101"} },
		"non-contract id": func(c *ImportConfirmation) { c.OverwriteConflicts = []string{"has space"} },
		"over-long id": func(c *ImportConfirmation) {
			c.OverwriteConflicts = []string{strings.Repeat("9", ImportExternalIDMaxBytes+1)}
		},
		"long space title": func(c *ImportConfirmation) {
			c.NewSpace = &ImportNewSpaceMetadata{Title: strings.Repeat("x", ImportSpaceTitleMaxRunes+1)}
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			c := &ImportConfirmation{PreflightRevision: revision}
			mutate(c)
			if err := c.IsValid(); err == nil {
				t.Errorf("expected rejection for %q", name)
			}
		})
	}
}

func TestNewImportFidelity(t *testing.T) {
	f := NewImportFidelity()
	if f.FullFidelity {
		t.Errorf("full_fidelity must always be false")
	}
	if f.Scope != FidelityScopePagesOnly || f.Comments != FidelityCountedNotImported {
		t.Errorf("unexpected fidelity: %+v", f)
	}
}
