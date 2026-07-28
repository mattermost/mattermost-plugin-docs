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

// handleCreateImport handles POST /api/v1/imports/preflight.
//
// The upload is streamed to a private temporary file (never buffered in memory and never held in the
// database), inspected synchronously, and — on success — persisted as a job plus its normalized
// staged pages. The temporary file exists only for the duration of this request: everything the
// worker later needs is in PostgreSQL, so no subsequent step depends on this node.
func (p *Plugin) handleCreateImport(w http.ResponseWriter, r *http.Request) {
	actorID := userIDFromRequest(r)

	// Cap the whole request body before reading any part, so an oversized upload is rejected without
	// being written to disk.
	r.Body = http.MaxBytesReader(w, r.Body, importer.MaxBundleUploadBytes+importer.MaxMultipartOverheadBytes)

	// ParseMultipartForm is deliberately avoided: it would buffer/spool every part itself, including
	// a 250 MiB archive, before the handler can apply its own limits.
	reader, err := r.MultipartReader()
	if err != nil {
		p.writeAppError(w, mmmodel.NewAppError("handleCreateImport", "api.import.invalid_multipart.app_error", nil, "", http.StatusBadRequest).Wrap(err))
		return
	}

	upload, cleanup, appErr := p.readImportUpload(reader)
	if cleanup != nil {
		// Remove the temp file and its directory on every return path, including success: the bundle
		// is not needed after inspection.
		defer cleanup()
	}
	if appErr != nil {
		p.writeAppError(w, appErr)
		return
	}

	view, appErr := p.service.CreateImportFromBundle(actorID, upload.request, upload.file, upload.size, upload.sha256)
	if appErr != nil {
		p.writeAppError(w, appErr)
		return
	}
	writeJSON(w, http.StatusCreated, view)
}

// importUpload is the validated result of reading the multipart request: the decoded JSON request
// part plus the on-disk archive and the digest computed while writing it.
type importUpload struct {
	request *model.ImportUploadRequest
	file    *os.File
	size    int64
	sha256  string
}

// readImportUpload consumes the multipart stream, requiring exactly one `request` part and one
// `bundle` part. It returns a cleanup func whenever a temporary file/directory was created, so the
// caller can remove them even when this returns an error.
func (p *Plugin) readImportUpload(reader *multipart.Reader) (*importUpload, func(), *mmmodel.AppError) {
	upload := &importUpload{}
	var cleanup func()

	// Every AppError below is constructed with a string-literal message id (rather than through a
	// shared helper taking the id as a variable) so the i18n extraction tool can discover the keys.
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			// A body that trips MaxBytesReader surfaces here; report it as too large rather than as a
			// malformed request so the client can tell the two apart.
			if isMaxBytesError(err) {
				return nil, cleanup, mmmodel.NewAppError("readImportUpload", "api.import.upload_too_large.app_error", nil, "", http.StatusRequestEntityTooLarge).Wrap(err)
			}
			return nil, cleanup, mmmodel.NewAppError("readImportUpload", "api.import.invalid_multipart.app_error", nil, "", http.StatusBadRequest).Wrap(err)
		}

		switch part.FormName() {
		case importPartRequest:
			if upload.request != nil {
				_ = part.Close()
				return nil, cleanup, mmmodel.NewAppError("readImportUpload", "api.import.duplicate_part.app_error", nil, "", http.StatusBadRequest)
			}
			req, reqErr := decodeImportRequestPart(part)
			_ = part.Close()
			if reqErr != nil {
				return nil, cleanup, reqErr
			}
			upload.request = req

		case importPartBundle:
			if upload.file != nil {
				_ = part.Close()
				return nil, cleanup, mmmodel.NewAppError("readImportUpload", "api.import.duplicate_part.app_error", nil, "", http.StatusBadRequest)
			}
			file, size, sum, tmpCleanup, bundleErr := writeBundleToTempFile(part)
			_ = part.Close()
			if tmpCleanup != nil {
				cleanup = tmpCleanup
			}
			if bundleErr != nil {
				return nil, cleanup, bundleErr
			}
			upload.file, upload.size, upload.sha256 = file, size, sum

		default:
			_ = part.Close()
			return nil, cleanup, mmmodel.NewAppError("readImportUpload", "api.import.unknown_part.app_error", nil, "", http.StatusBadRequest)
		}
	}

	if upload.request == nil {
		return nil, cleanup, mmmodel.NewAppError("readImportUpload", "api.import.missing_request_part.app_error", nil, "", http.StatusBadRequest)
	}
	if upload.file == nil {
		return nil, cleanup, mmmodel.NewAppError("readImportUpload", "api.import.missing_bundle_part.app_error", nil, "", http.StatusBadRequest)
	}
	return upload, cleanup, nil
}

// decodeImportRequestPart decodes the JSON `request` part under its own size cap, rejecting trailing
// data after the object.
func decodeImportRequestPart(part *multipart.Part) (*model.ImportUploadRequest, *mmmodel.AppError) {
	limited := io.LimitReader(part, importer.MaxRequestPartBytes+1)
	raw, err := io.ReadAll(limited)
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
	// Read one byte past the cap so an exactly-oversized upload is still detected.
	written, err := io.Copy(io.MultiWriter(file, hasher), io.LimitReader(part, importer.MaxBundleUploadBytes+1))
	if err != nil {
		if isMaxBytesError(err) {
			return nil, 0, "", cleanup, mmmodel.NewAppError("writeBundleToTempFile", "api.import.upload_too_large.app_error", nil, "", http.StatusRequestEntityTooLarge).Wrap(err)
		}
		return nil, 0, "", cleanup, mmmodel.NewAppError("writeBundleToTempFile", "api.import.upload_read_failed.app_error", nil, "", http.StatusBadRequest).Wrap(err)
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

// isMaxBytesError reports whether err is the error http.MaxBytesReader returns when the request body
// exceeds its limit, including when it surfaces wrapped by the multipart reader.
func isMaxBytesError(err error) bool {
	var maxBytesErr *http.MaxBytesError
	return errors.As(err, &maxBytesErr)
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
