// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package importer

import (
	"github.com/mattermost/mattermost-plugin-docs/server/model"
)

// Preflight issue codes. They are stable strings shared by the report, the wizard, and the tests, so a
// message can be reworded without breaking anything that keys off the finding.
const (
	IssueLocalChangesPreserved         = "local_changes_preserved"
	IssueSourceAndLocalConflict        = "source_and_local_conflict"
	IssueMappedTargetMissing           = "mapped_target_missing"
	IssueMappedTargetWrongSpace        = "mapped_target_wrong_space"
	IssueParentMappingMissing          = "parent_mapping_missing"
	IssueSourcePageStale               = "source_page_stale"
	IssueSourceOrderChangedNotApplied  = "source_order_changed_not_applied"
	IssueSourceParentChangedNotApplied = "source_parent_changed_not_applied"
	IssueLocalParentChangedPreserved   = "local_parent_changed_preserved"
	IssueLocalBodyNotCanonical         = "local_body_not_canonical"
	IssueTargetSiblingCapacityExceeded = "target_sibling_capacity_exceeded"
	IssueTargetDepthExceeded           = "target_depth_exceeded"
	IssueMappingCapacityExceeded       = "mapping_capacity_exceeded"
	IssueAuthorFallbackToActor         = "author_fallback_to_actor"
)

// MappingBaseline is the durable per-page baseline a reimport compares against, taken from
// DOCS_ImportEntity. It is the importer's record of what it last applied, which is what makes "did the
// source change" and "did someone edit this locally" two separate questions rather than one.
type MappingBaseline struct {
	ExternalID                 string
	LocalID                    string
	LastSourceContentHash      string
	LastAppliedContentHash     string
	LastAppliedParentID        string
	LastSourceParentExternalID string
	LastSourceOrdinal          int
	// LastSourceTitle keeps the page's source title available for reports after a local rename or after
	// staged bodies have been purged, which is the only place a stale entry's title can come from.
	LastSourceTitle string
}

// LocalPageState is the current state of a mapped page in the target Space.
type LocalPageState struct {
	// Exists is false when the mapping points at a page that is no longer present at all.
	Exists bool
	// Deleted is true for a soft-deleted page: the row is there but the page is not live.
	Deleted bool
	SpaceID string
	// ParentID is "" for a Space root.
	ParentID string
	// AppliedContentHash is the hash of the page's current content, computed by the caller because it
	// needs the stored body.
	AppliedContentHash string
	// BodyIsCanonical is false when the stored body could not be canonicalized as TipTap, which makes the
	// page a definite local edit relative to any canonical baseline the importer wrote.
	BodyIsCanonical bool
}

// ClassifyInput is one staged page plus everything needed to decide what to do with it.
type ClassifyInput struct {
	// IncomingSourceContentHash is the hash of the staged page's source content.
	IncomingSourceContentHash string
	// IncomingParentExternalID is the source parent, "" for a source root.
	IncomingParentExternalID string
	IncomingSourceOrdinal    int
	// TargetSpaceID is the Space the job imports into.
	TargetSpaceID string
	// Mapping is nil when this external id has never been imported into the selected source.
	Mapping *MappingBaseline
	// Local is the current state of Mapping.LocalID; ignored when Mapping is nil.
	Local LocalPageState
	// ParentAvailable reports whether the source parent resolves to something this import can parent
	// under: a live existing mapping, or an earlier staged create with a planned id.
	ParentAvailable bool
	// MappingCapacityExceeded reports that adopting this page would push the selected source past its
	// retained-mapping cap.
	MappingCapacityExceeded bool
}

// Classification is the decision for one staged page.
type Classification struct {
	Action model.ImportAction
	// Issues are the stable codes explaining the decision, in deterministic order.
	Issues []string
	// OverwriteEligible reports whether a conflict may be approved for overwrite at confirmation. Only a
	// conflict is ever eligible.
	OverwriteEligible bool
	// LocalID is the page this decision targets, empty for a create.
	LocalID string
}

// Classify decides what a reimport does with one staged page.
//
// Content and structure are deliberately independent. A page whose source parent moved, or whose local
// parent was moved by a user, is *not* thereby a content change: V1 preserves local structure and reports
// source structural drift instead. Folding either into the content comparison would turn a safe body
// update into a conflict, or worse, silently undo a user's deliberate reorganization.
func Classify(in ClassifyInput) Classification {
	// A page whose adoption would breach the source's retained-mapping cap is blocked before anything
	// else: the cap bounds stale anti-joins and restart work, so exceeding it is not something a content
	// decision may override.
	if in.MappingCapacityExceeded {
		return Classification{Action: model.ImportActionBlocked, Issues: []string{IssueMappingCapacityExceeded}}
	}

	if in.Mapping == nil {
		if !in.ParentAvailable {
			return Classification{Action: model.ImportActionBlocked, Issues: []string{IssueParentMappingMissing}}
		}
		return Classification{Action: model.ImportActionCreate}
	}

	// A mapping whose target is gone or has moved out of the Space is blocked rather than recreated or
	// overwritten. Auto-restoring would resurrect a page a user deliberately deleted, and writing into
	// another Space would import content somewhere nobody reviewed.
	if !in.Local.Exists || in.Local.Deleted {
		return Classification{
			Action:  model.ImportActionBlocked,
			Issues:  []string{IssueMappedTargetMissing},
			LocalID: in.Mapping.LocalID,
		}
	}
	if in.TargetSpaceID != "" && in.Local.SpaceID != in.TargetSpaceID {
		return Classification{
			Action:  model.ImportActionBlocked,
			Issues:  []string{IssueMappedTargetWrongSpace},
			LocalID: in.Mapping.LocalID,
		}
	}

	sourceContentChanged := in.IncomingSourceContentHash != in.Mapping.LastSourceContentHash
	localContentChanged := in.Local.AppliedContentHash != in.Mapping.LastAppliedContentHash
	sourceParentChanged := in.IncomingParentExternalID != in.Mapping.LastSourceParentExternalID
	localParentChanged := in.Local.ParentID != in.Mapping.LastAppliedParentID
	sourceOrderChanged := in.IncomingSourceOrdinal != in.Mapping.LastSourceOrdinal

	out := Classification{LocalID: in.Mapping.LocalID}
	switch {
	case sourceContentChanged && localContentChanged:
		out.Action = model.ImportActionConflict
		out.OverwriteEligible = true
		out.Issues = append(out.Issues, IssueSourceAndLocalConflict)
	case sourceContentChanged:
		out.Action = model.ImportActionUpdate
	case localContentChanged:
		out.Action = model.ImportActionPreserveLocal
		out.Issues = append(out.Issues, IssueLocalChangesPreserved)
	default:
		out.Action = model.ImportActionNoop
	}

	// A body the importer can no longer canonicalize is reported explicitly. It already forced
	// localContentChanged through its opaque hash, so this only explains *why*.
	if !in.Local.BodyIsCanonical {
		out.Issues = append(out.Issues, IssueLocalBodyNotCanonical)
	}
	// Structural findings are appended after the content decision, in a fixed order, so the issue list is
	// deterministic for the revision digest.
	if sourceParentChanged {
		out.Issues = append(out.Issues, IssueSourceParentChangedNotApplied)
	}
	if localParentChanged {
		out.Issues = append(out.Issues, IssueLocalParentChangedPreserved)
	}
	if sourceOrderChanged {
		out.Issues = append(out.Issues, IssueSourceOrderChangedNotApplied)
	}
	return out
}

// OutcomeForPlannedAction maps a planned action onto the outcome a preflight result records. Preflight
// results carry the outcome the plan *would* produce, so a report reads the same way before and after
// execution; execution overwrites them with what actually happened.
func OutcomeForPlannedAction(action model.ImportAction) model.ImportOutcome {
	switch action {
	case model.ImportActionCreate:
		return model.ImportOutcomeCreated
	case model.ImportActionUpdate:
		return model.ImportOutcomeUpdated
	case model.ImportActionNoop:
		return model.ImportOutcomeUnchanged
	case model.ImportActionPreserveLocal:
		return model.ImportOutcomeLocalPreserved
	case model.ImportActionConflict:
		return model.ImportOutcomeConflictSkipped
	case model.ImportActionStale:
		return model.ImportOutcomeStale
	case model.ImportActionBlocked:
		return model.ImportOutcomeBlocked
	default:
		return model.ImportOutcomeBlocked
	}
}

// RequiresReimportAcknowledgement reports whether a planned action means the import touches pages that
// already exist, which the user must acknowledge before execution.
func RequiresReimportAcknowledgement(action model.ImportAction) bool {
	switch action {
	case model.ImportActionUpdate, model.ImportActionNoop, model.ImportActionPreserveLocal,
		model.ImportActionConflict, model.ImportActionStale:
		return true
	default:
		return false
	}
}
