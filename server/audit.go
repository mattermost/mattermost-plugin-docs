// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"net/http"

	"github.com/gorilla/mux"
	mmmodel "github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
)

// Audit event names for the comment mutation routes, following core's camelCase convention.
const (
	auditEventCreatePageComment      = "createPageComment"
	auditEventCreatePageCommentReply = "createPageCommentReply"
	auditEventUpdatePageComment      = "updatePageComment"
	auditEventDeletePageComment      = "deletePageComment"
)

// auditObjectTypePageComment names the entity every comment mutation route acts on.
const auditObjectTypePageComment = "page_comment"

// makeAuditRecord builds the record a mutating handler emits through the server audit pipeline.
// The status starts as fail and stays there through every early return; the handler flips it to
// success only after its write lands. The route's path ids are copied into the event parameters,
// so a refused attempt still records which resources it named. Every route that reaches for this
// builder mutates a page comment, so the object type belongs here rather than on each success
// branch: it then also reaches the records recordAuditOutcome settles, which have no success
// branch to hang it on.
func (p *Plugin) makeAuditRecord(r *http.Request, eventName, userID string) *mmmodel.AuditRecord {
	rec := plugin.MakeAuditRecord(eventName, mmmodel.AuditStatusFail)
	rec.AddEventObjectType(auditObjectTypePageComment)
	rec.Actor.UserId = userID
	rec.Actor.Client = r.UserAgent()
	rec.Actor.IpAddress = r.RemoteAddr
	rec.Actor.XForwardedFor = r.Header.Get("X-Forwarded-For")
	rec.AddMeta(mmmodel.AuditKeyAPIPath, r.URL.Path)
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
