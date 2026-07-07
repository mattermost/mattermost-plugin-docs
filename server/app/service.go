// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Package app provides the business-logic service layer for the Docs plugin,
// sitting between the HTTP API and the plugin store. Store errors are wrapped into
// *mmmodel.AppError so HTTP status codes reach the client.
package app

import (
	"errors"
	"net/http"
	"strings"

	mmmodel "github.com/mattermost/mattermost/server/public/model"

	"github.com/mattermost/mattermost-plugin-docs/server/model"
	"github.com/mattermost/mattermost-plugin-docs/server/store"
)

// Logger is the logging surface the service needs.
type Logger interface {
	Debug(msg string, keyValuePairs ...any)
}

type noopLogger struct{}

func (noopLogger) Debug(_ string, _ ...any) {}

// Service is the central service struct for the Docs plugin.
type Service struct {
	store *store.Store
	log   Logger
}

// New creates a Service wired to the given store and logger.
// Passing nil for log installs a no-op logger, safe for store-backed unit tests.
func New(s *store.Store, log Logger) *Service {
	if log == nil {
		log = noopLogger{}
	}
	return &Service{
		store: s,
		log:   log,
	}
}

func (s *Service) logDebug(message string, keyValuePairs ...any) {
	s.log.Debug(message, keyValuePairs...)
}

// normalizeTitle sanitizes and trims a title. Length and required-field validation are
// enforced by Page.IsValid/Space.IsValid at the store boundary — the single source of truth
// for that rule — so this only normalizes.
func normalizeTitle(title string) string {
	return strings.TrimSpace(mmmodel.SanitizeUnicode(title))
}

// normalizeAndValidatePagePatch normalizes a page update patch's Title (trimmed, with the
// result written back into the patch); Body and SearchText are left as-is. A nil field means
// "leave unchanged". Size caps and required-field checks are enforced by Page.IsValid at the
// store boundary. It defers patch-shape validation to PagePatch.IsValid.
func normalizeAndValidatePagePatch(patch *model.PagePatch) *mmmodel.AppError {
	// The patch.Title != nil guard below protects the title dereference; IsValid only
	// rejects a nil or all-nil patch and can pass with Title == nil.
	if validErr := patch.IsValid(); validErr != nil {
		return validErr
	}
	if patch.Title != nil {
		normalized := normalizeTitle(*patch.Title)
		patch.Title = &normalized
	}
	return nil
}

// PerPageDefault is the page size used when perPage is not a positive value, matching
// core's page-param convention (server/channels/web/params.go).
const PerPageDefault = 60

// PerPageMaximum is the largest page size a caller may request; larger values are
// clamped down, matching core's page-param convention.
const PerPageMaximum = 200

// paginationOffsetLimit converts a zero-based page/size into a SQL offset/limit. perPage <= 0
// is clamped to PerPageDefault and perPage > PerPageMaximum is clamped down to PerPageMaximum,
// so the returned limit is always positive and bounded — a caller can never request an
// unbounded result this way.
func paginationOffsetLimit(page, perPage int) (offset, limit int) {
	if page < 0 {
		page = 0
	}
	switch {
	case perPage <= 0:
		perPage = PerPageDefault
	case perPage > PerPageMaximum:
		perPage = PerPageMaximum
	}
	return page * perPage, perPage
}

// storeAppError maps a store sentinel error to an *AppError with the conventional status code
// and a shared message key (app.store.*); the where argument identifies the calling operation for logs.
// This is the default for translating store errors; hand-roll an inline NewAppError only when a
// case needs a message key or metadata this can't produce (e.g. CreatePage's space-not-found, or
// UpdatePage's conflict carrying ModifiedBy/ModifiedAt).
func storeAppError(where string, err error) *mmmodel.AppError {
	switch {
	case store.IsErrNotFound(err):
		return mmmodel.NewAppError(where, "app.store.not_found.app_error", nil, "", http.StatusNotFound).Wrap(err)
	case store.IsErrInvalidInput(err):
		return invalidInputAppError(where, err)
	case store.IsErrConflict(err):
		return mmmodel.NewAppError(where, "app.store.conflict.app_error", nil, "", http.StatusConflict).Wrap(err)
	case store.IsErrLimitExceeded(err):
		// Use the limit the error carries; different store methods have different bounds.
		var limitErr *store.ErrLimitExceeded
		_ = errors.As(err, &limitErr) // guaranteed true: IsErrLimitExceeded already performed this assertion
		return mmmodel.NewAppError(where, "app.store.too_large.app_error", map[string]any{"Limit": limitErr.Limit}, "", http.StatusUnprocessableEntity).Wrap(err)
	default:
		return mmmodel.NewAppError(where, "app.store.internal_error.app_error", nil, "", http.StatusInternalServerError).Wrap(err)
	}
}

// invalidInputAppError maps a store ErrInvalidInput to a 400 *AppError, preferring the
// specific validation key the store carried (from a model IsValid check) over a shared fallback.
func invalidInputAppError(where string, err error) *mmmodel.AppError {
	var invErr *store.ErrInvalidInput
	if errors.As(err, &invErr) && invErr.Reason != "" {
		return mmmodel.NewAppError(where, invErr.Reason, nil, "", http.StatusBadRequest).Wrap(err)
	}
	return mmmodel.NewAppError(where, "app.store.invalid_input.app_error", nil, "", http.StatusBadRequest).Wrap(err)
}

// restoreReasonAppError maps a store.Reason* restore-failure code (see RestorePage, RestoreSpace)
// to its app-facing error key via reasonKeys, so the store communicates which condition failed
// without naming the app-facing message key itself. Returns nil if err isn't an ErrInvalidInput
// carrying one of reasonKeys, leaving the caller to fall back to storeAppError.
func restoreReasonAppError(where string, err error, reasonKeys map[string]string) *mmmodel.AppError {
	var invErr *store.ErrInvalidInput
	if !errors.As(err, &invErr) {
		return nil
	}
	key, ok := reasonKeys[invErr.Reason]
	if !ok {
		return nil
	}
	return mmmodel.NewAppError(where, key, nil, "", http.StatusBadRequest).Wrap(err)
}
