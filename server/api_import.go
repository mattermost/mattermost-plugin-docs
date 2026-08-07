// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"os"

	"github.com/gorilla/mux"
	mmmodel "github.com/mattermost/mattermost/server/public/model"

	"github.com/mattermost/mattermost-plugin-docs/server/importer"
	"github.com/mattermost/mattermost-plugin-docs/server/model"
)

// Multipart part names accepted by the upload endpoint. Any other part — or a repeat of either —
// is rejected rather than ignored, so a client cannot smuggle a second archive past inspection.
const (
	importPartRequest = "request"
	importPartBundle  = "bundle"
)

// abandonPart discards a rejected multipart part *without reading it*.
//
// multipart.Part.Close drains whatever is unread (io.Copy to io.Discard), so calling it on a
// mis-ordered or unexpected part would make the server consume the whole body — potentially a 250 MiB
// archive — before the request is refused, and before either authorization or the inspection
// semaphore has been consulted. Returning without reading leaves the unread body to net/http, which
// closes the connection rather than draining an oversized one.
func abandonPart(_ *multipart.Part) {}

// handleCreateImport handles POST /api/v1/imports/preflight.
//
// The multipart parts are required in order — `request` first, `bundle` second — so the small JSON
// request can be parsed and *authorized* before a single byte of a possibly 250 MiB archive is
// accepted. The bundle is then streamed to a private temporary file, inspected, and staged in one
// transaction. The temporary file exists only for this request: everything the worker later needs is
// in PostgreSQL, so no subsequent step depends on this node.
func (p *Plugin) handleCreateImport(w http.ResponseWriter, r *http.Request) {
	actorID := userIDFromRequest(r)

	// Cap the whole request body before reading any part, so an oversized upload is rejected without
	// being written to disk.
	r.Body = http.MaxBytesReader(w, r.Body, importer.MaxBundleUploadBytes+importer.MaxMultipartOverheadBytes)

	// ParseMultipartForm is deliberately avoided: it would buffer/spool every part itself, including
	// the archive, before the handler can apply its own limits or authorize the request.
	reader, err := r.MultipartReader()
	if err != nil {
		p.writeAppError(w, mmmodel.NewAppError("handleCreateImport", "api.import.invalid_multipart.app_error", nil, "", http.StatusBadRequest).Wrap(err))
		return
	}

	// Part 1 must be the JSON request.
	requestPart, err := reader.NextPart()
	if err != nil {
		p.writeAppError(w, multipartReadError("handleCreateImport", err))
		return
	}
	if requestPart.FormName() != importPartRequest {
		abandonPart(requestPart)
		p.writeAppError(w, mmmodel.NewAppError("handleCreateImport", "api.import.request_part_not_first.app_error", nil, "", http.StatusBadRequest))
		return
	}
	uploadRequest, appErr := decodeImportRequestPart(requestPart)
	// Deliberately not Close(): the decoder reads only MaxRequestPartBytes+1 bytes, so an oversized part is
	// intentionally left unread — and Close would drain the remainder, letting a caller push most of the
	// 250 MiB body limit through this part before authorization has even run. See abandonPart.
	abandonPart(requestPart)
	if appErr != nil {
		p.writeAppError(w, appErr)
		return
	}

	// Authorize before accepting the bundle body. An unauthorized caller never gets to spend our disk
	// or parser budget.
	target, appErr := p.service.AuthorizeImportTarget(actorID, uploadRequest.Target)
	if appErr != nil {
		p.writeAppError(w, appErr)
		return
	}

	// Only now claim the single inspection slot, still before reading bundle bytes, so concurrent
	// uploads cannot pile temporary files onto disk.
	release, ok := p.acquireInspectionSlot(w)
	if !ok {
		return
	}
	defer release()

	// Part 2 must be the bundle.
	bundlePart, err := reader.NextPart()
	if err != nil {
		p.writeAppError(w, multipartReadError("handleCreateImport", err))
		return
	}
	if bundlePart.FormName() != importPartBundle {
		abandonPart(bundlePart)
		p.writeAppError(w, mmmodel.NewAppError("handleCreateImport", "api.import.bundle_part_not_second.app_error", nil, "", http.StatusBadRequest))
		return
	}
	file, size, sum, cleanup, bundleErr := writeBundleToTempFile(bundlePart)
	// Deliberately not Close(): multipart.Part.Close drains the unread remainder, which on a rejection
	// (oversized archive, out-of-space temp file) means reading the rest of a possibly 250 MiB body the
	// server has already decided to refuse. See abandonPart.
	abandonPart(bundlePart)
	if cleanup != nil {
		// Remove the temp file and its directory on every return path, including success: the bundle
		// is not needed after inspection.
		defer cleanup()
	}
	if bundleErr != nil {
		p.writeAppError(w, bundleErr)
		return
	}

	// Reject any further parts rather than ignoring them, so a second archive cannot ride along.
	if extra, extraErr := reader.NextPart(); extraErr == nil {
		abandonPart(extra)
		p.writeAppError(w, mmmodel.NewAppError("handleCreateImport", "api.import.unknown_part.app_error", nil, "", http.StatusBadRequest))
		return
	} else if !errors.Is(extraErr, io.EOF) {
		p.writeAppError(w, multipartReadError("handleCreateImport", extraErr))
		return
	}

	view, appErr := p.service.CreateImportFromBundle(actorID, target, file, size, sum)
	if appErr != nil {
		// Admission exhaustion surfaces from the staging transaction as a 429; give it the same
		// Retry-After hint the semaphore path sends, so a client never has to guess for one of them.
		if appErr.StatusCode == http.StatusTooManyRequests {
			w.Header().Set("Retry-After", importRetryAfterSeconds)
		}
		p.writeAppError(w, appErr)
		return
	}
	writeJSON(w, http.StatusCreated, view)
}

// acquireInspectionSlot takes the process-wide single inspection slot and registers the in-flight
// inspection so deactivation can wait for it. It returns a release func and false when the request
// must be rejected, having already written the response.
//
// The closed-check and the WaitGroup Add happen under one mutex: doing them separately would race a
// concurrent deactivation between the check and the Add, letting a new inspection start after the
// store was closed.
func (p *Plugin) acquireInspectionSlot(w http.ResponseWriter) (func(), bool) {
	p.inspectionMu.Lock()
	if p.inspectionClosed {
		p.inspectionMu.Unlock()
		p.writeAppError(w, mmmodel.NewAppError("acquireInspectionSlot", "api.import.shutting_down.app_error", nil, "", http.StatusServiceUnavailable))
		return nil, false
	}
	p.inspectionWG.Add(1)
	p.inspectionMu.Unlock()

	select {
	case p.inspectionSemaphore <- struct{}{}:
		return func() {
			<-p.inspectionSemaphore
			p.inspectionWG.Done()
		}, true
	default:
		// Another inspection holds the only slot. This is a capacity condition, not a client error, so
		// the caller is told when to retry rather than being told the request was wrong.
		p.inspectionWG.Done()
		w.Header().Set("Retry-After", importRetryAfterSeconds)
		p.writeAppError(w, mmmodel.NewAppError("acquireInspectionSlot", "api.import.inspection_busy.app_error", nil, "", http.StatusTooManyRequests))
		return nil, false
	}
}

// importRetryAfterSeconds is the Retry-After hint sent with a 429. One inspection runs at a time and
// a large bundle takes tens of seconds, so a short retry keeps clients from hammering the endpoint.
const importRetryAfterSeconds = "30"

// multipartReadError maps a multipart read failure onto its status: a body that trips MaxBytesReader
// is too large, anything else is malformed.
func multipartReadError(where string, err error) *mmmodel.AppError {
	if isMaxBytesError(err) {
		return mmmodel.NewAppError(where, "api.import.upload_too_large.app_error", nil, "", http.StatusRequestEntityTooLarge).Wrap(err)
	}
	if errors.Is(err, io.EOF) {
		return mmmodel.NewAppError(where, "api.import.missing_request_part.app_error", nil, "", http.StatusBadRequest).Wrap(err)
	}
	return mmmodel.NewAppError(where, "api.import.invalid_multipart.app_error", nil, "", http.StatusBadRequest).Wrap(err)
}

// isMaxBytesError reports whether err is the error http.MaxBytesReader returns when the request body
// exceeds its limit, including when it surfaces wrapped by the multipart reader.
func isMaxBytesError(err error) bool {
	var maxBytesErr *http.MaxBytesError
	return errors.As(err, &maxBytesErr)
}

// handleCancelImport handles POST /api/v1/imports/{job_id}/cancel. Cancelling releases the admission
// capacity the job held, so it is the user's way out of a stuck or unwanted import.
func (p *Plugin) handleCancelImport(w http.ResponseWriter, r *http.Request) {
	actorID := userIDFromRequest(r)
	jobID := mux.Vars(r)["job_id"]
	view, appErr := p.service.CancelImportJob(jobID, actorID)
	if appErr != nil {
		p.writeAppError(w, appErr)
		return
	}
	// 202: the job is durably canceled, but any already-committed work is deliberately left in place
	// rather than rolled back.
	writeJSON(w, http.StatusAccepted, view)
}

// handleGetImport handles GET /api/v1/imports/{job_id}.
func (p *Plugin) handleGetImport(w http.ResponseWriter, r *http.Request) {
	actorID := userIDFromRequest(r)
	jobID := mux.Vars(r)["job_id"]
	view, appErr := p.service.GetImportJob(jobID, actorID)
	if appErr != nil {
		p.writeAppError(w, appErr)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// handleListImports handles GET /api/v1/imports?team_id={id}. Only the caller's own jobs are listed.
func (p *Plugin) handleListImports(w http.ResponseWriter, r *http.Request) {
	actorID := userIDFromRequest(r)
	teamID := r.URL.Query().Get("team_id")
	page, perPage := pageParam(r), perPageParam(r)
	views, hasMore, appErr := p.service.GetImportJobsForActor(actorID, teamID, page, perPage)
	if appErr != nil {
		p.writeAppError(w, appErr)
		return
	}
	writePaginatedJSON(w, views, page, perPage, hasMore)
}

// handleGetImportIssues handles GET /api/v1/imports/{job_id}/issues?stage=&severity=.
func (p *Plugin) handleGetImportIssues(w http.ResponseWriter, r *http.Request) {
	actorID := userIDFromRequest(r)
	jobID := mux.Vars(r)["job_id"]
	stage := r.URL.Query().Get("stage")
	severity := r.URL.Query().Get("severity")
	page, perPage := pageParam(r), perPageParam(r)
	issues, hasMore, appErr := p.service.GetImportIssues(jobID, actorID, stage, severity, page, perPage)
	if appErr != nil {
		p.writeAppError(w, appErr)
		return
	}
	writePaginatedJSON(w, issues, page, perPage, hasMore)
}

// handleGetImportPreflightResults handles GET /api/v1/imports/{job_id}/preflight-results.
func (p *Plugin) handleGetImportPreflightResults(w http.ResponseWriter, r *http.Request) {
	actorID := userIDFromRequest(r)
	jobID := mux.Vars(r)["job_id"]
	page, perPage := pageParam(r), perPageParam(r)
	results, hasMore, appErr := p.service.GetImportPreflightResults(jobID, actorID, page, perPage)
	if appErr != nil {
		p.writeAppError(w, appErr)
		return
	}
	writePaginatedJSON(w, results, page, perPage, hasMore)
}

// handleSelectImportSource handles POST /api/v1/imports/{job_id}/source.
func (p *Plugin) handleSelectImportSource(w http.ResponseWriter, r *http.Request) {
	actorID := userIDFromRequest(r)
	jobID := mux.Vars(r)["job_id"]

	req, appErr := decodeImportJSONBody[model.ImportSourceSelectionRequest](r, importSourceBodyMaxBytes)
	if appErr != nil {
		p.writeAppError(w, appErr)
		return
	}
	view, appErr := p.service.SelectImportSource(jobID, actorID, *req)
	if appErr != nil {
		p.writeAppError(w, appErr)
		return
	}
	// 202: the choice is durable and the job is queued, but preflight has not run yet.
	writeJSON(w, http.StatusAccepted, view)
}

// handleConfirmImport handles POST /api/v1/imports/{job_id}/confirm.
func (p *Plugin) handleConfirmImport(w http.ResponseWriter, r *http.Request) {
	actorID := userIDFromRequest(r)
	jobID := mux.Vars(r)["job_id"]

	req, appErr := decodeImportJSONBody[model.ImportConfirmRequest](r, model.ImportConfirmationMaxBytes)
	if appErr != nil {
		p.writeAppError(w, appErr)
		return
	}
	view, appErr := p.service.ConfirmImportJob(jobID, actorID, *req)
	if appErr != nil {
		p.writeAppError(w, appErr)
		return
	}
	// 202: the import is queued for the worker, not performed inline.
	writeJSON(w, http.StatusAccepted, view)
}

// importSourceBodyMaxBytes bounds the source-selection body. It carries at most a mode, an id, and a
// display name, so anything larger is malformed rather than merely big.
const importSourceBodyMaxBytes = 8 * 1024

// decodeImportJSONBody decodes a JSON request body under an explicit size cap, rejecting a body that
// exceeds it with 413 and trailing data after the object with 400.
//
// The cap is applied by reading one byte past it rather than trusting Content-Length, which a client
// controls independently of what it actually sends.
func decodeImportJSONBody[T any](r *http.Request, maxBytes int64) (*T, *mmmodel.AppError) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxBytes+1))
	if err != nil {
		return nil, mmmodel.NewAppError("decodeImportJSONBody", "api.import.invalid_body.app_error", nil, "", http.StatusBadRequest).Wrap(err)
	}
	if int64(len(raw)) > maxBytes {
		return nil, mmmodel.NewAppError("decodeImportJSONBody", "api.import.body_too_large.app_error",
			map[string]any{"MaxBytes": maxBytes}, "", http.StatusRequestEntityTooLarge)
	}

	var body T
	dec := json.NewDecoder(bytes.NewReader(raw))
	if decErr := dec.Decode(&body); decErr != nil {
		return nil, mmmodel.NewAppError("decodeImportJSONBody", "api.invalid_json.app_error", nil, "", http.StatusBadRequest).Wrap(decErr)
	}
	// Reject a second concatenated value, for the same reason the multipart request part does: More()
	// reports false before a closing delimiter, so the only reliable check is decoding again for EOF.
	if decErr := dec.Decode(&struct{}{}); !errors.Is(decErr, io.EOF) {
		return nil, mmmodel.NewAppError("decodeImportJSONBody", "api.invalid_json.app_error", nil, "", http.StatusBadRequest)
	}
	return &body, nil
}

// decodeImportRequestPart decodes the JSON `request` part under its own size cap, rejecting trailing
// data after the object.
func decodeImportRequestPart(part *multipart.Part) (*model.ImportUploadRequest, *mmmodel.AppError) {
	raw, err := io.ReadAll(io.LimitReader(part, importer.MaxRequestPartBytes+1))
	if err != nil {
		return nil, mmmodel.NewAppError("decodeImportRequestPart", "api.import.invalid_multipart.app_error", nil, "", http.StatusBadRequest).Wrap(err)
	}
	if len(raw) > importer.MaxRequestPartBytes {
		return nil, mmmodel.NewAppError("decodeImportRequestPart", "api.import.request_part_too_large.app_error",
			map[string]any{"MaxBytes": importer.MaxRequestPartBytes}, "", http.StatusRequestEntityTooLarge)
	}

	var req model.ImportUploadRequest
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := dec.Decode(&req); err != nil {
		return nil, mmmodel.NewAppError("decodeImportRequestPart", "api.invalid_json.app_error", nil, "", http.StatusBadRequest).Wrap(err)
	}
	// Reject a second concatenated value. json.Decoder.More() is not sufficient (it reports false
	// before a closing delimiter), so decode again and require EOF.
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, mmmodel.NewAppError("decodeImportRequestPart", "api.invalid_json.app_error", nil, "", http.StatusBadRequest)
	}
	return &req, nil
}

// faultRecordingWriter remembers the first error its destination returned, so a caller of io.Copy can
// tell a write-side failure from a read-side one and classify it accordingly.
type faultRecordingWriter struct {
	w   io.Writer
	err error
}

func (f *faultRecordingWriter) Write(p []byte) (int, error) {
	n, err := f.w.Write(p)
	if err != nil && f.err == nil {
		f.err = err
	}
	return n, err
}

// writeBundleToTempFile streams the archive part to a 0600 file inside a freshly created 0700
// directory, computing its SHA-256 while writing and enforcing the compressed-size cap. The returned
// file is rewound and ready to be read as an io.ReaderAt; the returned cleanup removes both the file
// and its directory.
func writeBundleToTempFile(part *multipart.Part) (_ *os.File, _ int64, _ string, cleanup func(), _ *mmmodel.AppError) {
	// MkdirTemp creates the directory with 0700 and a random name, so no other user can reach the
	// archive and no predictable path can be pre-created by an attacker.
	dir, err := os.MkdirTemp("", "docs-import-*")
	if err != nil {
		return nil, 0, "", nil, mmmodel.NewAppError("writeBundleToTempFile", "api.import.temp_storage_failed.app_error", nil, "", http.StatusInternalServerError).Wrap(err)
	}
	cleanup = func() { _ = os.RemoveAll(dir) }

	// CreateTemp creates the file with 0600 and a random name inside that directory.
	file, err := os.CreateTemp(dir, "bundle-*.zip")
	if err != nil {
		return nil, 0, "", cleanup, mmmodel.NewAppError("writeBundleToTempFile", "api.import.temp_storage_failed.app_error", nil, "", http.StatusInternalServerError).Wrap(err)
	}
	// The file is unlinked by cleanup; closing it is part of that same teardown.
	prevCleanup := cleanup
	cleanup = func() {
		_ = file.Close()
		prevCleanup()
	}

	hasher := sha256.New()
	// io.Copy reports one error for both sides of the transfer, so the destination is wrapped to record
	// its own failures. Without that, a full or failing disk is indistinguishable from a truncated upload
	// and gets blamed on the client as a 400 — an operational fault reported as a client error.
	sink := &faultRecordingWriter{w: io.MultiWriter(file, hasher)}
	// Read one byte past the cap so an exactly-oversized upload is still detected.
	written, err := io.Copy(sink, io.LimitReader(part, importer.MaxBundleUploadBytes+1))
	if err != nil {
		switch {
		case sink.err != nil:
			return nil, 0, "", cleanup, mmmodel.NewAppError("writeBundleToTempFile", "api.import.temp_storage_failed.app_error", nil, "", http.StatusInternalServerError).Wrap(sink.err)
		case isMaxBytesError(err):
			return nil, 0, "", cleanup, mmmodel.NewAppError("writeBundleToTempFile", "api.import.upload_too_large.app_error", nil, "", http.StatusRequestEntityTooLarge).Wrap(err)
		default:
			return nil, 0, "", cleanup, mmmodel.NewAppError("writeBundleToTempFile", "api.import.upload_read_failed.app_error", nil, "", http.StatusBadRequest).Wrap(err)
		}
	}
	if written > importer.MaxBundleUploadBytes {
		return nil, 0, "", cleanup, mmmodel.NewAppError("writeBundleToTempFile", "api.import.upload_too_large.app_error",
			map[string]any{"MaxBytes": importer.MaxBundleUploadBytes}, "", http.StatusRequestEntityTooLarge)
	}
	if written == 0 {
		return nil, 0, "", cleanup, mmmodel.NewAppError("writeBundleToTempFile", "api.import.empty_bundle.app_error", nil, "", http.StatusBadRequest)
	}

	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, 0, "", cleanup, mmmodel.NewAppError("writeBundleToTempFile", "api.import.temp_storage_failed.app_error", nil, "", http.StatusInternalServerError).Wrap(err)
	}
	return file, written, hex.EncodeToString(hasher.Sum(nil)), cleanup, nil
}
