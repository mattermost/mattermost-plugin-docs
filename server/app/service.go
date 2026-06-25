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

// Service is the central service struct for the Docs plugin.
type Service struct {
	store  *store.Store
	client *pluginapi.Client
}

// New creates a Service wired to the given store and pluginapi client.
// The client may be nil in store-backed unit tests that seed data directly.
func New(s *store.Store, client *pluginapi.Client) *Service {
	return &Service{
		store:  s,
		client: client,
	}
}

// validateTitle sanitizes and validates an entity title, returning the normalized form.
// keyPrefix is the message-key namespace (e.g. "app.page.create").
func validateTitle(where, keyPrefix, title string, maxRunes int) (string, *mmmodel.AppError) {
	title = strings.TrimSpace(mmmodel.SanitizeUnicode(title))
	if title == "" {
		return "", mmmodel.NewAppError(where, keyPrefix+".missing_title.app_error", nil, "", http.StatusBadRequest)
	}
	if utf8.RuneCountInString(title) > maxRunes {
		return "", mmmodel.NewAppError(where, keyPrefix+".title_too_long.app_error", map[string]any{"MaxLength": maxRunes}, "", http.StatusBadRequest)
	}
	return title, nil
}

// validatePagePatch enforces the title/body/searchText caps on a page update patch. A
// nil field means "leave unchanged". Rejects an all-nil patch (no-op) and a searchText
// supplied without a body change (searchText is the body's plain-text projection).
func validatePagePatch(where string, patch *model.PagePatch) *mmmodel.AppError {
	if patch == nil || (patch.Title == nil && patch.Body == nil && patch.SearchText == nil) {
		return mmmodel.NewAppError(where, "app.page.update.nothing_to_update.app_error", nil, "", http.StatusBadRequest)
	}
	if patch.Title != nil {
		if _, titleErr := validateTitle(where, "app.page.update", *patch.Title, model.PageTitleMaxRunes); titleErr != nil {
			return titleErr
		}
	}
	if patch.SearchText != nil && patch.Body == nil {
		return mmmodel.NewAppError(where, "app.page.update.search_text_without_content.app_error", nil, "", http.StatusBadRequest)
	}
	if patch.Body != nil && len(*patch.Body) > model.PageBodyMaxBytes {
		return mmmodel.NewAppError(where, "app.page.update.body_too_long.app_error", map[string]any{"MaxBytes": model.PageBodyMaxBytes}, "", http.StatusBadRequest)
	}
	if patch.SearchText != nil && len(*patch.SearchText) > model.PageSearchTextMaxBytes {
		return mmmodel.NewAppError(where, "app.page.update.search_text_too_long.app_error", map[string]any{"MaxBytes": model.PageSearchTextMaxBytes}, "", http.StatusBadRequest)
	}
	return nil
}

// paginationOffsetLimit converts a zero-based page/size into a SQL offset/limit;
// perPage <= 0 means "no limit" and returns (0, 0).
func paginationOffsetLimit(page, perPage int) (offset, limit int) {
	if page < 0 {
		page = 0
	}
	if perPage > 0 {
		return page * perPage, perPage
	}
	return 0, 0
}

// storeAppError maps a store sentinel error to an *AppError with the conventional status
// code and a message key under keyPrefix (e.g. "app.space.get" -> "app.space.get.not_found.app_error").
// Methods needing bespoke handling (distinct keys, conflict metadata) build their own instead.
func storeAppError(where, keyPrefix string, err error) *mmmodel.AppError {
	switch {
	case store.IsErrNotFound(err):
		return mmmodel.NewAppError(where, keyPrefix+".not_found.app_error", nil, "", http.StatusNotFound).Wrap(err)
	case store.IsErrInvalidInput(err):
		return mmmodel.NewAppError(where, keyPrefix+".invalid_input.app_error", nil, "", http.StatusBadRequest).Wrap(err)
	case store.IsErrConflict(err):
		return mmmodel.NewAppError(where, keyPrefix+".conflict.app_error", nil, "", http.StatusConflict).Wrap(err)
	case store.IsErrLimitExceeded(err):
		// Use the limit the error carries; different store methods have different bounds.
		limit := store.MaxPageDescendantsLimit
		var limitErr *store.ErrLimitExceeded
		if errors.As(err, &limitErr) {
			limit = limitErr.Limit
		}
		return mmmodel.NewAppError(where, keyPrefix+".too_large.app_error", map[string]any{"Limit": limit}, "", http.StatusUnprocessableEntity).Wrap(err)
	default:
		return mmmodel.NewAppError(where, keyPrefix+".app_error", nil, "", http.StatusInternalServerError).Wrap(err)
	}
}
