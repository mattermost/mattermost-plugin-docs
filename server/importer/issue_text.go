// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package importer

import (
	"github.com/mattermost/mattermost-plugin-docs/server/model"
)

// Issue text lives here rather than in i18n message files because these strings are report *content*, not UI
// chrome: they are persisted verbatim into DOCS_ImportIssue at the moment the finding is made and must read
// the same when the report is downloaded months later, on any locale. The stable code is what a localized
// client keys off to render its own wording.
//
// It sits in this package rather than in app because both preflight and execution write issue rows, and
// execution writes them from inside the store's page transaction. One definition means a finding cannot be
// worded one way at review time and another way at execution.
//
// Every entry answers two questions a reader has: what happened, and what to do about it. An issue with no
// remediation is a dead end, so the default remediation says explicitly that no action is needed rather than
// being left blank.

// IssueSeverity maps a code to how much attention it deserves. Severity drives report filtering and the
// completed/completed_with_issues distinction, so it separates "the import will not touch this page" (error)
// from "we kept your version" (warning) and "worth knowing" (info).
func IssueSeverity(code string) model.ImportIssueSeverity {
	switch code {
	case IssueMappedTargetMissing,
		IssueMappedTargetWrongSpace,
		IssueParentMappingMissing,
		IssueTargetSiblingCapacityExceeded,
		IssueTargetDepthExceeded,
		IssueMappingCapacityExceeded,
		IssueParentBlocked,
		IssueParentNotAvailableAfterImport,
		IssueConflictChangedAfterConfirmation,
		IssueChannelCompensationFailed,
		IssueMissingPageOutcome,
		IssueSkippedByReviewedPlan:
		return model.ImportSeverityError
	case IssueSourceAndLocalConflict,
		IssueLocalChangesPreserved,
		IssueLocalBodyNotCanonical,
		IssueAuthorFallbackToActor,
		IssueNotAttemptedCanceled,
		IssueNotAttemptedFailed,
		IssueReportTruncated:
		return model.ImportSeverityWarning
	default:
		return model.ImportSeverityInfo
	}
}

// IssueMessage is the human-facing description persisted with a finding.
func IssueMessage(code string) string {
	switch code {
	case IssueLocalChangesPreserved:
		return "This page was edited in Mattermost after it was last imported, and the source has not changed since. Your version is kept."
	case IssueSourceAndLocalConflict:
		return "This page changed both in Confluence and in Mattermost since the last import. It is skipped unless you approve overwriting it."
	case IssueMappedTargetMissing:
		return "The Mattermost page this Confluence page was imported into no longer exists."
	case IssueMappedTargetWrongSpace:
		return "The Mattermost page this Confluence page was imported into has moved to a different Space."
	case IssueParentMappingMissing:
		return "This page's Confluence parent is not in this bundle and has never been imported, so there is nothing to nest it under."
	case IssueSourcePageStale:
		return "This page was imported previously but is no longer present in the Confluence space."
	case IssueSourceOrderChangedNotApplied:
		return "This page's position among its siblings changed in Confluence. Positions are not applied to existing pages."
	case IssueSourceParentChangedNotApplied:
		return "This page moved to a different parent in Confluence. Moves are not applied to existing pages."
	case IssueLocalParentChangedPreserved:
		return "This page was moved in Mattermost since it was imported. Its current location is kept."
	case IssueLocalBodyNotCanonical:
		return "This page's current content is not in the format the importer writes, so it is treated as locally edited."
	case IssueTargetSiblingCapacityExceeded:
		return "Importing these pages would exceed the maximum number of pages allowed directly under one parent."
	case IssueTargetDepthExceeded:
		return "Importing this page would nest it deeper than Mattermost allows."
	case IssueParentBlocked:
		return "This page is skipped because the page it would be nested under is itself being skipped."
	case IssueMappingCapacityExceeded:
		return "This import source already tracks the maximum number of pages, so no further pages can be adopted into it."
	case IssueAuthorFallbackToActor:
		return "The original Confluence author could not be matched to an active Mattermost user, so the page is attributed to you."
	case IssueReportTruncated:
		return "This import produced more findings than the report can store, so the remaining per-page findings are omitted. Every page still has a recorded outcome."
	case IssueConflictChangedAfterConfirmation:
		return "You approved overwriting this page, but it changed again between your approval and the import, so it was left alone."
	case IssueParentNotAvailableAfterImport:
		return "This page was skipped because the page it would be nested under was not created by this import."
	case IssueSourceCreateAtInvalid:
		return "This page's Confluence creation date was missing or in the future, so the import's own time was used instead."
	case IssueSourceUpdateAtInvalid:
		return "This page's Confluence modification date was missing or in the future. The original value is still recorded on the page."
	case IssueNotAttemptedCanceled:
		return "This import was canceled before every page was processed. Pages already imported are kept; the rest were not attempted."
	case IssueNotAttemptedFailed:
		return "This import stopped before every page was processed. Pages already imported are kept; the rest were not attempted."
	case IssueChannelCompensated:
		return "A channel created for the new Space was removed again, because the Space itself was never created."
	case IssueChannelCompensationFailed:
		return "A channel was created for a Space this import never finished setting up, and it could not be removed automatically."
	case IssueMissingPageOutcome:
		return "This import reported completion while a page had no recorded outcome, so it is reported as failed instead."
	case IssueSkippedByReviewedPlan:
		return "The plan you reviewed said this page would be skipped, so it was left alone even though it could now be imported."
	default:
		return "The importer recorded a finding for this page."
	}
}

// IssueRemediation is the persisted "what to do about it" text.
func IssueRemediation(code string) string {
	switch code {
	case IssueLocalChangesPreserved:
		return "No action needed. To replace your version with the Confluence one, edit the page in Confluence so its content changes, then import again."
	case IssueSourceAndLocalConflict:
		return "Compare the two versions and, if the Confluence version should win, approve this page for overwriting during confirmation."
	case IssueMappedTargetMissing:
		return "Restore the deleted page in Mattermost before importing again, or leave it deleted and this page will keep being skipped."
	case IssueMappedTargetWrongSpace:
		return "Move the page back to this Space, or import into the Space it now lives in."
	case IssueParentMappingMissing:
		return "Include the parent page in the export, or import the parent's space first."
	case IssueSourcePageStale:
		return "No action needed. The Mattermost page is left untouched; delete it manually if it is no longer wanted."
	case IssueSourceOrderChangedNotApplied,
		IssueSourceParentChangedNotApplied:
		return "No action needed. Reorganize the page in Mattermost if you want to match the Confluence layout."
	case IssueLocalParentChangedPreserved:
		return "No action needed. Your organization of this page is kept on every future import."
	case IssueLocalBodyNotCanonical:
		return "Open and re-save the page in Mattermost to convert its content, then import again if you want the Confluence version applied."
	case IssueTargetSiblingCapacityExceeded:
		return "Move some existing pages out from under that parent, or split the import into smaller bundles."
	case IssueTargetDepthExceeded:
		return "Import under a shallower parent, or flatten the page hierarchy in Confluence."
	case IssueParentBlocked:
		return "Resolve the finding on the parent page and import again; this page will then be created with it."
	case IssueMappingCapacityExceeded:
		return "Import the remaining pages as a separate import source, so each source stays within its limit."
	case IssueAuthorFallbackToActor:
		return "Create or activate a Mattermost account for that Confluence user and include the mapping in the export to attribute future imports correctly."
	case IssueReportTruncated:
		return "Review the per-page outcomes directly, or split the import into smaller bundles to get a complete list of findings."
	case IssueConflictChangedAfterConfirmation:
		return "Review the page again and, if the Confluence version should still win, import again and approve it."
	case IssueParentNotAvailableAfterImport:
		return "Resolve the finding on the parent page and import again; this page will then be created with it."
	case IssueSourceCreateAtInvalid,
		IssueSourceUpdateAtInvalid:
		return "No action needed. Correct the date in Confluence and import again if the original timestamp matters."
	case IssueNotAttemptedCanceled,
		IssueNotAttemptedFailed:
		return "Import the same bundle again to continue. Pages that were already imported are recognized and not duplicated."
	case IssueChannelCompensated:
		return "No action needed. Import the bundle again to retry creating the Space."
	case IssueChannelCompensationFailed:
		return "Ask a system administrator to archive the channel named in this finding's details."
	case IssueMissingPageOutcome:
		return "Import the same bundle again; pages that were already imported are recognized and not duplicated."
	case IssueSkippedByReviewedPlan:
		return "Import the same bundle again to have this page reviewed against the current state of the Space."
	default:
		return "No action needed."
	}
}
