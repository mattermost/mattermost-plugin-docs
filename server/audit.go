// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"net/http"

	"github.com/gorilla/mux"
	mmmodel "github.com/mattermost/mattermost/server/public/model"
)

// Audit event names for the plugin's mutating routes, following core's camelCase convention
// (model.AuditEventCreateChannel and friends). Read routes are not audited.
const (
	auditEventCreateSpace       = "createSpace"
	auditEventUpdateSpace       = "updateSpace"
	auditEventDeleteSpace       = "deleteSpace"
	auditEventRestoreSpace      = "restoreSpace"
	auditEventAddSpaceMember    = "addSpaceMember"
	auditEventRemoveSpaceMember = "removeSpaceMember"

	auditEventCreatePage      = "createPage"
	auditEventUpdatePage      = "updatePage"
	auditEventDeletePage      = "deletePage"
	auditEventRestorePage     = "restorePage"
	auditEventMovePage        = "movePage"
	auditEventMovePageToSpace = "movePageToSpace"
	auditEventDuplicatePage   = "duplicatePage"

	// The draft autosave heartbeat (PATCH .../draft) is deliberately not audited: it fires
	// continuously while a user types, and core's own draft upsert route emits no audit record
	// either. The draft lifecycle boundaries below are.
	auditEventCreateSpaceDraft = "createSpaceDraft"
	auditEventDeletePageDraft  = "deletePageDraft"
	auditEventPublishPageDraft = "publishPageDraft"

	auditEventCreatePageComment      = "createPageComment"
	auditEventCreatePageCommentReply = "createPageCommentReply"
	auditEventUpdatePageComment      = "updatePageComment"
	auditEventDeletePageComment      = "deletePageComment"
)

// makeAuditRecord builds the record a mutating handler emits through the server audit pipeline.
// The status starts as fail and stays there through every early return; the handler flips it to
// success only after its write lands. The route's path ids are copied into the event parameters,
// so a refused attempt still records which resources it named.
func (p *Plugin) makeAuditRecord(r *http.Request, eventName, userID string) *mmmodel.AuditRecord {
	rec := &mmmodel.AuditRecord{
		EventName: eventName,
		Status:    mmmodel.AuditStatusFail,
		Actor: mmmodel.AuditEventActor{
			UserId:        userID,
			Client:        r.UserAgent(),
			IpAddress:     r.RemoteAddr,
			XForwardedFor: r.Header.Get("X-Forwarded-For"),
		},
		Meta: map[string]any{mmmodel.AuditKeyAPIPath: r.URL.Path},
		EventData: mmmodel.AuditEventData{
			Parameters:  map[string]any{},
			PriorState:  map[string]any{},
			ResultState: map[string]any{},
		},
	}
	for key, value := range mux.Vars(r) {
		mmmodel.AddEventParameterToAuditRec(rec, key, value)
	}
	return rec
}

// recordAuditOutcome settles the audit status of a write that returned an error. A comment write
// through the plugin API can fail after its row has committed, and the service reports that as its
// committed return; the audit trail records the durable state change that happened rather than the
// error the caller saw, so a created, edited, or deleted comment is never absent from the log.
// Leaves the record at fail when nothing was written.
func (p *Plugin) recordAuditOutcome(rec *mmmodel.AuditRecord, committed bool) {
	if committed {
		rec.Success()
	}
}
