// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"net/http"

	"github.com/gorilla/mux"
)

// handleGetPageActiveEditors handles
// GET /api/v1/spaces/{space_id}/pages/{page_id}/active-editors
// Returns the active-editors snapshot for the page (active_editors, as_of, active_timeout_ms).
func (p *Plugin) handleGetPageActiveEditors(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	spaceID := vars["space_id"]
	pageID := vars["page_id"]
	userID := userIDFromRequest(r)

	if _, ok := p.requireSpaceMembership(w, spaceID, userID, false); !ok {
		return
	}

	snapshot, appErr := p.service.GetPageActiveEditors(pageID, spaceID)
	if appErr != nil {
		p.writeAppError(w, appErr)
		return
	}

	writeJSON(w, http.StatusOK, snapshot)
}
