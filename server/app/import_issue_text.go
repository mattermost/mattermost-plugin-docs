// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package app

import (
	"github.com/mattermost/mattermost-plugin-docs/server/importer"
	"github.com/mattermost/mattermost-plugin-docs/server/model"
)

// Issue text lives here rather than in i18n message files because these strings are report *content*,
// not UI chrome: they are persisted verbatim into DOCS_ImportIssue at the moment the finding is made and
// must read the same when the report is downloaded months later, on any locale. The stable code is what a
// localized client keys off to render its own wording.
//
// Every entry answers two questions a reader has: what happened, and what to do about it. An issue with
// no remediation is a dead end, so the default remediation says explicitly that no action is needed
// rather than being left blank.

// preflightIssueSeverity maps a code to how much attention it deserves. Severity drives report filtering,
// so it distinguishes "the import will not touch this page" (error) from "we kept your version" (warning)
// and "worth knowing" (info).
func preflightIssueSeverity(code string) model.ImportIssueSeverity {
	switch code {
	case importer.IssueMappedTargetMissing,
		importer.IssueMappedTargetWrongSpace,
		importer.IssueParentMappingMissing,
		importer.IssueTargetSiblingCapacityExceeded,
		importer.IssueTargetDepthExceeded,
		importer.IssueMappingCapacityExceeded,
		importer.IssueParentBlocked:
		return model.ImportSeverityError
	case importer.IssueSourceAndLocalConflict,
		importer.IssueLocalChangesPreserved,
		importer.IssueLocalBodyNotCanonical,
		importer.IssueAuthorFallbackToActor:
		return model.ImportSeverityWarning
	default:
		return model.ImportSeverityInfo
	}
}

// preflightIssueMessage is the human-facing description persisted with a finding.
func preflightIssueMessage(code string) string {
	switch code {
	case importer.IssueLocalChangesPreserved:
		return "This page was edited in Mattermost after it was last imported, and the source has not changed since. Your version is kept."
	case importer.IssueSourceAndLocalConflict:
		return "This page changed both in Confluence and in Mattermost since the last import. It is skipped unless you approve overwriting it."
	case importer.IssueMappedTargetMissing:
		return "The Mattermost page this Confluence page was imported into no longer exists."
	case importer.IssueMappedTargetWrongSpace:
		return "The Mattermost page this Confluence page was imported into has moved to a different Space."
	case importer.IssueParentMappingMissing:
		return "This page's Confluence parent is not in this bundle and has never been imported, so there is nothing to nest it under."
	case importer.IssueSourcePageStale:
		return "This page was imported previously but is no longer present in the Confluence space."
	case importer.IssueSourceOrderChangedNotApplied:
		return "This page's position among its siblings changed in Confluence. Positions are not applied to existing pages."
	case importer.IssueSourceParentChangedNotApplied:
		return "This page moved to a different parent in Confluence. Moves are not applied to existing pages."
	case importer.IssueLocalParentChangedPreserved:
		return "This page was moved in Mattermost since it was imported. Its current location is kept."
	case importer.IssueLocalBodyNotCanonical:
		return "This page's current content is not in the format the importer writes, so it is treated as locally edited."
	case importer.IssueTargetSiblingCapacityExceeded:
		return "Importing these pages would exceed the maximum number of pages allowed directly under one parent."
	case importer.IssueTargetDepthExceeded:
		return "Importing this page would nest it deeper than Mattermost allows."
	case importer.IssueParentBlocked:
		return "This page is skipped because the page it would be nested under is itself being skipped."
	case importer.IssueMappingCapacityExceeded:
		return "This import source already tracks the maximum number of pages, so no further pages can be adopted into it."
	case importer.IssueAuthorFallbackToActor:
		return "The original Confluence author could not be matched to an active Mattermost user, so the page is attributed to you."
	case importer.IssueReportTruncated:
		return "This import produced more findings than the report can store, so the remaining per-page findings are omitted. Every page still has a recorded outcome."
	default:
		return "The importer recorded a finding for this page."
	}
}

// preflightIssueRemediation is the persisted "what to do about it" text.
func preflightIssueRemediation(code string) string {
	switch code {
	case importer.IssueLocalChangesPreserved:
		return "No action needed. To replace your version with the Confluence one, edit the page in Confluence so its content changes, then import again."
	case importer.IssueSourceAndLocalConflict:
		return "Compare the two versions and, if the Confluence version should win, approve this page for overwriting during confirmation."
	case importer.IssueMappedTargetMissing:
		return "Restore the deleted page in Mattermost before importing again, or leave it deleted and this page will keep being skipped."
	case importer.IssueMappedTargetWrongSpace:
		return "Move the page back to this Space, or import into the Space it now lives in."
	case importer.IssueParentMappingMissing:
		return "Include the parent page in the export, or import the parent's space first."
	case importer.IssueSourcePageStale:
		return "No action needed. The Mattermost page is left untouched; delete it manually if it is no longer wanted."
	case importer.IssueSourceOrderChangedNotApplied,
		importer.IssueSourceParentChangedNotApplied:
		return "No action needed. Reorganize the page in Mattermost if you want to match the Confluence layout."
	case importer.IssueLocalParentChangedPreserved:
		return "No action needed. Your organization of this page is kept on every future import."
	case importer.IssueLocalBodyNotCanonical:
		return "Open and re-save the page in Mattermost to convert its content, then import again if you want the Confluence version applied."
	case importer.IssueTargetSiblingCapacityExceeded:
		return "Move some existing pages out from under that parent, or split the import into smaller bundles."
	case importer.IssueTargetDepthExceeded:
		return "Import under a shallower parent, or flatten the page hierarchy in Confluence."
	case importer.IssueParentBlocked:
		return "Resolve the finding on the parent page and import again; this page will then be created with it."
	case importer.IssueMappingCapacityExceeded:
		return "Import the remaining pages as a separate import source, so each source stays within its limit."
	case importer.IssueAuthorFallbackToActor:
		return "Create or activate a Mattermost account for that Confluence user and include the mapping in the export to attribute future imports correctly."
	case importer.IssueReportTruncated:
		return "Review the per-page outcomes directly, or split the import into smaller bundles to get a complete list of findings."
	default:
		return "No action needed."
	}
}
