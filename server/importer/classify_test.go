// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package importer

import (
	"slices"
	"testing"

	"github.com/mattermost/mattermost-plugin-docs/server/model"
)

const (
	hashA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	hashB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	hashC = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
)

// mappedInput returns a ClassifyInput for a page already imported into a source, with the given source
// and local hashes. Baselines are hashA for both sides, so passing hashA means "unchanged".
func mappedInput(incomingSourceHash, currentLocalHash string) ClassifyInput {
	return ClassifyInput{
		IncomingSourceContentHash: incomingSourceHash,
		TargetSpaceID:             "space",
		Mapping: &MappingBaseline{
			ExternalID:             "100",
			LocalID:                "local-100",
			LastSourceContentHash:  hashA,
			LastAppliedContentHash: hashA,
		},
		Local: LocalPageState{
			Exists:             true,
			SpaceID:            "space",
			AppliedContentHash: currentLocalHash,
			BodyIsCanonical:    true,
		},
		ParentAvailable: true,
	}
}

// TestClassify_DecisionTable is the reimport decision table. It is the single most consequential piece of
// logic in the importer: getting a cell wrong either discards someone's edits or silently refuses to apply
// a legitimate update.
func TestClassify_DecisionTable(t *testing.T) {
	tests := map[string]struct {
		in         ClassifyInput
		wantAction model.ImportAction
		wantIssue  string
		wantEligib bool
	}{
		"unchanged source, unchanged local": {
			in:         mappedInput(hashA, hashA),
			wantAction: model.ImportActionNoop,
		},
		"changed source, unchanged local": {
			in:         mappedInput(hashB, hashA),
			wantAction: model.ImportActionUpdate,
		},
		"unchanged source, changed local": {
			in:         mappedInput(hashA, hashB),
			wantAction: model.ImportActionPreserveLocal,
			wantIssue:  IssueLocalChangesPreserved,
		},
		"changed source, changed local": {
			in:         mappedInput(hashB, hashC),
			wantAction: model.ImportActionConflict,
			wantIssue:  IssueSourceAndLocalConflict,
			wantEligib: true,
		},
		"no mapping": {
			in:         ClassifyInput{IncomingSourceContentHash: hashB, ParentAvailable: true},
			wantAction: model.ImportActionCreate,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got := Classify(tc.in)
			if got.Action != tc.wantAction {
				t.Errorf("action = %s, want %s", got.Action, tc.wantAction)
			}
			if tc.wantIssue != "" && !slices.Contains(got.Issues, tc.wantIssue) {
				t.Errorf("issues = %v, want to contain %s", got.Issues, tc.wantIssue)
			}
			if got.OverwriteEligible != tc.wantEligib {
				t.Errorf("overwrite_eligible = %v, want %v", got.OverwriteEligible, tc.wantEligib)
			}
			// Only a conflict is ever approvable for overwrite: approving anything else would discard edits
			// the user was never shown.
			if got.OverwriteEligible && got.Action != model.ImportActionConflict {
				t.Errorf("%s must not be overwrite-eligible", got.Action)
			}
		})
	}
}

// TestClassify_StructureIsIndependentOfContent pins the separation the whole hash split exists for. A
// parent move — in either direction — must not turn an otherwise safe decision into a different one.
func TestClassify_StructureIsIndependentOfContent(t *testing.T) {
	t.Run("a source parent move does not block a content update", func(t *testing.T) {
		in := mappedInput(hashB, hashA)
		in.IncomingParentExternalID = "999"
		in.Mapping.LastSourceParentExternalID = "100"
		got := Classify(in)
		if got.Action != model.ImportActionUpdate {
			t.Errorf("action = %s, want update", got.Action)
		}
		if !slices.Contains(got.Issues, IssueSourceParentChangedNotApplied) {
			t.Errorf("the source move must be reported: %v", got.Issues)
		}
	})

	t.Run("a local parent move is preserved and does not count as a content edit", func(t *testing.T) {
		in := mappedInput(hashA, hashA)
		in.Local.ParentID = "moved-here"
		in.Mapping.LastAppliedParentID = "was-here"
		got := Classify(in)
		// Content is untouched on both sides, so this is still a no-op — a local move must not read as a
		// local content change, or every reorganized page would become a conflict on the next import.
		if got.Action != model.ImportActionNoop {
			t.Errorf("action = %s, want noop", got.Action)
		}
		if !slices.Contains(got.Issues, IssueLocalParentChangedPreserved) {
			t.Errorf("the preserved local move must be reported: %v", got.Issues)
		}
	})

	t.Run("a source order change does not make content an update", func(t *testing.T) {
		in := mappedInput(hashA, hashA)
		in.IncomingSourceOrdinal = 7
		in.Mapping.LastSourceOrdinal = 2
		got := Classify(in)
		if got.Action != model.ImportActionNoop {
			t.Errorf("action = %s, want noop", got.Action)
		}
		if !slices.Contains(got.Issues, IssueSourceOrderChangedNotApplied) {
			t.Errorf("the source reorder must be reported: %v", got.Issues)
		}
	})
}

// TestClassify_BlockedCases covers every reason a page is refused rather than written. Each of these would
// otherwise cause real damage: recreating a deleted page, writing into a Space nobody reviewed, or
// silently rooting an orphan at the top of the tree.
func TestClassify_BlockedCases(t *testing.T) {
	t.Run("the mapped page was deleted", func(t *testing.T) {
		in := mappedInput(hashB, hashB)
		in.Local.Deleted = true
		got := Classify(in)
		if got.Action != model.ImportActionBlocked || !slices.Contains(got.Issues, IssueMappedTargetMissing) {
			t.Errorf("got %s %v, want blocked/%s", got.Action, got.Issues, IssueMappedTargetMissing)
		}
		if got.LocalID != "local-100" {
			t.Errorf("a blocked mapping must still name its page, got %q", got.LocalID)
		}
	})

	t.Run("the mapped page is gone entirely", func(t *testing.T) {
		in := mappedInput(hashB, hashB)
		in.Local = LocalPageState{Exists: false}
		if got := Classify(in); got.Action != model.ImportActionBlocked {
			t.Errorf("action = %s, want blocked", got.Action)
		}
	})

	t.Run("the mapped page moved to another Space", func(t *testing.T) {
		in := mappedInput(hashB, hashA)
		in.Local.SpaceID = "elsewhere"
		got := Classify(in)
		if got.Action != model.ImportActionBlocked || !slices.Contains(got.Issues, IssueMappedTargetWrongSpace) {
			t.Errorf("got %s %v, want blocked/%s", got.Action, got.Issues, IssueMappedTargetWrongSpace)
		}
	})

	t.Run("a new page whose parent resolves to nothing", func(t *testing.T) {
		got := Classify(ClassifyInput{IncomingSourceContentHash: hashB, ParentAvailable: false})
		if got.Action != model.ImportActionBlocked || !slices.Contains(got.Issues, IssueParentMappingMissing) {
			t.Errorf("got %s %v, want blocked/%s", got.Action, got.Issues, IssueParentMappingMissing)
		}
	})

	t.Run("the source is at its retained-mapping cap", func(t *testing.T) {
		got := Classify(ClassifyInput{IncomingSourceContentHash: hashB, ParentAvailable: true, MappingCapacityExceeded: true})
		if got.Action != model.ImportActionBlocked || !slices.Contains(got.Issues, IssueMappingCapacityExceeded) {
			t.Errorf("got %s %v, want blocked/%s", got.Action, got.Issues, IssueMappingCapacityExceeded)
		}
	})
}

// TestClassify_OpaqueLocalBodyIsReported covers a page whose stored body the importer can no longer
// canonicalize. Its opaque hash already differs from any canonical baseline, so it is protected as a local
// edit; this pins that the reason is reported rather than left as an unexplained "preserved".
func TestClassify_OpaqueLocalBodyIsReported(t *testing.T) {
	in := mappedInput(hashA, hashB)
	in.Local.BodyIsCanonical = false
	got := Classify(in)
	if got.Action != model.ImportActionPreserveLocal {
		t.Errorf("action = %s, want preserve_local", got.Action)
	}
	if !slices.Contains(got.Issues, IssueLocalBodyNotCanonical) {
		t.Errorf("issues = %v, want to contain %s", got.Issues, IssueLocalBodyNotCanonical)
	}
}

// TestClassify_IssueOrderIsDeterministic matters because the preflight revision digests these codes: an
// unstable order would change the revision between two identical computations and invalidate a
// confirmation for no reason.
func TestClassify_IssueOrderIsDeterministic(t *testing.T) {
	build := func() ClassifyInput {
		in := mappedInput(hashB, hashC)
		in.Local.BodyIsCanonical = false
		in.IncomingParentExternalID = "999"
		in.Mapping.LastSourceParentExternalID = "100"
		in.Local.ParentID = "moved"
		in.Mapping.LastAppliedParentID = "original"
		in.IncomingSourceOrdinal = 3
		return in
	}
	first := Classify(build()).Issues
	for range 5 {
		if got := Classify(build()).Issues; !slices.Equal(got, first) {
			t.Fatalf("issue order is not stable: %v vs %v", got, first)
		}
	}
	want := []string{
		IssueSourceAndLocalConflict,
		IssueLocalBodyNotCanonical,
		IssueSourceParentChangedNotApplied,
		IssueLocalParentChangedPreserved,
		IssueSourceOrderChangedNotApplied,
	}
	if !slices.Equal(first, want) {
		t.Errorf("issues = %v, want %v", first, want)
	}
}

// TestOutcomeForPlannedAction pins the planned-action to outcome mapping, so a preflight report reads the
// same way before and after execution replaces it with what actually happened.
func TestOutcomeForPlannedAction(t *testing.T) {
	want := map[model.ImportAction]model.ImportOutcome{
		model.ImportActionCreate:        model.ImportOutcomeCreated,
		model.ImportActionUpdate:        model.ImportOutcomeUpdated,
		model.ImportActionNoop:          model.ImportOutcomeUnchanged,
		model.ImportActionPreserveLocal: model.ImportOutcomeLocalPreserved,
		model.ImportActionConflict:      model.ImportOutcomeConflictSkipped,
		model.ImportActionStale:         model.ImportOutcomeStale,
		model.ImportActionBlocked:       model.ImportOutcomeBlocked,
	}
	for action, outcome := range want {
		if got := OutcomeForPlannedAction(action); got != outcome {
			t.Errorf("%s -> %s, want %s", action, got, outcome)
		}
		// Every mapping must land on an outcome the model accepts and that is not the empty "undecided"
		// value: a result row with no outcome is the one thing a report must never contain.
		mapped := OutcomeForPlannedAction(action)
		if mapped == "" || !mapped.IsValid() {
			t.Errorf("%s produced an outcome the model rejects: %q", action, mapped)
		}
	}
}

// TestRequiresReimportAcknowledgement pins which actions mean "this import touches pages that already
// exist". A noop counts: the user is still reimporting, even though nothing changes.
func TestRequiresReimportAcknowledgement(t *testing.T) {
	for _, action := range []model.ImportAction{
		model.ImportActionUpdate, model.ImportActionNoop, model.ImportActionPreserveLocal,
		model.ImportActionConflict, model.ImportActionStale,
	} {
		if !RequiresReimportAcknowledgement(action) {
			t.Errorf("%s should require the reimport acknowledgement", action)
		}
	}
	for _, action := range []model.ImportAction{
		model.ImportActionCreate, model.ImportActionBlocked, model.ImportActionNotAttempted,
	} {
		if RequiresReimportAcknowledgement(action) {
			t.Errorf("%s should not require the reimport acknowledgement", action)
		}
	}
}
