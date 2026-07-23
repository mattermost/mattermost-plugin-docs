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
		"bad preflight rev": func(j *ImportJob) { j.PreflightRevision = "short" },
		"zero timestamps":   func(j *ImportJob) { j.CreateAt = 0 },
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
