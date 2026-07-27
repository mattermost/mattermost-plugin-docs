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
		if s.OwnsSourceQueue() {
			t.Errorf("%s (terminal) must not own the source queue", s)
		}
	}
	owning := []ImportJobState{ImportStateQueuedPreflight, ImportStatePreflighting, ImportStateAwaitingConfirmation, ImportStateQueuedImport, ImportStateImporting, ImportStateCanceling}
	for _, s := range owning {
		if s.IsTerminal() {
			t.Errorf("%s should not be terminal", s)
		}
		if !s.OwnsSourceQueue() {
			t.Errorf("%s should own the source queue", s)
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
		"bad id":            func(j *ImportJob) { j.Id = "x" },
		"bad actor":         func(j *ImportJob) { j.ActorId = "" },
		"bad target kind":   func(j *ImportJob) { j.TargetKind = "sideways" },
		"bad target space":  func(j *ImportJob) { j.TargetSpaceId = "" },
		"bad source mode":   func(j *ImportJob) { j.SourceSelectionMode = "maybe" },
		"bad state":         func(j *ImportJob) { j.State = "limbo" },
		"bad bundle sha":    func(j *ImportJob) { j.BundleSha256 = "nothex" },
		"empty bundle sha":  func(j *ImportJob) { j.BundleSha256 = "" },
		"bad preflight rev": func(j *ImportJob) { j.PreflightRevision = "short" },
		"zero timestamps":   func(j *ImportJob) { j.CreateAt = 0 },
		"long space title":  func(j *ImportJob) { j.ConfirmedSpaceTitle = strings.Repeat("x", ImportSpaceTitleMaxRunes+1) },
		"long source name":  func(j *ImportJob) { j.SelectedSourceDisplayName = strings.Repeat("x", ImportDisplayNameMaxRunes+1) },
		"long error code":   func(j *ImportJob) { j.ErrorCode = strings.Repeat("x", ImportErrorCodeMaxRunes+1) },
		"oversize confirm":  func(j *ImportJob) { j.Confirmation = make(ImportConfirmation, ImportConfirmationMaxBytes+1) },
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
	// Empty confirmation persists as an empty JSON object (matches the column's NOT NULL DEFAULT).
	empty := ImportConfirmation(nil)
	v, err := empty.Value()
	if err != nil {
		t.Fatalf("empty Value: %v", err)
	}
	if v != "{}" {
		t.Errorf("empty Value = %v, want {}", v)
	}

	// A payload larger than 1 MiB (which mmmodel.StringInterface's Value() would reject) must be
	// accepted, since a valid confirmation with thousands of conflict descriptors exceeds 1 MiB.
	oneAndHalfMiB := ImportConfirmation(`{"overwrite_conflicts":[` + strings.Repeat("0", 1_500_000) + `]}`)
	if _, err := oneAndHalfMiB.Value(); err != nil {
		t.Fatalf("1.5 MiB confirmation should persist, got %v", err)
	}
	// Confirm StringInterface would have rejected the same size, i.e. our new type is what unblocks it.
	big := make(mmmodel.StringInterface)
	big["blob"] = strings.Repeat("x", 1_500_000)
	if _, siErr := big.Value(); siErr == nil {
		t.Errorf("expected StringInterface to reject a >1 MiB payload (sanity check on the motivation)")
	}

	// Over the deliberate cap is rejected as a backstop.
	over := ImportConfirmation(strings.Repeat("x", ImportConfirmationMaxBytes+1))
	if _, err := over.Value(); err == nil {
		t.Errorf("expected rejection above ImportConfirmationMaxBytes")
	}

	// Round-trips through Scan for both []byte and string sources.
	var c ImportConfirmation
	if err := c.Scan([]byte(`{"a":1}`)); err != nil || string(c) != `{"a":1}` {
		t.Errorf("Scan([]byte) = %q, %v", string(c), err)
	}
	if err := c.Scan(`{"b":2}`); err != nil || string(c) != `{"b":2}` {
		t.Errorf("Scan(string) = %q, %v", string(c), err)
	}
	if err := c.Scan(nil); err != nil || c != nil {
		t.Errorf("Scan(nil) = %q, %v", string(c), err)
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
