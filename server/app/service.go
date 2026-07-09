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
	"unicode/utf8"

	mmmodel "github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/pluginapi"

	"github.com/mattermost/mattermost-plugin-docs/server/model"
	"github.com/mattermost/mattermost-plugin-docs/server/store"
)

// Logger is the logging surface the service needs.
type Logger interface {
	Debug(msg string, keyValuePairs ...any)
	Warn(msg string, keyValuePairs ...any)
}

type noopLogger struct{}

func (noopLogger) Debug(_ string, _ ...any) {}
func (noopLogger) Warn(_ string, _ ...any)  {}

// Service is the central service struct for the Docs plugin.
type Service struct {
	store  *store.Store
	log    Logger
	client *pluginapi.Client
}

// New creates a Service wired to the given store, logger, and optional pluginapi client.
// Passing nil for store panics immediately; passing nil for log installs a no-op logger.
// client may be nil in store-only unit tests that never exercise channel/team operations.
func New(s *store.Store, log Logger, client *pluginapi.Client) *Service {
	if s == nil {
		panic("app.New: store must not be nil")
	}
	if log == nil {
		log = noopLogger{}
	}
	return &Service{
		store:  s,
		log:    log,
		client: client,
	}
}

// validateTitle sanitizes and validates an entity title, returning the normalized form.
// where identifies the calling operation for logs; the message keys are shared across callers.
func validateTitle(where, title string, maxRunes int) (string, *mmmodel.AppError) {
	title = normalizeTitle(title)
	if title == "" {
		return "", mmmodel.NewAppError(where, "app.shared.title_required.app_error", nil, "", http.StatusBadRequest)
	}
	if utf8.RuneCountInString(title) > maxRunes {
		return "", mmmodel.NewAppError(where, "app.shared.title_too_long.app_error", map[string]any{"MaxLength": maxRunes}, "", http.StatusBadRequest)
	}
	return title, nil
}

// validateSpaceMutableFields enforces the Description/Icon size caps shared by CreateSpace and
// UpdateSpace. where identifies the calling operation for logs; the message keys are shared
// across callers.
func validateSpaceMutableFields(where, description, icon string) *mmmodel.AppError {
	if utf8.RuneCountInString(description) > model.SpaceDescriptionMaxRunes {
		return mmmodel.NewAppError(where, "app.shared.description_too_long.app_error", map[string]any{"MaxLength": model.SpaceDescriptionMaxRunes}, "", http.StatusBadRequest)
	}
	if len(icon) > model.SpaceIconMaxBytes {
		return mmmodel.NewAppError(where, "app.shared.icon_too_large.app_error", map[string]any{"MaxBytes": model.SpaceIconMaxBytes}, "", http.StatusBadRequest)
	}
	return nil
}

// sameTeamSpaces reports whether sourceSpaceID and destSpaceID belong to the same team.
// Returns bool (not AppError) on a cross-team result so each caller supplies its own message key.
func (s *Service) sameTeamSpaces(sourceSpaceID, destSpaceID string) (bool, *mmmodel.AppError) {
	sourceSpace, srcErr := s.GetSpace(sourceSpaceID)
	if srcErr != nil {
		return false, srcErr
	}
	destSpace, dstErr := s.GetSpace(destSpaceID)
	if dstErr != nil {
		return false, dstErr
	}
	return sourceSpace.TeamId == destSpace.TeamId, nil
}

func normalizeTitle(title string) string {
	return strings.TrimSpace(mmmodel.SanitizeUnicode(title))
}

// normalizeAndValidatePagePatch normalizes a page update patch's Title (trimmed, empty rejected),
// with the result written back into the patch; Body and SearchText are left as-is. A nil field
// means "leave unchanged". It defers patch-shape validation to PagePatch.IsValid.
func normalizeAndValidatePagePatch(where string, patch *model.PagePatch) *mmmodel.AppError {
	// The patch.Title != nil guard below protects the title dereference; IsValid only
	// rejects a nil or all-nil patch and can pass with Title == nil.
	if validErr := patch.IsValid(); validErr != nil {
		return validErr
	}
	if patch.Title != nil {
		normalized, titleErr := validateTitle(where, *patch.Title, model.PageTitleMaxRunes)
		if titleErr != nil {
			return titleErr
		}
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

// ClampPerPage normalizes a requested page size: non-positive values default to PerPageDefault
// and values above PerPageMaximum are capped. The result is always in [1, PerPageMaximum].
func ClampPerPage(perPage int) int {
	if perPage <= 0 {
		return PerPageDefault
	}
	if perPage > PerPageMaximum {
		return PerPageMaximum
	}
	return perPage
}

// paginationOffsetLimit converts a zero-based page/size into an offset/limit. perPage <= 0
// is clamped to PerPageDefault and perPage > PerPageMaximum is clamped down to PerPageMaximum.
// The returned limit is perPage+1 so callers can pass the result directly to the store and
// detect has_more by checking whether the store returned more than perPage rows. writePaginatedJSON
// trims the slice and sets HasMore precisely using this convention.
func paginationOffsetLimit(page, perPage int) (offset, limit int) {
	if page < 0 {
		page = 0
	}
	perPage = ClampPerPage(perPage)
	return page * perPage, perPage + 1
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
	case store.IsErrCircularReference(err):
		return mmmodel.NewAppError(where, "app.page.move.circular_reference.app_error", nil, "", http.StatusBadRequest).Wrap(err)
	case store.IsErrInvalidInput(err):
		return invalidInputAppError(where, err)
	case store.IsErrConflict(err):
		return mmmodel.NewAppError(where, "app.store.conflict.app_error", nil, "", http.StatusConflict).Wrap(err)
	case store.IsErrLimitExceeded(err):
		// Use the limit the error carries; different store methods have different bounds.
		var limitErr *store.ErrLimitExceeded
		_ = errors.As(err, &limitErr) // guaranteed true: IsErrLimitExceeded already performed this assertion
		switch limitErr.Reason {
		case store.ReasonMaxDepthExceeded:
			return mmmodel.NewAppError(where, "app.page.move.max_depth_exceeded.app_error", map[string]any{"MaxDepth": limitErr.Limit}, "", http.StatusBadRequest).Wrap(err)
		case store.ReasonSubtreeMaxDepthExceeded:
			return mmmodel.NewAppError(where, "app.page.move.subtree_max_depth_exceeded.app_error", map[string]any{"MaxDepth": limitErr.Limit}, "", http.StatusBadRequest).Wrap(err)
		}
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
// to a pre-built AppError via appErrors, so the store communicates which condition failed without
// naming the app-facing message key itself. Callers construct AppErrors with string-literal IDs so
// the i18n extraction tool can discover them. Returns nil if err is not an ErrInvalidInput carrying
// one of the mapped reasons, leaving the caller to fall back to storeAppError.
func restoreReasonAppError(err error, appErrors map[string]*mmmodel.AppError) *mmmodel.AppError {
	var invErr *store.ErrInvalidInput
	if !errors.As(err, &invErr) {
		return nil
	}
	appErr, ok := appErrors[invErr.Reason]
	if !ok {
		return nil
	}
	return appErr.Wrap(err)
}
