// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package app

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-docs/server/importer"
)

// TestImportFailureAppError_UnwrapsToTheSpecificCode pins the status mapping for content failures.
// Inspection reports every rejected page as page_content_invalid, which cannot distinguish an over-limit
// document from a malformed one — so when the specific TipTap code was discarded, every entry in
// importContentLimitCodes became unreachable and an oversized bundle was reported as a client error.
func TestImportFailureAppError_UnwrapsToTheSpecificCode(t *testing.T) {
	cases := map[string]struct {
		cause      error
		wantCode   string
		wantStatus int
	}{
		"content over a size limit is not processable": {
			cause:      &importer.TipTapError{Code: importer.TipTapErrTooManyNodes, Message: "too many nodes"},
			wantCode:   importer.TipTapErrTooManyNodes,
			wantStatus: http.StatusUnprocessableEntity,
		},
		"content nested too deeply is not processable": {
			cause:      &importer.TipTapError{Code: importer.TipTapErrTooDeep, Message: "too deep"},
			wantCode:   importer.TipTapErrTooDeep,
			wantStatus: http.StatusUnprocessableEntity,
		},
		"content the sanitizer refuses is a client error": {
			cause:      &importer.TipTapError{Code: importer.TipTapErrSanitizerRejected, Message: "rejected"},
			wantCode:   importer.TipTapErrSanitizerRejected,
			wantStatus: http.StatusBadRequest,
		},
		"malformed content is a client error": {
			cause:      &importer.TipTapError{Code: importer.TipTapErrNotDoc, Message: "not a doc"},
			wantCode:   importer.TipTapErrNotDoc,
			wantStatus: http.StatusBadRequest,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			// Wrapped exactly as handlePageLine wraps it, so the test exercises the real shape.
			wrapped := &importer.InspectError{
				Code:    importer.InspectErrTipTap,
				Message: "line 3: page \"100\" content invalid",
				Cause:   tc.cause,
			}
			require.Equal(t, tc.wantCode, importFailureCode(wrapped))
			appErr := importFailureAppError("test", wrapped)
			require.Equal(t, tc.wantStatus, appErr.StatusCode)
		})
	}
}

// TestImportFailureAppError_UnwrappedInspectErrorsKeepTheirCode confirms the reordering did not shadow
// inspection codes that have no cause: they still drive the status on their own.
func TestImportFailureAppError_UnwrappedInspectErrorsKeepTheirCode(t *testing.T) {
	depth := &importer.InspectError{Code: importer.InspectErrDepthExceeded, Message: "too deep"}
	require.Equal(t, importer.InspectErrDepthExceeded, importFailureCode(depth))
	require.Equal(t, http.StatusUnprocessableEntity, importFailureAppError("test", depth).StatusCode)

	parent := &importer.InspectError{Code: importer.InspectErrParentNotSeen, Message: "missing parent"}
	require.Equal(t, http.StatusBadRequest, importFailureAppError("test", parent).StatusCode)

	// An archive that is simply too large keeps its own 413 rather than being reclassified.
	tooBig := &importer.ArchiveError{Code: importer.ArchiveErrTooManyEntries, Message: "too many entries"}
	require.Equal(t, http.StatusRequestEntityTooLarge, importFailureAppError("test", tooBig).StatusCode)
}
